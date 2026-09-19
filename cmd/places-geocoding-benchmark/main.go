// places-geocoding-benchmark evaluates a verified Open Maps generation against
// an external, checksum-pinned address corpus. It never runs in the default
// test suite and does not publish or activate a generation.
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"openmaps/internal/geocoding"
	"openmaps/internal/importer"
	"openmaps/internal/places"
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
)

type datasetConfig struct {
	Schema   int    `json:"schema"`
	Name     string `json:"name"`
	Revision string `json:"revision"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	Bytes    int64  `json:"bytes"`
	Records  int    `json:"records"`
	Citation string `json:"citation"`
	Terms    string `json:"terms"`
	Upstream string `json:"upstream"`
}

type address struct {
	Street   string   `json:"streetAddress"`
	Locality string   `json:"addressLocality"`
	Region   string   `json:"addressRegion"`
	Postcode string   `json:"postalCode"`
	Country  string   `json:"addressCountry"`
	Lat      *float64 `json:"latitude"`
	Lng      *float64 `json:"longitude"`
}

type record struct {
	Input  string `json:"input"`
	Target string `json:"target"`
	Aux    struct {
		Address address `json:"address"`
	} `json:"aux"`
}

type modeResult struct {
	Queries          int            `json:"queries"`
	Matched          int            `json:"matched"`
	Ambiguous        int            `json:"ambiguous"`
	Within100Meters  int            `json:"within_100_meters"`
	Within1000Meters int            `json:"within_1000_meters"`
	Within10KMeters  int            `json:"within_10000_meters"`
	MedianMeters     *float64       `json:"median_meters,omitempty"`
	P95Meters        *float64       `json:"p95_meters,omitempty"`
	Outcomes         map[string]int `json:"outcomes"`
	Returned         []sample       `json:"returned_samples"`
	Samples          []sample       `json:"nonmatching_samples"`
	distances        []float64
}

type sample struct {
	Input          string   `json:"input"`
	CanonicalInput string   `json:"canonical_input,omitempty"`
	Target         string   `json:"target"`
	Outcome        string   `json:"outcome"`
	FirstID        string   `json:"first_id,omitempty"`
	BestID         string   `json:"best_id,omitempty"`
	DistanceMeters *float64 `json:"distance_meters,omitempty"`
}

type report struct {
	Schema           int        `json:"schema"`
	Dataset          string     `json:"dataset"`
	DatasetRevision  string     `json:"dataset_revision"`
	DatasetSHA256    string     `json:"dataset_sha256"`
	Generation       string     `json:"generation"`
	ManifestSHA256   string     `json:"manifest_sha256"`
	NormalizedSHA256 string     `json:"normalized_sha256"`
	DataSHA256       string     `json:"data_sha256,omitempty"`
	RecordsRead      int        `json:"records_read"`
	USRecords        int        `json:"us_records"`
	InvalidTargets   int        `json:"invalid_targets"`
	Limit            int        `json:"limit,omitempty"`
	Verbatim         modeResult `json:"verbatim"`
	Canonical        modeResult `json:"component_canonical"`
	Citation         string     `json:"citation"`
	Terms            string     `json:"terms"`
}

type forwarder interface {
	Forward(context.Context, string) (geocoding.Response, error)
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	lookup := flag.String("lookup", "", "verified lookup generation")
	configPath := flag.String("dataset-config", "config/benchmarks/messy-streets-gold.json", "pinned external dataset metadata")
	datasetPath := flag.String("dataset", "data/benchmarks/messy-streets-gold.jsonl.gz", "downloaded compressed JSONL")
	reportPath := flag.String("report", "data/benchmarks/messy-streets-report.json", "output report")
	fetch := flag.Bool("fetch", false, "download the missing pinned dataset")
	limit := flag.Int("limit", 0, "maximum explicit-US records; zero evaluates all")
	flag.Parse()
	if flag.NArg() != 0 || *lookup == "" || *limit < 0 {
		return fmt.Errorf("usage: places-geocoding-benchmark -lookup DIR [-fetch] [-limit N]")
	}
	var config datasetConfig
	if err := readJSON(*configPath, &config); err != nil {
		return err
	}
	if config.Schema != 1 || config.Name == "" || config.Revision == "" || config.URL == "" || config.SHA256 == "" || config.Bytes <= 0 || config.Records <= 0 || config.Citation == "" || config.Terms == "" {
		return fmt.Errorf("incomplete benchmark dataset configuration")
	}
	if err := os.MkdirAll(filepath.Dir(*datasetPath), 0o755); err != nil {
		return err
	}
	if *fetch {
		if err := fetchDataset(context.Background(), config, *datasetPath); err != nil {
			return err
		}
	}
	if err := importer.Verify(*datasetPath, config.SHA256); err != nil {
		return err
	}
	info, err := os.Stat(*datasetPath)
	if err != nil {
		return err
	}
	if info.Size() != config.Bytes {
		return fmt.Errorf("dataset size mismatch: got %d want %d", info.Size(), config.Bytes)
	}
	store, err := placeduckdb.Open(*lookup)
	if err != nil {
		return err
	}
	defer store.Close()
	var manifest placeduckdb.Manifest
	if err = readJSON(filepath.Join(*lookup, placeduckdb.ManifestName), &manifest); err != nil {
		return err
	}
	manifestSHA256, err := importer.Checksum(filepath.Join(*lookup, placeduckdb.ManifestName))
	if err != nil {
		return err
	}
	result, err := evaluate(context.Background(), store, *datasetPath, *limit)
	if err != nil {
		return err
	}
	if *limit == 0 && result.RecordsRead != config.Records {
		return fmt.Errorf("dataset record count: got %d want %d", result.RecordsRead, config.Records)
	}
	result.Schema = 1
	result.Dataset = config.Name
	result.DatasetRevision = config.Revision
	result.DatasetSHA256 = config.SHA256
	result.Generation = *lookup
	result.ManifestSHA256 = manifestSHA256
	result.NormalizedSHA256 = manifest.NormalizedSHA256
	result.DataSHA256 = manifest.DataSHA256
	result.Limit = *limit
	result.Citation = config.Citation
	result.Terms = config.Terms
	if err = os.MkdirAll(filepath.Dir(*reportPath), 0o755); err != nil {
		return err
	}
	if err = importer.WriteJSON(*reportPath, result); err != nil {
		return err
	}
	encoded, _ := json.MarshalIndent(result, "", "  ")
	fmt.Println(string(encoded))
	return nil
}

func readJSON(path string, value any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

func fetchDataset(ctx context.Context, config datasetConfig, path string) error {
	if _, err := os.Stat(path); err == nil {
		return importer.Verify(path, config.SHA256)
	} else if !os.IsNotExist(err) {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, config.URL, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", config.URL, response.StatusCode)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".messy-streets-*")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if _, err = io.Copy(temporary, response.Body); err != nil {
		temporary.Close()
		return err
	}
	if err = temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	if err = importer.Verify(temporary.Name(), config.SHA256); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), path)
}

func evaluate(ctx context.Context, service forwarder, path string, limit int) (report, error) {
	result := report{Verbatim: newMode(), Canonical: newMode()}
	f, err := os.Open(path)
	if err != nil {
		return result, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return result, err
	}
	defer gz.Close()
	scanner := bufio.NewScanner(gz)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	for scanner.Scan() {
		result.RecordsRead++
		var item record
		if err = json.Unmarshal(normalizeNaN(scanner.Bytes()), &item); err != nil {
			return result, fmt.Errorf("record %d: %w", result.RecordsRead, err)
		}
		if !isUS(item.Aux.Address.Country) {
			continue
		}
		result.USRecords++
		if limit > 0 && result.USRecords > limit {
			result.USRecords--
			break
		}
		if item.Aux.Address.Lat == nil || item.Aux.Address.Lng == nil {
			result.InvalidTargets++
			continue
		}
		target := places.Location{Lat: *item.Aux.Address.Lat, Lng: *item.Aux.Address.Lng}
		if target.Lat < -90 || target.Lat > 90 || target.Lng < -180 || target.Lng > 180 {
			result.InvalidTargets++
			continue
		}
		canonical := canonicalInput(item.Aux.Address)
		evaluateOne(ctx, service, &result.Verbatim, item.Input, canonical, item.Target, target)
		evaluateOne(ctx, service, &result.Canonical, canonical, canonical, item.Target, target)
	}
	if err = scanner.Err(); err != nil {
		return result, err
	}
	finish(&result.Verbatim)
	finish(&result.Canonical)
	return result, nil
}

// normalizeNaN converts the bare NaN tokens emitted by the upstream Python
// serializer to JSON null without altering the same text inside a string. The
// benchmark remains strict about all other JSON syntax.
func normalizeNaN(data []byte) []byte {
	if !bytes.Contains(data, []byte("NaN")) {
		return data
	}
	result := make([]byte, 0, len(data))
	inString := false
	escaped := false
	for index := 0; index < len(data); {
		current := data[index]
		if inString {
			result = append(result, current)
			if escaped {
				escaped = false
			} else if current == '\\' {
				escaped = true
			} else if current == '"' {
				inString = false
			}
			index++
			continue
		}
		if current == '"' {
			inString = true
			result = append(result, current)
			index++
			continue
		}
		if index+3 <= len(data) && string(data[index:index+3]) == "NaN" {
			result = append(result, "null"...)
			index += 3
			continue
		}
		result = append(result, current)
		index++
	}
	return result
}

func newMode() modeResult {
	return modeResult{Outcomes: map[string]int{}, Returned: []sample{}, Samples: []sample{}}
}

func evaluateOne(ctx context.Context, service forwarder, mode *modeResult, input, canonical, targetHash string, target places.Location) {
	mode.Queries++
	response, err := service.Forward(ctx, input)
	outcome := response.Outcome
	if outcome == "" {
		outcome = "error"
	}
	mode.Outcomes[outcome]++
	entry := sample{Input: input, Target: targetHash, Outcome: outcome}
	if input != canonical {
		entry.CanonicalInput = canonical
	}
	if err != nil || len(response.Results) == 0 {
		appendSample(mode, entry)
		return
	}
	mode.Matched++
	if len(response.Results) > 1 {
		mode.Ambiguous++
	}
	best := response.Results[0]
	distance := geocoding.DistanceMeters(target, best.Entity.Location)
	for _, candidate := range response.Results[1:] {
		candidateDistance := geocoding.DistanceMeters(target, candidate.Entity.Location)
		if candidateDistance < distance {
			best, distance = candidate, candidateDistance
		}
	}
	entry.FirstID = response.Results[0].Entity.ID
	entry.DistanceMeters = &distance
	entry.BestID = best.Entity.ID
	if len(mode.Returned) < 50 {
		mode.Returned = append(mode.Returned, entry)
	}
	mode.distances = append(mode.distances, distance)
	if distance <= 100 {
		mode.Within100Meters++
	}
	if distance <= 1000 {
		mode.Within1000Meters++
	}
	if distance <= 10000 {
		mode.Within10KMeters++
	} else {
		appendSample(mode, entry)
	}
}

func appendSample(mode *modeResult, value sample) {
	if len(mode.Samples) < 50 {
		mode.Samples = append(mode.Samples, value)
	}
}

func finish(mode *modeResult) {
	if len(mode.distances) == 0 {
		mode.distances = nil
		return
	}
	sort.Float64s(mode.distances)
	median := percentile(mode.distances, 0.5)
	p95 := percentile(mode.distances, 0.95)
	mode.MedianMeters, mode.P95Meters = &median, &p95
	mode.distances = nil
}

func percentile(sorted []float64, fraction float64) float64 {
	position := fraction * float64(len(sorted)-1)
	lower := int(math.Floor(position))
	upper := int(math.Ceil(position))
	if lower == upper {
		return sorted[lower]
	}
	weight := position - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

func isUS(country string) bool {
	switch strings.ToLower(strings.TrimSpace(country)) {
	case "us", "usa", "u.s.", "u.s.a.", "united states", "united-states", "united states (usa)", "united states of america":
		return true
	default:
		return false
	}
}

func canonicalInput(value address) string {
	parts := []string{value.Street, value.Locality, value.Region, value.Postcode, "US"}
	kept := parts[:0]
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, ", ")
}
