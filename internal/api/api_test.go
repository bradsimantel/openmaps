package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openmaps/internal/api"
	"openmaps/internal/importer"
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
)

func setup(t *testing.T) (api.Handler, *placeduckdb.Store) {
	t.Helper()
	raw, err := os.ReadFile("../importer/testdata/small.json")
	if err != nil {
		t.Fatal(err)
	}
	var b importer.Bundle
	if err = json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "lookup")
	if err = placeduckdb.Build(context.Background(), path, b); err != nil {
		t.Fatal(err)
	}
	s, err := placeduckdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return api.Handler{Places: s, Geocoding: s}, s
}
func call(h http.Handler, method, path, body, mask string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if mask != "" {
		r.Header.Set("X-Goog-FieldMask", mask)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestAutocompleteDetailsContract(t *testing.T) {
	h, _ := setup(t)
	for _, query := range []string{"White", "26 Marl", "Marlborough", "Newport"} {
		w := call(h, "POST", "/v1/places:autocomplete", `{"input":"`+query+`","languageCode":"en-US","sessionToken":"123e4567-e89b-42d3-a456-426614174000"}`, "")
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var body struct {
			Suggestions []struct {
				Prediction struct {
					Place string `json:"place"`
					ID    string `json:"placeId"`
					Text  struct {
						Text string `json:"text"`
					} `json:"text"`
					Types []string `json:"types"`
				} `json:"placePrediction"`
			} `json:"suggestions"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if len(body.Suggestions) == 0 || len(body.Suggestions) > 5 {
			t.Fatal(w.Body.String())
		}
		for _, s := range body.Suggestions {
			p := s.Prediction
			if p.Place != "places/"+p.ID || p.Text.Text == "" || len(p.Types) == 0 {
				t.Fatalf("prediction: %+v", p)
			}
			details := call(h, "GET", "/v1/"+p.Place, "", "id,name,displayName,location,types")
			if details.Code != 200 {
				t.Fatal(details.Body.String())
			}
			var d map[string]any
			if err := json.Unmarshal(details.Body.Bytes(), &d); err != nil {
				t.Fatal(err)
			}
			if len(d) != 5 || d["id"] != p.ID || d["name"] != p.Place {
				t.Fatal(d)
			}
			loc := d["location"].(map[string]any)
			if loc["latitude"].(float64) < 41 || loc["longitude"].(float64) > -71 {
				t.Fatal("coordinate order", loc)
			}
		}
	}
	w := call(h, "POST", "/v1/places:autocomplete", `{"input":"no_such_place"}`, "")
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"suggestions":[]}` {
		t.Fatal(w.Body.String())
	}
}
func TestFieldMasks(t *testing.T) {
	h, _ := setup(t)
	id := importer.PublicID("fixture:place:tavern")
	for _, tc := range []struct{ method, path, body, mask, want string }{
		{"GET", "/v1/places/" + id, "", "displayName.text", `{"displayName":{"text":"White Horse Tavern"}}`},
		{"GET", "/v1/places/" + id + "?fields=id", "", "", `{"id":"` + id + `"}`},
		{"GET", "/v1/places/" + id + "?%24fields=id", "", "", `{"id":"` + id + `"}`},
		{"GET", "/v1/places/" + id, "", "attributions.provider", `{"attributions":[{"provider":"Synthetic test data"}]}`},
		{"GET", "/v1/places/" + importer.PublicID("fixture:place:cafe"), "", "websiteUri", `{}`},
		{"POST", "/v1/places:autocomplete", `{"input":"White Horse"}`, "suggestions.placePrediction.placeId", `{"suggestions":[{"placePrediction":{"placeId":"` + id + `"}}]}`},
	} {
		w := call(h, tc.method, tc.path, tc.body, tc.mask)
		if w.Code != 200 || strings.TrimSpace(w.Body.String()) != tc.want {
			t.Fatalf("mask %s: %d %s", tc.mask, w.Code, w.Body.String())
		}
	}
}
func TestErrors(t *testing.T) {
	h, _ := setup(t)
	id := importer.PublicID("fixture:place:tavern")
	for _, tc := range []struct {
		name, method, path, body, mask string
		code                           int
		status                         string
	}{
		{"missing input", "POST", "/v1/places:autocomplete", `{}`, "", 400, "INVALID_ARGUMENT"},
		{"blank input", "POST", "/v1/places:autocomplete", `{"input":"  "}`, "", 400, "INVALID_ARGUMENT"},
		{"invalid JSON", "POST", "/v1/places:autocomplete", `{`, "", 400, "INVALID_ARGUMENT"},
		{"multiple JSON values", "POST", "/v1/places:autocomplete", `{"input":"White"} {}`, "", 400, "INVALID_ARGUMENT"},
		{"wrong field case", "POST", "/v1/places:autocomplete", `{"Input":"White"}`, "", 400, "INVALID_ARGUMENT"},
		{"unsupported bias", "POST", "/v1/places:autocomplete", `{"input":"White","locationBias":{}}`, "", 400, "INVALID_ARGUMENT"},
		{"unsupported field", "GET", "/v1/places/" + id, "", "rating", 400, "INVALID_ARGUMENT"},
		{"missing mask", "GET", "/v1/places/" + id, "", "", 400, "INVALID_ARGUMENT"},
		{"mask whitespace", "GET", "/v1/places/" + id, "", "id, name", 400, "INVALID_ARGUMENT"},
		{"two masks", "GET", "/v1/places/" + id + "?fields=id", "", "name", 400, "INVALID_ARGUMENT"},
		{"duplicate parameter", "GET", "/v1/places/" + id + "?fields=id&fields=name", "", "", 400, "INVALID_ARGUMENT"},
		{"unknown query", "GET", "/v1/places/" + id + "?regionCode=US", "", "id", 400, "INVALID_ARGUMENT"},
		{"unsupported language", "POST", "/v1/places:autocomplete", `{"input":"White","languageCode":"fr"}`, "", 400, "INVALID_ARGUMENT"},
		{"invalid token", "POST", "/v1/places:autocomplete", `{"input":"White","sessionToken":"bad+token"}`, "", 400, "INVALID_ARGUMENT"},
		{"body too large", "POST", "/v1/places:autocomplete", `{"input":"` + strings.Repeat("x", 17000) + `"}`, "", 400, "INVALID_ARGUMENT"},
		{"unicode too long", "POST", "/v1/places:autocomplete", `{"input":"` + strings.Repeat("é", 201) + `"}`, "", 400, "INVALID_ARGUMENT"},
		{"details body", "GET", "/v1/places/" + id, `{}`, "id", 400, "INVALID_ARGUMENT"},
		{"unknown ID", "GET", "/v1/places/absent", "", "id", 404, "NOT_FOUND"},
		{"unknown route", "GET", "/v1/geocode", "", "", 404, "NOT_FOUND"},
		{"wrong method", "GET", "/v1/places:autocomplete", "", "", 405, "INVALID_ARGUMENT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := call(h, tc.method, tc.path, tc.body, tc.mask)
			var got struct {
				Error struct {
					Code    int    `json:"code"`
					Status  string `json:"status"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if w.Code != tc.code || got.Error.Code != tc.code || got.Error.Status != tc.status || got.Error.Message == "" {
				t.Fatalf("got %d %s", w.Code, w.Body.String())
			}
			if tc.code == 405 && w.Header().Get("Allow") == "" {
				t.Fatal("missing Allow")
			}
		})
	}
}
func TestStorageErrorsDoNotLeak(t *testing.T) {
	h, s := setup(t)
	s.Close()
	for _, tc := range []struct{ method, path, body, mask string }{{"POST", "/v1/places:autocomplete", `{"input":"White"}`, ""}, {"GET", "/v1/places/any", "", "id"}} {
		w := call(h, tc.method, tc.path, tc.body, tc.mask)
		if w.Code != 500 || !strings.Contains(w.Body.String(), `"status":"INTERNAL"`) || strings.Contains(w.Body.String(), "sql:") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestRouteLookupResolutionErrors(t *testing.T) {
	h, _ := setup(t)
	path := "/directions/v2:computeRoutes"
	waypoints := func(origin, destination string) string {
		return `{"origin":` + origin + `,"destination":` + destination + `,"polylineEncoding":"GEO_JSON_LINESTRING"}`
	}
	coordinate := `{"location":{"latLng":{"latitude":41.49,"longitude":-71.31}}}`
	for _, tc := range []struct {
		name, origin, outcome string
		code                  int
	}{
		{"business place ID resolves", `{"placeId":"` + importer.PublicID("fixture:place:tavern") + `"}`, "unavailable", 503},
		{"standalone address ID resolves", `{"placeId":"` + importer.PublicID("fixture:address:26") + `"}`, "unavailable", 503},
		{"exact address resolves", `{"address":"26 Marlborough St"}`, "unavailable", 503},
		{"unknown place ID", `{"placeId":"om_missing"}`, "unknown_place_id", 400},
		{"ambiguous address", `{"address":"10 Shared Street"}`, "ambiguous_address", 400},
		{"unresolved address", `{"address":"99999 Marlborough Street"}`, "unresolved_address", 400},
		{"unsupported address syntax", `{"address":"26 Marlborough Street Apt 2"}`, "unsupported_address_syntax", 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := call(h, "POST", path, waypoints(tc.origin, coordinate), "routes.duration")
			if w.Code != tc.code || !strings.Contains(w.Body.String(), `"outcome":"`+tc.outcome+`"`) {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.outcome == "ambiguous_address" && !strings.Contains(w.Body.String(), `"candidate_count":2`) {
				t.Fatal(w.Body.String())
			}
		})
	}
	for _, origin := range []string{`{"placeId":"om_missing"}`, `{"address":"26 Marlborough Street"}`} {
		w := call(api.Handler{}, "POST", path, waypoints(origin, coordinate), "routes.duration")
		if w.Code != 503 || !strings.Contains(w.Body.String(), `"outcome":"lookup_unavailable"`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
