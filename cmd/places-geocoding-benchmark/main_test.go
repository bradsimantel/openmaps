package main

import (
	"context"
	"encoding/json"
	"math"
	"testing"

	"openmaps/internal/geocoding"
	"openmaps/internal/places"
)

type fixtureForwarder struct{}

func (fixtureForwarder) Forward(_ context.Context, input string) (geocoding.Response, error) {
	if input == "10 Main St, Boston, MA, 02108, US" {
		return geocoding.Response{Outcome: "matched", Results: []geocoding.Result{{Entity: places.Entity{ID: "fixture", Location: places.Location{Lat: 42.357, Lng: -71.063}}}}}, nil
	}
	return geocoding.Response{Outcome: "unsupported_input", Results: []geocoding.Result{}}, nil
}

func TestNormalizeNaN(t *testing.T) {
	raw := []byte(`{"streetAddress":"NaN Road","addressRegion":NaN,"latitude":42.1}`)
	var got struct {
		Street string   `json:"streetAddress"`
		Region *string  `json:"addressRegion"`
		Lat    *float64 `json:"latitude"`
	}
	if err := json.Unmarshal(normalizeNaN(raw), &got); err != nil {
		t.Fatal(err)
	}
	if got.Street != "NaN Road" || got.Region != nil || got.Lat == nil || *got.Lat != 42.1 {
		t.Fatalf("unexpected normalized record: %+v", got)
	}
}

func TestFinishInterpolatesPercentiles(t *testing.T) {
	mode := newMode()
	mode.distances = []float64{3, 1}
	finish(&mode)
	if mode.MedianMeters == nil || *mode.MedianMeters != 2 || mode.P95Meters == nil || math.Abs(*mode.P95Meters-2.9) > 1e-9 {
		t.Fatalf("unexpected percentiles: median=%v p95=%v", mode.MedianMeters, mode.P95Meters)
	}
}

func TestCanonicalInputAndCountry(t *testing.T) {
	value := address{Street: " 10 Main St ", Locality: "Boston", Region: "MA", Postcode: "02108"}
	if got := canonicalInput(value); got != "10 Main St, Boston, MA, 02108, US" {
		t.Fatalf("canonical input = %q", got)
	}
	for _, value := range []string{"US", "usa", "United States", "United-States", "United States (USA)", "United States of America"} {
		if !isUS(value) {
			t.Fatalf("US spelling rejected: %q", value)
		}
	}
	if isUS("Canada") {
		t.Fatal("non-US country accepted")
	}
}

func TestEvaluateOneSeparatesSurfaceGrammarFromMatch(t *testing.T) {
	mode := newMode()
	target := places.Location{Lat: 42.3571, Lng: -71.0631}
	evaluateOne(context.Background(), fixtureForwarder{}, &mode, "10 Main St Boston MA 02108", "10 Main St, Boston, MA, 02108, US", "drt", target)
	if mode.Queries != 1 || mode.Matched != 0 || mode.Outcomes["unsupported_input"] != 1 {
		t.Fatalf("unexpected verbatim result: %+v", mode)
	}
	mode = newMode()
	evaluateOne(context.Background(), fixtureForwarder{}, &mode, "10 Main St, Boston, MA, 02108, US", "10 Main St, Boston, MA, 02108, US", "drt", target)
	finish(&mode)
	if mode.Matched != 1 || mode.Within100Meters != 1 || len(mode.Returned) != 1 || mode.Returned[0].FirstID != "fixture" || mode.Returned[0].BestID != "fixture" || mode.MedianMeters == nil || math.IsNaN(*mode.MedianMeters) {
		t.Fatalf("unexpected canonical result: %+v", mode)
	}
}
