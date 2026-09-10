package qualification

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"
)

const ResponseLimit = 64 << 20
const Mask = "routes.distanceMeters,routes.duration,routes.staticDuration,routes.polyline"

type Case struct {
	Name   string `json:"name"`
	State  string `json:"state,omitempty"`
	From   Point  `json:"from"`
	To     Point  `json:"to"`
	Expect string `json:"expect"`
}
type Offline struct {
	Case     Case   `json:"case"`
	Outcome  string `json:"outcome"`
	Verified bool   `json:"verified"`
	Route    struct {
		Meters, Seconds float64
		Geometry        []Point
	} `json:"route"`
}

func EachRow(path string, fn func([]byte) error) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 65536), ResponseLimit+1)
	for s.Scan() {
		if len(s.Bytes()) > ResponseLimit {
			return errors.New("offline row exceeds budget")
		}
		if e = fn(s.Bytes()); e != nil {
			return e
		}
	}
	return s.Err()
}
func LoadCases(paths []string) ([]Offline, error) {
	cases := []Offline{}
	retainedBytes := 0
	seen := map[string]Offline{}
	for _, path := range paths {
		e := EachRow(path, func(raw []byte) error {
			retainedBytes += len(raw)
			if retainedBytes > 128<<20 {
				return errors.New("offline evidence exceeds 128 MiB budget")
			}
			var row Offline
			if e := json.Unmarshal(raw, &row); e != nil {
				return e
			}
			if row.Case.Name == "" {
				return nil
			}
			expect := row.Case.Expect
			if expect == "" {
				expect = "routed"
			}
			if row.Outcome != expect || !row.Verified && row.Outcome != "unreachable" && row.Outcome != "unsnappable" {
				return fmt.Errorf("case %s lacks successful offline qualification", row.Case.Name)
			}
			if prior, ok := seen[row.Case.Name]; ok {
				if !reflect.DeepEqual(prior, row) {
					return errors.New("conflicting duplicate offline case")
				}
				return nil
			}
			if !valid(row.Case.From) || !valid(row.Case.To) {
				return errors.New("invalid case coordinate")
			}
			seen[row.Case.Name] = row
			cases = append(cases, row)
			if len(cases) > 500 {
				return errors.New("more than 500 cases")
			}
			return nil
		})
		if e != nil {
			return nil, e
		}
	}
	if len(cases) == 0 {
		return nil, errors.New("require verified offline cases")
	}
	return cases, nil
}
func Body(c Case) []byte {
	point := func(p Point) any {
		return map[string]any{"location": map[string]any{"latLng": map[string]float64{"longitude": p[0], "latitude": p[1]}}}
	}
	b, _ := json.Marshal(map[string]any{"origin": point(c.From), "destination": point(c.To), "polylineEncoding": "GEO_JSON_LINESTRING"})
	return b
}
func CheckResponse(row Offline, status int, raw []byte) (string, error) {
	var got struct {
		Routes []struct {
			Distance                 *int `json:"distanceMeters"`
			Duration, StaticDuration string
			Polyline                 struct {
				Line struct {
					Type        string
					Coordinates []Point
				} `json:"geoJsonLinestring"`
			}
		}
		Openmaps struct{ Outcome, Profile, Snapshot, Attribution string }
	}
	if len(raw) > ResponseLimit {
		return "", errors.New("response exceeds budget")
	}
	if e := json.Unmarshal(raw, &got); e != nil {
		return "", e
	}
	meta := got.Openmaps
	wantStatus := 200
	if row.Outcome == "unsnappable" {
		wantStatus = 400
	}
	if status != wantStatus || meta.Outcome != row.Outcome {
		return meta.Snapshot, errors.New("status/outcome differs from verified offline case")
	}
	if meta.Profile != "osm-scout-public-auto-v1" || meta.Snapshot == "" || meta.Attribution == "" {
		return meta.Snapshot, errors.New("missing profile/source identity")
	}
	if row.Outcome == "routed" {
		if len(got.Routes) != 1 {
			return meta.Snapshot, errors.New("expected one route")
		}
		r := got.Routes[0]
		if r.Distance == nil || *r.Distance != int(math.Round(row.Route.Meters)) || r.Duration != fmt.Sprintf("%.0fs", math.Round(row.Route.Seconds)) || r.StaticDuration != r.Duration {
			return meta.Snapshot, errors.New("cost differs from source-verified path")
		}
		if r.Polyline.Line.Type != "LineString" || !reflect.DeepEqual(r.Polyline.Line.Coordinates, row.Route.Geometry) {
			return meta.Snapshot, errors.New("full geometry differs from source-verified path")
		}
	} else if len(got.Routes) != 0 {
		return meta.Snapshot, errors.New("unexpected route on failed case")
	}
	return meta.Snapshot, nil
}

type RequestResult struct {
	Case     string  `json:"case"`
	Worker   int     `json:"worker"`
	Index    int     `json:"index"`
	Started  float64 `json:"started_seconds"`
	Status   int     `json:"status"`
	Bytes    int     `json:"bytes"`
	SHA256   string  `json:"sha256"`
	Snapshot string  `json:"snapshot"`
	Passed   bool    `json:"passed"`
	Error    string  `json:"error,omitempty"`
	Seconds  float64 `json:"seconds"`
}
type HTTPOptions struct {
	URL, Out string
	Offline  []string
	Workers  int
	Duration time.Duration
}

// VerifyHTTP bounds active responses by workers, and streams request records.
// Statistics retain at most one duration per request with a finite request cap.
func VerifyHTTP(ctx context.Context, o HTTPOptions) (map[string]any, error) {
	if o.Workers < 1 || o.Workers > 4 || o.Duration < 0 || o.Duration > 30*time.Minute {
		return nil, errors.New("workers 1..4, duration 0..1800s")
	}
	cases, e := LoadCases(o.Offline)
	if e != nil {
		return nil, e
	}
	if e = os.Mkdir(o.Out, 0700); e != nil {
		return nil, e
	}
	output, e := os.OpenFile(filepath.Join(o.Out, "requests.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return nil, e
	}
	defer output.Close()
	enc := json.NewEncoder(output)
	client := &http.Client{Timeout: 40 * time.Second}
	defer client.CloseIdleConnections()
	started := time.Now()
	var mu sync.Mutex
	var wg sync.WaitGroup
	seen := map[string]bool{}
	snapshots := map[string]bool{}
	times := []float64{}
	failures := []RequestResult{}
	var totalBytes int64
	passed, requests := 0, 0
	var writeError error
	workers := o.Workers
	if o.Duration == 0 {
		workers = min(workers, len(cases))
	}
	for worker := 0; worker < workers; worker++ {
		wg.Go(func() {
			for index := worker; ; index += workers {
				mu.Lock()
				stop := requests >= 100000 || writeError != nil
				mu.Unlock()
				if stop || ctx.Err() != nil {
					return
				}
				row := cases[index%len(cases)]
				begin := time.Now()
				result := RequestResult{Case: row.Case.Name, Worker: worker, Index: index, Started: begin.Sub(started).Seconds()}
				raw := []byte{}
				req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(o.URL, "/")+"/directions/v2:computeRoutes", bytes.NewReader(Body(row.Case)))
				if err == nil {
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("X-Goog-FieldMask", Mask)
					var response *http.Response
					response, err = client.Do(req)
					if err == nil {
						result.Status = response.StatusCode
						raw, err = readBounded(response.Body, ResponseLimit)
						response.Body.Close()
					}
				}
				result.Bytes = len(raw)
				result.SHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
				if err == nil {
					result.Snapshot, err = CheckResponse(row, result.Status, raw)
				}
				result.Passed = err == nil
				if err != nil {
					result.Error = err.Error()
				}
				result.Seconds = time.Since(begin).Seconds()
				mu.Lock()
				if !seen[row.Case.Name] {
					name := filepath.Join(o.Out, fmt.Sprintf("response-%03d.json", index%len(cases)))
					if e := os.WriteFile(name, raw, 0600); e != nil {
						writeError = e
					}
					seen[row.Case.Name] = true
				}
				if e := enc.Encode(result); e != nil {
					writeError = e
				}
				requests++
				times = append(times, result.Seconds)
				totalBytes += int64(result.Bytes)
				snapshots[result.Snapshot] = true
				if result.Passed {
					passed++
				} else if len(failures) < 1000 {
					failures = append(failures, result)
				}
				mu.Unlock()
				if o.Duration == 0 {
					if index+workers >= len(cases) {
						return
					}
				} else if time.Since(started) >= o.Duration {
					return
				}
			}
		})
	}
	wg.Wait()
	if e = output.Sync(); e != nil {
		return nil, e
	}
	if writeError != nil {
		return nil, writeError
	}
	if len(times) == 0 {
		return nil, ctx.Err()
	}
	sort.Float64s(times)
	percentile := func(q float64) float64 { return times[min(len(times)-1, int(math.Ceil(q*float64(len(times))))-1)] }
	ids := []string{}
	for id := range snapshots {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	summary := map[string]any{"seconds": time.Since(started).Seconds(), "workers": workers, "cases": len(cases), "requests": requests, "passed": passed, "failures": failures, "bytes": totalBytes, "p50_seconds": percentile(.5), "p95_seconds": percentile(.95), "max_seconds": times[len(times)-1], "snapshots": ids, "note": "Full JSON receive/parse and geometry equality; shared host OS cache, no physical-cold claim"}
	b, _ := json.MarshalIndent(summary, "", "  ")
	if e = os.WriteFile(filepath.Join(o.Out, "summary.json"), append(b, '\n'), 0600); e != nil {
		return summary, e
	}
	if e = ctx.Err(); e != nil {
		return summary, e
	}
	if passed != requests {
		return summary, errors.New("HTTP verification failures")
	}
	return summary, nil
}
