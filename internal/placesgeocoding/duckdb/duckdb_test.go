package duckdb_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	_ "github.com/duckdb/duckdb-go/v2"

	"openmaps/internal/api"
	"openmaps/internal/geocoding"
	"openmaps/internal/importer"
	"openmaps/internal/places"
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
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

func stores(t *testing.T) (*places.Store, *geocoding.Store, *placeduckdb.Store, string) {
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
	candidatePath := filepath.Join(dir, "duckdb")
	if err = placeduckdb.Build(context.Background(), candidatePath, bundle); err != nil {
		t.Fatal(err)
	}
	candidate, err := placeduckdb.Open(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		legacyPlaces.Close()
		candidate.Close()
	})
	return legacyPlaces, legacyGeocoding, candidate, candidatePath
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
		{"POST", "/v1/places:autocomplete", `{"input":"W"}`, ""},
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
	for _, test := range cases {
		t.Run(test.target+test.body, func(t *testing.T) {
			want := request(legacy, test.method, test.target, test.body, test.mask)
			got := request(proof, test.method, test.target, test.body, test.mask)
			if got.Code != want.Code || got.Body.String() != want.Body.String() {
				t.Fatalf("DuckDB: %d %s\nlegacy: %d %s", got.Code, got.Body.String(), want.Code, want.Body.String())
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
	queries := []string{"W", "White", "26 Marl", "Marlborough", "Newport"}
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
					errors <- fmt.Errorf("autocomplete %q: %w", query, err)
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

func servingProjection(in []places.Entity) []places.Entity {
	out := make([]places.Entity, len(in))
	for i, entity := range in {
		out[i] = places.Entity{ID: entity.ID, Kind: entity.Kind, Name: entity.Name, Address: entity.Address, Subtype: entity.Subtype}
	}
	return out
}

func TestArtifactDeterminismIsolationAndCorruption(t *testing.T) {
	bundle := fixture(t)
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	if err := placeduckdb.Build(context.Background(), a, bundle); err != nil {
		t.Fatal(err)
	}
	slices.Reverse(bundle.Records)
	slices.Reverse(bundle.Relationships)
	if err := placeduckdb.Build(context.Background(), b, bundle); err != nil {
		t.Fatal(err)
	}
	ma, err := placeduckdb.Verify(a)
	if err != nil {
		t.Fatal(err)
	}
	mb, err := placeduckdb.Verify(b)
	if err != nil {
		t.Fatal(err)
	}
	if ma.Schema != mb.Schema || ma.CoordinateOrder != mb.CoordinateOrder || len(ma.Files) != len(mb.Files) {
		t.Fatalf("incompatible manifests:\n%+v\n%+v", ma, mb)
	}
	for i := range ma.Files {
		if ma.Files[i].Name != mb.Files[i].Name || ma.Files[i].Rows != mb.Files[i].Rows ||
			ma.Files[i].Name != placeduckdb.IndexName && ma.Files[i].SHA256 != mb.Files[i].SHA256 {
			t.Fatalf("non-deterministic normalized file:\n%+v\n%+v", ma.Files[i], mb.Files[i])
		}
	}
	if err = placeduckdb.Build(context.Background(), a, bundle); err == nil {
		t.Fatal("overwrote an artifact")
	}
	db, err := sql.Open("duckdb", filepath.Join(a, placeduckdb.IndexName)+"?access_mode=read_only")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec("ATTACH '" + strings.ReplaceAll(filepath.Join(b, placeduckdb.IndexName), "'", "''") + "' AS comparison (READ_ONLY)"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"metadata", "entity_locator", "search_entities", "names", "tokens", "postings", "short_prefix_head", "address_lookup", "address_spatial", "corpus_stats"} {
		var differences int
		query := fmt.Sprintf(`SELECT count(*) FROM (
(SELECT * FROM main.%[1]s EXCEPT SELECT * FROM comparison.%[1]s)
UNION ALL
(SELECT * FROM comparison.%[1]s EXCEPT SELECT * FROM main.%[1]s))`, table)
		if err = db.QueryRow(query).Scan(&differences); err != nil || differences != 0 {
			t.Fatalf("non-deterministic DuckDB table %s: differences=%d: %v", table, differences, err)
		}
	}
	rows, err := db.Query("SELECT table_name FROM information_schema.tables WHERE table_schema='main' ORDER BY table_name")
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
			t.Fatalf("complete base table leaked into DuckDB: %s", name)
		}
	}
	if err = os.WriteFile(filepath.Join(a, placeduckdb.EntitiesName), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = placeduckdb.Open(a); err == nil {
		t.Fatal("opened corrupt Parquet generation")
	}
}

func TestSelectionReplacementRollbackAndConcurrentLeases(t *testing.T) {
	bundle := fixture(t)
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	if err := placeduckdb.Build(context.Background(), first, bundle); err != nil {
		t.Fatal(err)
	}
	withoutTavern := bundle
	withoutTavern.Records = slices.DeleteFunc(withoutTavern.Records, func(record importer.Record) bool {
		return record.Key() == "fixture:place:tavern"
	})
	withoutTavern.Relationships = slices.DeleteFunc(withoutTavern.Relationships, func(relationship importer.Relationship) bool {
		return relationship.From == "fixture:place:tavern" || relationship.To == "fixture:place:tavern"
	})
	if err := placeduckdb.Build(context.Background(), second, withoutTavern); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(root, "selection.json")
	if err := placeduckdb.InitializeSelection(state, first); err != nil {
		t.Fatal(err)
	}
	handler, err := placeduckdb.OpenLiveHandler(state)
	if err != nil {
		t.Fatal(err)
	}
	defer handler.Close()
	id := importer.PublicID("fixture:place:tavern")
	assertStatus := func(want int) {
		t.Helper()
		got := request(handler, "GET", "/v1/places/"+id, "", "*")
		if got.Code != want {
			t.Fatalf("status=%d body=%s want=%d", got.Code, got.Body.String(), want)
		}
	}
	assertStatus(http.StatusOK)

	statuses := make(chan int, 100)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				statuses <- request(handler, "GET", "/v1/places/"+id, "", "*").Code
			}
		}()
	}
	if err = placeduckdb.ActivateSelection(state, second); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusOK && status != http.StatusNotFound {
			t.Fatalf("request observed partial replacement status %d", status)
		}
	}
	assertStatus(http.StatusNotFound)
	secondReference, err := placeduckdb.Describe(second)
	if err != nil {
		t.Fatal(err)
	}
	if handler.Current() != secondReference {
		t.Fatalf("handler did not select second generation: %+v", handler.Current())
	}
	if err = placeduckdb.RollbackSelection(state); err != nil {
		t.Fatal(err)
	}
	assertStatus(http.StatusOK)
	firstReference, err := placeduckdb.Describe(first)
	if err != nil {
		t.Fatal(err)
	}
	if handler.Current() != firstReference {
		t.Fatalf("handler did not roll back: %+v", handler.Current())
	}

	corrupt := filepath.Join(root, "corrupt")
	if err = placeduckdb.Build(context.Background(), corrupt, fixture(t)); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(corrupt, placeduckdb.EntitiesName), []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = placeduckdb.ActivateSelection(state, corrupt); err == nil {
		t.Fatal("activated corrupt generation")
	}
	assertStatus(http.StatusOK)
}
