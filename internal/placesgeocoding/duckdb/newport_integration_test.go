//go:build integration

package duckdb_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"openmaps/internal/api"
	"openmaps/internal/geocoding"
	"openmaps/internal/importer"
	"openmaps/internal/places"
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
)

func TestNewportParity(t *testing.T) {
	baselinePath := os.Getenv("OPENMAPS_BASELINE")
	bundlePath := os.Getenv("OPENMAPS_BUNDLE")
	if baselinePath == "" || bundlePath == "" {
		t.Fatal("set absolute OPENMAPS_BASELINE and OPENMAPS_BUNDLE")
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
	legacyPlaces, err := places.Open(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	defer legacyPlaces.Close()
	legacyGeocoding, err := geocoding.Open(context.Background(), baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	legacyHTTP := api.Handler{Places: legacyPlaces, Geocoding: legacyGeocoding}
	candidateHTTP := api.Handler{Places: candidate, Geocoding: candidate}
	compareHTTP := func(method, target, body, mask string) {
		t.Helper()
		want := request(legacyHTTP, method, target, body, mask)
		got := request(candidateHTTP, method, target, body, mask)
		for _, header := range []string{"Content-Type", "Allow"} {
			if got.Header().Get(header) != want.Header().Get(header) {
				t.Fatalf("%s %s header %s differs: DuckDB=%q SQLite=%q", method, target, header, got.Header().Get(header), want.Header().Get(header))
			}
		}
		if got.Code != want.Code || got.Body.String() != want.Body.String() {
			t.Fatalf("%s %s differs\nDuckDB: %d %s\nSQLite: %d %s", method, target, got.Code, got.Body.String(), want.Code, want.Body.String())
		}
	}
	for _, check := range importer.NewportPlacesQueryChecks() {
		body, _ := json.Marshal(map[string]string{"input": check.Input})
		compareHTTP(http.MethodPost, "/v1/places:autocomplete", string(body), "")
		want, queryErr := legacyPlaces.Autocomplete(ctx, check.Input)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		got, queryErr := candidate.Autocomplete(ctx, check.Input)
		if queryErr != nil || !reflect.DeepEqual(got, servingProjection(want)) {
			t.Fatalf("autocomplete %q differs: %v\nDuckDB: %+v\nlegacy: %+v", check.Input, queryErr, got, want)
		}
		for _, entity := range want {
			compareHTTP(http.MethodGet, "/v1/places/"+entity.ID, "", "*")
			detail, detailErr := candidate.Details(ctx, entity.ID)
			if detailErr != nil || !reflect.DeepEqual(detail, entity) {
				t.Fatalf("details %s differs: %v\nDuckDB: %+v\nlegacy: %+v", entity.ID, detailErr, detail, entity)
			}
		}
	}
	var suite struct {
		Cases []struct {
			Address string
			LatLng  []float64
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
		if test.Address != "" {
			compareHTTP(http.MethodGet, "/maps/api/geocode/json?address="+url.QueryEscape(test.Address), "", "")
			want, wantErr := legacyGeocoding.Forward(ctx, test.Address)
			got, gotErr := candidate.Forward(ctx, test.Address)
			if errorString(gotErr) != errorString(wantErr) || !reflect.DeepEqual(got, want) {
				t.Fatalf("forward %q differs\nDuckDB: %+v %v\nlegacy: %+v %v", test.Address, got, gotErr, want, wantErr)
			}
		}
		if len(test.LatLng) == 2 {
			compareHTTP(http.MethodGet, fmt.Sprintf("/maps/api/geocode/json?latlng=%g%%2C%g", test.LatLng[0], test.LatLng[1]), "", "")
			point := places.Location{Lat: test.LatLng[0], Lng: test.LatLng[1]}
			want, wantErr := legacyGeocoding.Reverse(ctx, point)
			got, gotErr := candidate.Reverse(ctx, point)
			if errorString(gotErr) != errorString(wantErr) || !reflect.DeepEqual(got, want) {
				t.Fatalf("reverse %v differs\nDuckDB: %+v %v\nlegacy: %+v %v", point, got, gotErr, want, wantErr)
			}
		}
	}
	compareHTTP(http.MethodPost, "/v1/places:autocomplete", `{"input":"White","locationBias":{"circle":{"center":{"latitude":41.49,"longitude":-71.31},"radius":100}}}`, "")
	compareHTTP(http.MethodGet, "/maps/api/geocode/json?address=50+Bellevue+Avenue&units=imperial", "", "")
	compareHTTP(http.MethodGet, "/v1/places/om_missing", "", "*")
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
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
