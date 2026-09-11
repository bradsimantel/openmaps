package api_test

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"openmaps/internal/api"
	"openmaps/internal/importer"
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
)

func geocodingHandler(t *testing.T, edits ...func(*importer.Bundle)) api.Handler {
	t.Helper()
	raw, err := os.ReadFile("../importer/testdata/small.json")
	if err != nil {
		t.Fatal(err)
	}
	var b importer.Bundle
	if err = json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	for _, edit := range edits {
		edit(&b)
	}
	path := filepath.Join(t.TempDir(), "lookup")
	if err = placeduckdb.Build(context.Background(), path, b); err != nil {
		t.Fatal(err)
	}
	s, err := placeduckdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return api.Handler{Geocoding: s}
}

func TestGeocodingAddressComponents(t *testing.T) {
	want := `[
		{"long_name":"26","short_name":"26","types":["street_number"]},
		{"long_name":"Marlborough Street","short_name":"Marlborough Street","types":["route"]},
		{"long_name":"Rhode Island","short_name":"RI","types":["administrative_area_level_1","political"]},
		{"long_name":"United States","short_name":"US","types":["country","political"]},
		{"long_name":"02840","short_name":"02840","types":["postal_code"]}]`
	var expected any
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	for _, conflict := range []bool{false, true} {
		h := geocodingHandler(t, func(b *importer.Bundle) {
			// Add Overture to the existing identity; only the winning address's
			// raw components may be used, even if another provider has no adapter.
			r := b.Records[1]
			r.Source = "overture:address"
			r.Priority = 200
			if conflict {
				r.Priority = 50
			}
			r.Raw = json.RawMessage(`{"properties":{"number":"26","street":"Marlborough Street","country":"US","postcode":"02840","address_levels":[{"value":"RI"},{"value":null}]}}`)
			b.Identities["overture:address:26"] = "fixture:address:26"
			b.Records = append(b.Records, r)
		})
		for _, query := range []string{"address=026+Marlborough+St", "address=26+Marlborough+Street,+Newport", "latlng=41.49,-71.31"} {
			w := call(h, "GET", "/maps/api/geocode/json?"+query, "", "")
			var body struct {
				Results []struct {
					ID         string `json:"place_id"`
					Components any    `json:"address_components"`
				}
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if len(body.Results) != 1 || body.Results[0].ID != importer.PublicID("fixture:address:26") {
				t.Fatal(w.Body.String())
			}
			want := expected
			if conflict {
				want = []any{}
			}
			if !reflect.DeepEqual(body.Results[0].Components, want) {
				t.Fatalf("conflict=%v: %s", conflict, w.Body.String())
			}
		}
	}
}
func TestGeocodingContract(t *testing.T) {
	h := geocodingHandler(t)
	for _, tc := range []struct{ query, status string }{
		{"address=26+Marlborough+St", "OK"}, {"address=99999+Marlborough+Street", "ZERO_RESULTS"},
		{"latlng=0,0", "ZERO_RESULTS"}, {"address=26+Marlborough+St&language=en-US&key=ignored", "OK"},
		{"", "INVALID_REQUEST"}, {"address=", "INVALID_REQUEST"}, {"latlng=NaN,0", "INVALID_REQUEST"}, {"latlng=91,0", "INVALID_REQUEST"}, {"latlng=41,-181", "INVALID_REQUEST"}, {"latlng=41", "INVALID_REQUEST"},
		{"address=x&latlng=0,0", "INVALID_REQUEST"}, {"address=x&address=y", "INVALID_REQUEST"},
		{"address=x&language=fr", "INVALID_REQUEST"}, {"address=x&language=", "INVALID_REQUEST"},
		{"address=26+Marlborough+St+Unit+2", "INVALID_REQUEST"}, {"address=x&region=us", "INVALID_REQUEST"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			w := call(h, "GET", "/maps/api/geocode/json?"+tc.query, "", "")
			var b map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
				t.Fatal(err)
			}
			if w.Code != 200 || b["status"] != tc.status || b["results"] == nil {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	for _, parameter := range []string{"bounds", "components", "result_type", "location_type", "place_id", "extra_computations", "fields", "$fields", "region", "sessionToken"} {
		w := call(h, "GET", "/maps/api/geocode/json?address=x&"+url.QueryEscape(parameter)+"=x", "", "")
		if !strings.Contains(w.Body.String(), "INVALID_REQUEST") {
			t.Fatal(w.Body.String())
		}
	}
	w := call(h, "GET", "/maps/api/geocode/json?address=26+Marlborough+St", "", "")
	var body struct {
		Results []struct {
			ID         string `json:"place_id"`
			Components []any  `json:"address_components"`
			Geometry   struct {
				Location struct{ Lat, Lng float64 }
				Type     string `json:"location_type"`
			}
			Openmaps struct{ Precision string }
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Results) != 1 {
		t.Fatal(w.Body.String())
	}
	r := body.Results[0]
	if r.ID != importer.PublicID("fixture:address:26") || r.Geometry.Type != "APPROXIMATE" || r.Openmaps.Precision != "source_address_point" || len(r.Components) != 0 || r.Geometry.Location.Lat == 0 {
		t.Fatal(w.Body.String())
	}
	for _, word := range []string{"ROOFTOP", "viewport", "bounds", "subpremise", "entrance"} {
		if strings.Contains(w.Body.String(), word) {
			t.Fatal("fabricated field", word)
		}
	}
	if w = call(h, "POST", "/maps/api/geocode/json?address=x", "", ""); w.Code != 405 {
		t.Fatal(w.Code)
	}
	for _, w := range []string{call(h, "GET", "/maps/api/geocode/json?address=x", "{}", "").Body.String(), call(h, "GET", "/maps/api/geocode/json?address=x", "", "*").Body.String()} {
		if !strings.Contains(w, "INVALID_REQUEST") {
			t.Fatal(w)
		}
	}
}
