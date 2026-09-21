package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"openmaps/internal/importer"
	"openmaps/internal/places"
)

type benchmarkReport struct {
	Schema               int             `json:"schema"`
	Started              time.Time       `json:"started"`
	URL                  string          `json:"url"`
	Seed                 uint64          `json:"seed"`
	Suites               []suiteReport   `json:"suites"`
	Keystrokes           []requestResult `json:"keystrokes"`
	Loads                []latencyReport `json:"loads"`
	Details              detailsReport   `json:"details"`
	OrdinaryOver2Seconds int             `json:"ordinary_over_2_seconds"`
}

type suiteReport struct {
	Name    string          `json:"name"`
	Order   string          `json:"order"`
	Passed  int             `json:"passed"`
	Total   int             `json:"total"`
	Results []requestResult `json:"results"`
}

type requestResult struct {
	Category string        `json:"category,omitempty"`
	Intent   string        `json:"intent,omitempty"`
	Input    string        `json:"input"`
	Biased   bool          `json:"biased,omitempty"`
	Elapsed  time.Duration `json:"elapsed"`
	Status   int           `json:"status"`
	Results  int           `json:"results"`
	FirstID  string        `json:"first_id,omitempty"`
	Passed   bool          `json:"passed"`
	Error    string        `json:"error,omitempty"`
}

type latencyReport struct {
	Name         string        `json:"name"`
	Concurrency  int           `json:"concurrency"`
	Duration     time.Duration `json:"duration"`
	Requests     int64         `json:"requests"`
	Errors       int64         `json:"errors"`
	Over2Seconds int64         `json:"over_2_seconds"`
	P50          time.Duration `json:"p50,omitempty"`
	P95          time.Duration `json:"p95,omitempty"`
	P99          time.Duration `json:"p99,omitempty"`
	Maximum      time.Duration `json:"maximum,omitempty"`
	Throughput   float64       `json:"throughput_per_second"`
	ErrorRate    float64       `json:"error_rate"`
}

type detailsReport struct {
	Checked int      `json:"checked"`
	Failed  int      `json:"failed"`
	Errors  []string `json:"errors,omitempty"`
}

func benchmark(args []string) error {
	fs := flag.NewFlagSet("benchmark", flag.ContinueOnError)
	base := fs.String("url", "http://127.0.0.1:18090", "experimental adapter base URL")
	checksPaths := fs.String("checks", "config/us-query-checks.json,config/us-viewport-query-checks.json", "comma-separated expectation files")
	reportPath := fs.String("report", "", "output JSON report")
	seed := fs.Uint64("seed", 1, "deterministic randomized order seed")
	loadDuration := fs.Duration("load-duration", 10*time.Minute, "duration for each concurrency phase; zero skips load")
	loadLevels := fs.String("load-concurrency", "1,8,16,32", "comma-separated load concurrencies")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *reportPath == "" {
		return fmt.Errorf("benchmark requires -report")
	}
	report := benchmarkReport{Schema: 1, Started: time.Now().UTC(), URL: strings.TrimRight(*base, "/"), Seed: *seed}
	client := &http.Client{Timeout: 2500 * time.Millisecond}
	type checkSuite struct {
		name   string
		checks []importer.QueryCheck
	}
	checkSuites := []checkSuite{}
	allChecks := []importer.QueryCheck{}
	for _, path := range strings.Split(*checksPaths, ",") {
		path = strings.TrimSpace(path)
		checks, err := importer.ReadQueryChecks(path)
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		checkSuites = append(checkSuites, checkSuite{name: name, checks: checks})
		allChecks = append(allChecks, checks...)
	}
	for round, order := range []string{"randomized-first-pass", "randomized-warm"} {
		for suiteIndex, source := range checkSuites {
			checks := append([]importer.QueryCheck(nil), source.checks...)
			r := rand.New(rand.NewPCG(*seed+uint64(round*len(checkSuites)+suiteIndex), *seed^0x9e3779b97f4a7c15+uint64(round*len(checkSuites)+suiteIndex)))
			r.Shuffle(len(checks), func(i, j int) { checks[i], checks[j] = checks[j], checks[i] })
			suite := suiteReport{Name: source.name, Order: order, Total: len(checks)}
			for _, check := range checks {
				result, ids, entities := runRequest(context.Background(), client, report.URL, check.Input, check.LocationBias)
				result.Category, result.Intent, result.Biased = check.Category, check.Intent, check.LocationBias != nil
				for i, id := range ids {
					report.Details.Checked++
					detail, err := fetchDetails(context.Background(), client, report.URL, id)
					if err != nil {
						report.Details.Failed++
						report.Details.Errors = append(report.Details.Errors, id+": "+err.Error())
						continue
					}
					entities[i].Location = detail.Location
				}
				violations := evaluateCheck(check, entities)
				if len(violations) > 0 {
					if result.Error != "" {
						result.Error += "; "
					}
					result.Error += strings.Join(violations, "; ")
				}
				result.Passed = result.Error == "" && result.Status == 200
				if result.Passed {
					suite.Passed++
				}
				if result.Elapsed > 2*time.Second {
					report.OrdinaryOver2Seconds++
				}
				suite.Results = append(suite.Results, result)
			}
			report.Suites = append(report.Suites, suite)
		}
	}
	traces := []string{"u", "up", "ups", "ups s", "ups st", "ups store", "m", "ma", "main", "main st", "main street", "In-N-Out Los Angeles", "Pennsylvania Avenue, Washington, DC", "Providence", "Space Needle", "zzxnoresult"}
	for _, input := range traces {
		result, _, _ := runRequest(context.Background(), client, report.URL, input, nil)
		if result.Elapsed > 2*time.Second {
			report.OrdinaryOver2Seconds++
		}
		report.Keystrokes = append(report.Keystrokes, result)
	}
	if *loadDuration > 0 {
		levels, err := parseLevels(*loadLevels)
		if err != nil {
			return err
		}
		queries := make([]string, 0, len(allChecks)+len(traces))
		for _, c := range allChecks {
			queries = append(queries, c.Input)
		}
		queries = append(queries, traces...)
		for _, level := range levels {
			report.Loads = append(report.Loads, runLoad(client, report.URL, queries, *loadDuration, level, *seed+uint64(level)))
		}
	}
	if err := writeJSON(*reportPath, report); err != nil {
		return err
	}
	encoded, _ := json.MarshalIndent(report, "", "  ")
	fmt.Println(string(encoded))
	for _, suite := range report.Suites {
		if suite.Passed < suite.Total {
			return fmt.Errorf("suite %s %s passed %d/%d", suite.Name, suite.Order, suite.Passed, suite.Total)
		}
	}
	if report.Details.Failed != 0 {
		return fmt.Errorf("details failures: %d", report.Details.Failed)
	}
	return nil
}

type responseEntity struct {
	ID, Kind, Name string
	Location       places.Location
}

func runRequest(ctx context.Context, client *http.Client, base, input string, bias *places.Viewport) (requestResult, []string, []responseEntity) {
	result := requestResult{Input: input}
	body := map[string]any{"input": input}
	if bias != nil {
		body["locationBias"] = map[string]any{"rectangle": map[string]any{"low": map[string]float64{"latitude": bias.South, "longitude": bias.West}, "high": map[string]float64{"latitude": bias.North, "longitude": bias.East}}}
	}
	encoded, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/places:autocomplete", bytes.NewReader(encoded))
	req.Header.Set("Content-Type", "application/json")
	started := time.Now()
	response, err := client.Do(req)
	result.Elapsed = time.Since(started)
	if err != nil {
		result.Error = err.Error()
		return result, nil, nil
	}
	defer response.Body.Close()
	result.Status = response.StatusCode
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		result.Error = err.Error()
		return result, nil, nil
	}
	if response.StatusCode != 200 {
		result.Error = string(raw)
		return result, nil, nil
	}
	var payload struct {
		Suggestions []struct {
			PlacePrediction struct {
				PlaceID    string `json:"placeId"`
				Structured struct {
					Main struct {
						Text string `json:"text"`
					} `json:"mainText"`
				} `json:"structuredFormat"`
				Types []string `json:"types"`
			} `json:"placePrediction"`
		} `json:"suggestions"`
	}
	if err = json.Unmarshal(raw, &payload); err != nil {
		result.Error = err.Error()
		return result, nil, nil
	}
	ids := []string{}
	entities := []responseEntity{}
	for _, suggestion := range payload.Suggestions {
		p := suggestion.PlacePrediction
		ids = append(ids, p.PlaceID)
		kind := ""
		for _, typ := range p.Types {
			switch typ {
			case "locality", "political":
				if kind == "" {
					kind = "area"
				}
			case "route":
				kind = "street"
			case "establishment":
				kind = "business"
			case "street_address":
				kind = "address"
			}
		}
		entities = append(entities, responseEntity{ID: p.PlaceID, Kind: kind, Name: p.Structured.Main.Text})
	}
	result.Results = len(ids)
	if len(ids) > 0 {
		result.FirstID = ids[0]
	}
	return result, ids, entities
}

func fetchDetails(ctx context.Context, client *http.Client, base, id string) (responseEntity, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/places/"+id, nil)
	req.Header.Set("X-Goog-FieldMask", "id,displayName,location,types")
	response, err := client.Do(req)
	if err != nil {
		return responseEntity{}, err
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != 200 {
		return responseEntity{}, fmt.Errorf("HTTP %d: %s", response.StatusCode, raw)
	}
	var payload struct {
		ID          string `json:"id"`
		DisplayName struct {
			Text string `json:"text"`
		} `json:"displayName"`
		Location struct{ Latitude, Longitude float64 } `json:"location"`
		Types    []string                              `json:"types"`
	}
	if err = json.Unmarshal(raw, &payload); err != nil {
		return responseEntity{}, err
	}
	kind := ""
	for _, typ := range payload.Types {
		switch typ {
		case "locality", "political":
			if kind == "" {
				kind = "area"
			}
		case "route":
			kind = "street"
		case "establishment":
			kind = "business"
		case "street_address":
			kind = "address"
		}
	}
	return responseEntity{ID: payload.ID, Kind: kind, Name: payload.DisplayName.Text, Location: places.Location{Lat: payload.Location.Latitude, Lng: payload.Location.Longitude}}, nil
}

func evaluateCheck(check importer.QueryCheck, entities []responseEntity) []string {
	violations := []string{}
	if check.Empty {
		if len(entities) != 0 {
			violations = append(violations, "expected empty")
		}
		return violations
	}
	if len(entities) == 0 {
		return []string{"no results"}
	}
	first := entities[0]
	if check.FirstID != "" && first.ID != check.FirstID {
		violations = append(violations, "first ID "+first.ID)
	}
	if check.FirstKind != "" && first.Kind != check.FirstKind {
		violations = append(violations, "first kind "+first.Kind)
	}
	if check.FirstName != "" && first.Name != check.FirstName {
		violations = append(violations, "first name "+first.Name)
	}
	if check.Near != nil && places.DistanceMeters(first.Location, places.Location{Lat: check.Near.Lat, Lng: check.Near.Lng}) > check.Near.RadiusMeters {
		violations = append(violations, "first location outside radius")
	}
	if len(entities) < check.MinResults {
		violations = append(violations, "too few results")
	}
	if check.LocationBias != nil {
		v := *check.LocationBias
		if check.FirstInsideBias && !v.Contains(first.Location) {
			violations = append(violations, "first outside bias")
		}
		outside := false
		allOutside := true
		last := -1.0
		for _, entity := range entities {
			inside := v.Contains(entity.Location)
			outside = outside || !inside
			allOutside = allOutside && !inside
			distance := places.DistanceMeters(v.Center(), entity.Location)
			if check.DistanceOrderedFromBiasCenter && last >= 0 && distance+0.01 < last {
				violations = append(violations, "distance order")
				break
			}
			last = distance
		}
		if check.OutsideResultRequired && !outside {
			violations = append(violations, "no outside result")
		}
		if check.AllResultsOutsideBias && !allOutside {
			violations = append(violations, "result inside empty bias")
		}
	}
	return violations
}

func parseLevels(raw string) ([]int, error) {
	out := []int{}
	for _, part := range strings.Split(raw, ",") {
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(part), "%d", &n); err != nil || n < 1 || n > 128 {
			return nil, fmt.Errorf("invalid concurrency %q", part)
		}
		out = append(out, n)
	}
	return out, nil
}

func runLoad(client *http.Client, base string, queries []string, duration time.Duration, concurrency int, seed uint64) latencyReport {
	report := latencyReport{Name: "mixed-autocomplete", Concurrency: concurrency, Duration: duration}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	latencies := []time.Duration{}
	var requests, errs, over2 atomic.Int64
	started := time.Now()
	for worker := 0; worker < concurrency; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			r := rand.New(rand.NewPCG(seed+uint64(worker), seed^uint64(worker+1)))
			for ctx.Err() == nil {
				input := queries[r.IntN(len(queries))]
				result, _, _ := runRequest(ctx, client, base, input, nil)
				if ctx.Err() != nil {
					return
				}
				requests.Add(1)
				if result.Status != 200 || result.Error != "" {
					errs.Add(1)
				}
				if result.Elapsed > 2*time.Second {
					over2.Add(1)
				}
				mu.Lock()
				latencies = append(latencies, result.Elapsed)
				mu.Unlock()
			}
		}(worker)
	}
	wg.Wait()
	elapsed := time.Since(started)
	report.Requests, report.Errors, report.Over2Seconds = requests.Load(), errs.Load(), over2.Load()
	if report.Requests > 0 {
		report.Throughput = float64(report.Requests) / elapsed.Seconds()
		report.ErrorRate = float64(report.Errors) / float64(report.Requests)
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	if len(latencies) > 0 {
		report.P50 = percentile(latencies, .50)
		report.P95 = percentile(latencies, .95)
		report.P99 = percentile(latencies, .99)
		report.Maximum = latencies[len(latencies)-1]
	}
	return report
}

func percentile(values []time.Duration, p float64) time.Duration {
	index := int(math.Ceil(float64(len(values))*p)) - 1
	if index < 0 {
		index = 0
	}
	return values[index]
}
