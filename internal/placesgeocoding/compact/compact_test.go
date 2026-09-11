package compact_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"openmaps/internal/api"
	"openmaps/internal/geocoding"
	"openmaps/internal/importer"
	"openmaps/internal/places"
	"openmaps/internal/placesgeocoding/compact"
)

func fixture(t *testing.T) importer.Bundle {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "importer", "testdata", "small.json"))
	if err != nil {
		t.Fatal(err)
	}
	var bundle importer.Bundle
	if err = json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	return bundle
}

func stores(t *testing.T) (*places.Store, *geocoding.Store, *compact.Store, string) {
	t.Helper()
	bundle := fixture(t)
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "legacy.sqlite")
	if err := importer.Build(context.Background(), legacyPath, bundle); err != nil {
		t.Fatal(err)
	}
	legacyPlaces, err := places.Open(legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	legacyGeocoding, err := geocoding.Open(context.Background(), legacyPath)
	if err != nil {
		t.Fatal(err)
	}
	compactPath := filepath.Join(dir, "compact")
	if err = compact.Build(context.Background(), compactPath, bundle); err != nil {
		t.Fatal(err)
	}
	compactStore, err := compact.Open(compactPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		legacyPlaces.Close()
		compactStore.Close()
	})
	return legacyPlaces, legacyGeocoding, compactStore, compactPath
}

func request(handler http.Handler, method, target, body, mask string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if mask != "" {
		r.Header.Set("X-Goog-FieldMask", mask)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestAPIParityAndParquetEvidence(t *testing.T) {
	legacyPlaces, legacyGeocoding, candidate, _ := stores(t)
	legacy := api.Handler{Places: legacyPlaces, Geocoding: legacyGeocoding}
	proof := api.Handler{Places: candidate, Geocoding: candidate}
	id := importer.PublicID("fixture:place:tavern")
	cases := []struct {
		method, target, body, mask string
	}{
		{"POST", "/v1/places:autocomplete", `{"input":"White"}`, ""},
		{"POST", "/v1/places:autocomplete", `{"input":"26 Marl"}`, ""},
		{"POST", "/v1/places:autocomplete", `{"input":"Marlborough"}`, ""},
		{"POST", "/v1/places:autocomplete", `{"input":"Newport"}`, ""},
		{"GET", "/v1/places/" + id, "", "*"},
		{"GET", "/maps/api/geocode/json?address=26%20Marlborough%20Street", "", ""},
		{"GET", "/maps/api/geocode/json?address=26%20Marlborough%20Street%2C%20Boston", "", ""},
		{"GET", "/maps/api/geocode/json?address=26%20Marlborough%20Street%20Apt%202", "", ""},
		{"GET", "/maps/api/geocode/json?latlng=41.488%2C-71.312", "", ""},
		{"GET", "/maps/api/geocode/json?latlng=0%2C0", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.target+tc.body, func(t *testing.T) {
			want := request(legacy, tc.method, tc.target, tc.body, tc.mask)
			got := request(proof, tc.method, tc.target, tc.body, tc.mask)
			if got.Code != want.Code || got.Body.String() != want.Body.String() {
				t.Fatalf("candidate: %d %s\nlegacy: %d %s", got.Code, got.Body.String(), want.Code, want.Body.String())
			}
		})
	}
	evidence, err := candidate.Evidence(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if evidence.Entity.ID != id || len(evidence.Sources) != 1 || len(evidence.Attributes) == 0 || json.Unmarshal(evidence.Sources[0].Raw, &raw) != nil || raw["fixture"] != true {
		t.Fatalf("incomplete Parquet evidence: %+v", evidence)
	}
}

func TestConcurrentReadParity(t *testing.T) {
	legacyPlaces, _, candidate, _ := stores(t)
	queries := []string{"White", "26 Marl", "Marlborough", "Newport"}
	var wg sync.WaitGroup
	errors := make(chan error, 4)
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				query := queries[(worker+i)%len(queries)]
				want, err := legacyPlaces.Autocomplete(context.Background(), query)
				if err != nil {
					errors <- err
					return
				}
				got, err := candidate.Autocomplete(context.Background(), query)
				if err != nil || !reflect.DeepEqual(got, servingProjection(want)) {
					errors <- &parityError{query: query, err: err}
					return
				}
				if len(got) > 0 {
					if _, err = candidate.Details(context.Background(), got[0].ID); err != nil {
						errors <- err
						return
					}
				}
				if _, err = candidate.Forward(context.Background(), "10 Shared Street"); err != nil {
					errors <- err
					return
				}
				if _, err = candidate.Reverse(context.Background(), places.Location{Lat: 41.488, Lng: -71.312}); err != nil {
					errors <- err
					return
				}
			}
		}(worker)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
}

type parityError struct {
	query string
	err   error
}

func (e *parityError) Error() string {
	return "autocomplete parity failed for " + e.query + ": " + errorText(e.err)
}
func errorText(err error) string {
	if err == nil {
		return "different results"
	}
	return err.Error()
}

func servingProjection(in []places.Entity) []places.Entity {
	out := make([]places.Entity, len(in))
	for i, entity := range in {
		out[i] = places.Entity{ID: entity.ID, Kind: entity.Kind, Name: entity.Name, Address: entity.Address, Subtype: entity.Subtype}
	}
	return out
}

func TestArtifactIsDeterministicCompactAndVerified(t *testing.T) {
	bundle := fixture(t)
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	if err := compact.Build(context.Background(), a, bundle); err != nil {
		t.Fatal(err)
	}
	slices.Reverse(bundle.Records)
	slices.Reverse(bundle.Relationships)
	if err := compact.Build(context.Background(), b, bundle); err != nil {
		t.Fatal(err)
	}
	ma, err := compact.Verify(a)
	if err != nil {
		t.Fatal(err)
	}
	mb, err := compact.Verify(b)
	if err != nil || !reflect.DeepEqual(ma, mb) {
		t.Fatalf("non-deterministic manifests: %v", err)
	}
	if err = compact.Build(context.Background(), a, bundle); err == nil {
		t.Fatal("overwrote an artifact")
	}
	db, err := sql.Open("sqlite", filepath.Join(a, compact.IndexName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT name FROM sqlite_schema WHERE type='table' ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name == "source_records" || name == "attribute_provenance" || name == "relationships" {
			t.Fatalf("full base table leaked into compact SQLite: %s", name)
		}
	}
	if err = os.WriteFile(filepath.Join(a, compact.EntitiesName), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = compact.Open(a); err == nil {
		t.Fatal("opened a corrupt Parquet generation")
	}
}
