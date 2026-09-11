//go:build integration

package duckdb_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"openmaps/internal/api"
	"openmaps/internal/importer"
	"openmaps/internal/places"
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
)

func TestNewportGolden(t *testing.T) {
	bundlePath := os.Getenv("OPENMAPS_BUNDLE")
	if bundlePath == "" {
		t.Fatal("set absolute OPENMAPS_BUNDLE")
	}
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	var bundle importer.Bundle
	if err = json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(t.TempDir(), "newport")
	if err = placeduckdb.Build(context.Background(), artifact, bundle); err != nil {
		t.Fatal(err)
	}
	candidate, err := placeduckdb.Open(artifact)
	if err != nil {
		t.Fatal(err)
	}
	defer candidate.Close()
	ctx := context.Background()
	for _, check := range importer.NewportPlacesQueryChecks() {
		got, queryErr := candidate.Autocomplete(ctx, check.Input)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		if check.Empty && len(got) != 0 || !check.Empty && (len(got) == 0 || check.FirstID != "" && got[0].ID != check.FirstID || check.FirstKind != "" && got[0].Kind != check.FirstKind) {
			t.Fatalf("autocomplete %q failed golden expectation: %+v", check.Input, got)
		}
		for _, entity := range got {
			detail, detailErr := candidate.Details(ctx, entity.ID)
			projection := places.Entity{ID: detail.ID, Kind: detail.Kind, Name: detail.Name, Address: detail.Address, Subtype: detail.Subtype}
			if detailErr != nil || !reflect.DeepEqual(projection, entity) || len(detail.Attributions) == 0 {
				t.Fatalf("details %s mismatch: %+v %v", entity.ID, detail, detailErr)
			}
		}
	}
	var suite struct {
		Cases []struct {
			Name, Address, Outcome string
			LatLng                 []float64
			IDs                    []string
			Distances              []float64
			Partial                bool
		}
		Evidence []struct {
			ID             string
			SourceKey      string `json:"source_key"`
			Number, Street string
			Location       places.Location
		}
	}
	raw, err = os.ReadFile(filepath.Join("..", "..", "geocoding", "testdata", "newport.json"))
	if err != nil {
		t.Fatal("read geocoding suite", err)
	}
	if err = json.Unmarshal(raw, &suite); err != nil {
		t.Fatal("decode geocoding suite", err)
	}
	for _, test := range suite.Cases {
		var gotErr error
		var gotIDs []string
		var gotOutcome string
		var partial bool
		var distances []float64
		if test.Address != "" {
			response, err := candidate.Forward(ctx, test.Address)
			gotErr, gotOutcome = err, response.Outcome
			for _, result := range response.Results {
				gotIDs = append(gotIDs, result.Entity.ID)
				partial = result.Partial
			}
		} else {
			response, err := candidate.Reverse(ctx, places.Location{Lat: test.LatLng[0], Lng: test.LatLng[1]})
			gotErr, gotOutcome = err, response.Outcome
			for _, result := range response.Results {
				gotIDs = append(gotIDs, result.Entity.ID)
				distances = append(distances, *result.DistanceMeters)
			}
		}
		wantError := test.Outcome == "invalid_input" || test.Outcome == "unsupported_input"
		if (gotErr != nil) != wantError || gotOutcome != test.Outcome || !slices.Equal(gotIDs, test.IDs) || partial != test.Partial {
			t.Fatalf("%s: outcome=%s ids=%v partial=%v err=%v", test.Name, gotOutcome, gotIDs, partial, gotErr)
		}
		for i := range distances {
			if math.Abs(distances[i]-test.Distances[i]) > 0.00001 {
				t.Fatalf("%s distance[%d]=%.6f want %.6f", test.Name, i, distances[i], test.Distances[i])
			}
		}
	}
	for _, expected := range suite.Evidence {
		evidence, evidenceErr := candidate.Evidence(ctx, expected.ID)
		if evidenceErr != nil || evidence.Entity.Location != expected.Location {
			t.Fatalf("evidence %s: %+v %v", expected.ID, evidence, evidenceErr)
		}
		found := false
		for _, source := range evidence.Sources {
			if source.SourceKey == expected.SourceKey {
				var feature struct {
					Properties struct{ Number, Street string }
					Geometry   struct{ Coordinates []float64 }
				}
				if json.Unmarshal(source.Raw, &feature) != nil || len(feature.Geometry.Coordinates) != 2 {
					t.Fatalf("invalid original source evidence for %s", expected.ID)
				}
				found = feature.Properties.Number == expected.Number && feature.Properties.Street == expected.Street &&
					feature.Geometry.Coordinates[0] == expected.Location.Lng && feature.Geometry.Coordinates[1] == expected.Location.Lat
			}
		}
		if !found || len(evidence.Attributes) == 0 {
			t.Fatalf("incomplete original evidence for %s", expected.ID)
		}
	}
	handler := api.Handler{Places: candidate, Geocoding: candidate}
	for _, test := range []struct {
		method, target, body, mask string
		status                     int
		contains                   string
	}{
		{http.MethodPost, "/v1/places:autocomplete", `{"input":"White","locationBias":{}}`, "", 400, "INVALID_ARGUMENT"},
		{http.MethodGet, "/maps/api/geocode/json?address=50+Bellevue+Avenue&units=imperial", "", "", 200, "INVALID_REQUEST"},
		{http.MethodGet, "/v1/places/om_missing", "", "*", 404, "NOT_FOUND"},
	} {
		response := request(handler, test.method, test.target, test.body, test.mask)
		if response.Code != test.status || !strings.Contains(response.Header().Get("Content-Type"), "application/json") || !strings.Contains(response.Body.String(), test.contains) {
			t.Fatalf("%s: status=%d body=%s", test.target, response.Code, response.Body.String())
		}
	}
}

type latency struct{ p50, p95, p99 time.Duration }

func measure(iterations, workers int, operation func(int) error) (latency, error) {
	durations := make([]time.Duration, iterations)
	jobs := make(chan int)
	var wg sync.WaitGroup
	var firstErr error
	var errorMu sync.Mutex
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				start := time.Now()
				err := operation(i)
				durations[i] = time.Since(start)
				if err != nil {
					errorMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errorMu.Unlock()
				}
			}
		}()
	}
	for i := range iterations {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return latency{}, firstErr
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	percentile := func(p float64) time.Duration {
		index := int(math.Ceil(float64(len(durations))*p)) - 1
		return durations[max(0, min(index, len(durations)-1))]
	}
	return latency{percentile(.50), percentile(.95), percentile(.99)}, nil
}

func TestNewportMeasurements(t *testing.T) {
	bundlePath := os.Getenv("OPENMAPS_BUNDLE")
	if bundlePath == "" {
		t.Fatal("set absolute OPENMAPS_BUNDLE")
	}
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	var bundle importer.Bundle
	if err = json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(t.TempDir(), "newport")
	if err = placeduckdb.Build(context.Background(), artifact, bundle); err != nil {
		t.Fatal(err)
	}
	store, err := placeduckdb.Open(artifact)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx := context.Background()
	queries := []string{"m", "ma", "white horse", "main street", "newport", "50 bellevue", "zzxnoresult"}
	ids := []string{
		"om_a5e3dc7692e4d3b90b71b94fba66ec5b",
		"om_f88c096070879478ee02e036d3e67480",
		"om_0953764bc3686bc97c659471619e9dfa",
		"om_6393fc0fc62fcba159e53e572c6cbbbd",
	}
	addresses := []string{"50 Bellevue Avenue", "364 Bellevue Avenue", "999 Bellevue Avenue"}
	points := []places.Location{{Lat: 41.4904, Lng: -71.3102}, {Lat: 41.48, Lng: -71.31}, {Lat: 41.5000, Lng: -71.3000}}
	operations := []struct {
		name string
		run  func(int) error
	}{
		{"autocomplete", func(i int) error { _, err := store.Autocomplete(ctx, queries[i%len(queries)]); return err }},
		{"details", func(i int) error { _, err := store.Details(ctx, ids[i%len(ids)]); return err }},
		{"forward", func(i int) error { _, err := store.Forward(ctx, addresses[i%len(addresses)]); return err }},
		{"reverse", func(i int) error { _, err := store.Reverse(ctx, points[i%len(points)]); return err }},
		{"provenance", func(i int) error { _, err := store.Evidence(ctx, ids[i%len(ids)]); return err }},
	}
	for _, operation := range operations {
		result, measureErr := measure(100, 1, operation.run)
		if measureErr != nil {
			t.Fatal(operation.name, measureErr)
		}
		t.Logf("%-12s p50=%s p95=%s p99=%s", operation.name, result.p50, result.p95, result.p99)
	}
	mixed, err := measure(100, 4, func(i int) error {
		operation := operations[i%len(operations)]
		return operation.run(i / len(operations))
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%-12s p50=%s p95=%s p99=%s", "mixed-4", mixed.p50, mixed.p95, mixed.p99)
	for i := range 5 {
		start := time.Now()
		opened, openErr := placeduckdb.Open(artifact)
		if openErr != nil {
			t.Fatal(openErr)
		}
		if _, openErr = opened.Details(ctx, ids[i%len(ids)]); openErr != nil {
			t.Fatal(openErr)
		}
		t.Log(fmt.Sprintf("open+first-%d %s", i+1, time.Since(start)))
		opened.Close()
	}
}
