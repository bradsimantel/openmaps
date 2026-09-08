package geocoding_test

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"testing"

	"openmaps/internal/geocoding"
	"openmaps/internal/importer"
	"openmaps/internal/places"
)

func fixture(t *testing.T) *geocoding.Store {
	t.Helper()
	b := importer.Bundle{Schema: 1, Manifest: json.RawMessage(`{"bbox":[-71.33,41.47,-71.29,41.51]}`)}
	for _, item := range []struct {
		id, name string
		lat, lng float64
	}{
		{"a", "50 BELLEVUE Avenue", 41.49, -71.31}, {"b", "364 Bellevue Avenue", 41.48, -71.31}, {"c", "364 Bellevue Avenue", 41.48, -71.31},
		{"suffix", "12B École Road", 41.481, -71.311}, {"range", "1 -550 AMERICA Street", 41.49001, -71.31},
	} {
		values := map[string]any{"name": item.name, "address": item.name + ", RI, 02840, US", "location": places.Location{Lat: item.lat, Lng: item.lng}}
		r := importer.Record{Source: "fixture:address", SourceID: item.id, Release: "test", Kind: "address", Attributes: map[string]json.RawMessage{}, Paths: map[string]string{}, Raw: json.RawMessage(`{}`), Attributions: []places.Attribution{{Provider: "Fixture", URI: "https://example.org"}}}
		for k, v := range values {
			r.Attributes[k], _ = json.Marshal(v)
			r.Paths[k] = "/" + k
		}
		b.Records = append(b.Records, r)
	}
	path := filepath.Join(t.TempDir(), "data.sqlite")
	if err := importer.Build(context.Background(), path, b); err != nil {
		t.Fatal(err)
	}
	s, err := geocoding.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestForward(t *testing.T) {
	s := fixture(t)
	for _, tc := range []struct {
		input, outcome, id string
		count              int
		partial, invalid   bool
	}{
		{"050 bellevue AVE.", "matched", "a", 1, false, false},
		{"50 Bellevue Avenue, Newport, Rhode Island, 02840, USA", "matched", "a", 1, true, false},
		{"50 Bellevue Avenue, RI, 02840, US", "matched", "a", 1, false, false},
		{"0012b Ecole Rd", "matched", "suffix", 1, false, false},
		{"364 Bellevue Ave", "ambiguous", "", 2, false, false},
		{"50 Bellevue Avenue, Boston, MA", "no_match", "", 0, false, false},
		{"50 Bellevue Avenue, 99999", "no_match", "", 0, false, false},
		{"50 Bellevue Avenue, RI RI", "no_match", "", 0, false, false},
		{"999 Bellevue Avenue", "no_match", "", 0, false, false},
		{"50 Bellevue", "no_match", "", 0, false, false},
		{"50 Bellevue Avenue Apt 2", "unsupported_input", "", 0, false, true},
		{"1 -550 AMERICA Street", "unsupported_input", "", 0, false, true},
		{"Newport", "unsupported_input", "", 0, false, true},
		{"Bellevue Avenue", "unsupported_input", "", 0, false, true},
		{"", "invalid_input", "", 0, false, true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			got, err := s.Forward(context.Background(), tc.input)
			if (err != nil) != tc.invalid || got.Outcome != tc.outcome || len(got.Results) != tc.count {
				t.Fatalf("%+v: %v", got, err)
			}
			if tc.id != "" {
				r := got.Results[0]
				if r.Entity.ID != importer.PublicID("fixture:address:"+tc.id) || r.Partial != tc.partial || r.DistanceMeters != nil {
					t.Fatalf("%+v", r)
				}
			}
			if tc.count > 1 && got.Results[0].Entity.ID >= got.Results[1].Entity.ID {
				t.Fatal("unstable identity order")
			}
		})
	}
}
func TestReverseDistanceCoverageAndTies(t *testing.T) {
	s := fixture(t)
	origin := places.Location{Lat: 41.49, Lng: -71.31}
	for _, tc := range []struct {
		meters  float64
		count   int
		outcome string
	}{{0, 1, "matched"}, {99.99, 1, "matched"}, {100.01, 0, "no_nearby_address"}} {
		p := origin
		p.Lat += tc.meters / 6371008.8 * 180 / math.Pi
		got, err := s.Reverse(context.Background(), p)
		if err != nil || got.Outcome != tc.outcome || len(got.Results) != tc.count {
			t.Fatalf("%+v: %+v %v", tc, got, err)
		}
		if tc.count > 0 && (got.Results[0].Entity.ID != importer.PublicID("fixture:address:a") || math.Abs(*got.Results[0].DistanceMeters-tc.meters) > 0.00001) {
			t.Fatalf("incorrect nearest or distance: %+v", got)
		}
	}
	got, err := s.Reverse(context.Background(), places.Location{Lat: 41.48, Lng: -71.31})
	if err != nil || got.Outcome != "ambiguous" || len(got.Results) != 2 || got.Results[0].Entity.ID >= got.Results[1].Entity.ID {
		t.Fatalf("%+v %v", got, err)
	}
	for _, p := range []places.Location{{Lat: 41.469999, Lng: -71.31}, {Lat: 0, Lng: 0}, {Lat: 41.49, Lng: 179.999}} {
		got, err = s.Reverse(context.Background(), p)
		if err != nil || got.Outcome != "outside_coverage" || len(got.Results) != 0 {
			t.Fatalf("%+v %v", got, err)
		}
	}
	for _, p := range []places.Location{{Lat: 91}, {Lng: 181}, {Lat: math.NaN()}, {Lng: math.Inf(1)}} {
		if _, err = s.Reverse(context.Background(), p); err == nil {
			t.Fatal("invalid coordinates accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.Reverse(ctx, origin); err == nil {
		t.Fatal("cancellation ignored")
	}
	if d := geocoding.DistanceMeters(places.Location{Lng: 179.999}, places.Location{Lng: -179.999}); math.Abs(d-222.39016) > 0.01 {
		t.Fatal("dateline distance", d)
	}
}
