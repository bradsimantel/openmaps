package duckdb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"openmaps/internal/importer"
	"openmaps/internal/places"
)

// TestNationalServingBenchmark is an opt-in query benchmark over an already
// verified national generation. It deliberately skips the separate cold-start
// Verify pass so repeated query experiments do not spend minutes rehashing the
// same immutable files.
func TestNationalServingBenchmark(t *testing.T) {
	path := os.Getenv("OPENMAPS_NATIONAL_SERVING_BENCHMARK")
	reportPath := os.Getenv("OPENMAPS_NATIONAL_SERVING_REPORT")
	if path == "" && reportPath == "" {
		t.Skip("set OPENMAPS_NATIONAL_SERVING_BENCHMARK and OPENMAPS_NATIONAL_SERVING_REPORT")
	}
	if path == "" || reportPath == "" {
		t.Fatal("both national serving benchmark paths are required")
	}
	raw, err := os.ReadFile(filepath.Join(path, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	store, err := openVerified(path, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	type measurement struct {
		Operation  string  `json:"operation"`
		Input      string  `json:"input"`
		Iterations int     `json:"iterations"`
		MedianMS   float64 `json:"median_ms"`
		MaximumMS  float64 `json:"maximum_ms"`
		FirstID    string  `json:"first_id,omitempty"`
		FirstKind  string  `json:"first_kind,omitempty"`
	}
	report := struct {
		Generation   string        `json:"generation"`
		Measurements []measurement `json:"measurements"`
	}{Generation: path}
	measure := func(operation, input string, work func() (places.Entity, error)) {
		t.Helper()
		const iterations = 5
		times := make([]float64, 0, iterations)
		var first places.Entity
		for range iterations {
			started := time.Now()
			entity, workErr := work()
			if workErr != nil {
				t.Fatalf("%s %q: %v", operation, input, workErr)
			}
			if entity.ID == "" {
				t.Fatalf("%s %q returned no entity", operation, input)
			}
			first = entity
			times = append(times, float64(time.Since(started))/float64(time.Millisecond))
		}
		sort.Float64s(times)
		report.Measurements = append(report.Measurements, measurement{
			Operation: operation, Input: input, Iterations: iterations,
			MedianMS: times[len(times)/2], MaximumMS: times[len(times)-1],
			FirstID: first.ID, FirstKind: first.Kind,
		})
	}
	ctx := context.Background()
	for _, input := range []string{"White House", "Empire State Building", "Seattle", "1600 Pennsylvania Avenue Northwest"} {
		input := input
		measure("autocomplete", input, func() (places.Entity, error) {
			entities, queryErr := store.Autocomplete(ctx, input)
			if queryErr != nil || len(entities) == 0 {
				return places.Entity{}, queryErr
			}
			return entities[0], nil
		})
	}
	address := "1600 PENNSYLVANIA Avenue Northwest, DC, WASHINGTON, 20500, US"
	measure("forward", address, func() (places.Entity, error) {
		response, queryErr := store.Forward(ctx, address)
		if queryErr != nil || len(response.Results) == 0 {
			return places.Entity{}, queryErr
		}
		return response.Results[0].Entity, nil
	})
	measure("reverse", "38.89767510742324,-77.03654697024702", func() (places.Entity, error) {
		response, queryErr := store.Reverse(ctx, places.Location{Lat: 38.89767510742324, Lng: -77.03654697024702})
		if queryErr != nil || len(response.Results) == 0 {
			return places.Entity{}, queryErr
		}
		return response.Results[0].Entity, nil
	})
	if err = importer.WriteJSON(reportPath, report); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(report)
	t.Log(string(encoded))
}
