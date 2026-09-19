package duckdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"openmaps/internal/api"
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
	handler := api.Handler{Places: store, Geocoding: store}
	for _, input := range []string{"White House", "Empire State Building", "Seattle", "1600 Pennsylvania Avenue Northwest"} {
		input := input
		measure("autocomplete", input, func() (places.Entity, error) {
			request := httptest.NewRequest(http.MethodPost, "/v1/places:autocomplete", strings.NewReader(`{"input":`+strconv.Quote(input)+`}`))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			var body struct {
				Suggestions []struct {
					Prediction struct {
						ID    string   `json:"placeId"`
						Types []string `json:"types"`
					} `json:"placePrediction"`
				} `json:"suggestions"`
			}
			if response.Code != http.StatusOK {
				return places.Entity{}, fmt.Errorf("HTTP %d: %s", response.Code, response.Body.String())
			}
			if queryErr := json.Unmarshal(response.Body.Bytes(), &body); queryErr != nil {
				return places.Entity{}, queryErr
			}
			if len(body.Suggestions) == 0 {
				return places.Entity{}, nil
			}
			kind := "area"
			if len(body.Suggestions[0].Prediction.Types) > 0 {
				switch body.Suggestions[0].Prediction.Types[0] {
				case "street_address":
					kind = "address"
				case "establishment":
					kind = "business"
				case "route":
					kind = "street"
				}
			}
			return places.Entity{ID: body.Suggestions[0].Prediction.ID, Kind: kind}, nil
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
