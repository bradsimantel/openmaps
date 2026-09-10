//go:build integration

package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"openmaps/internal/places"
)

// TestPinnedNewportLookup rebuilds the checked-in pin from downloaded inputs.
// It verifies source identities/provenance, SQLite integrity, representative
// lookup behavior, aliases, repeated segment identities, and deterministic IDs.
func TestPinnedNewportLookup(t *testing.T) {
	dataDir := os.Getenv("OPENMAPS_DATA")
	if dataDir == "" {
		t.Fatal("set OPENMAPS_DATA to the prepared Newport source directory")
	}
	root := filepath.Join("..", "..")
	manifest, err := ReadManifest(filepath.Join(root, "imports/newport.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "imports/identities.json"))
	if err != nil {
		t.Fatal(err)
	}
	identities := map[string]string{}
	if err = json.Unmarshal(raw, &identities); err != nil {
		t.Fatal(err)
	}
	bundle, audit, err := Prepare(context.Background(), manifest, dataDir, identities)
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, _, err := Prepare(context.Background(), manifest, dataDir, identities)
	if err != nil || !reflect.DeepEqual(bundle, rebuilt) {
		t.Fatal("regional preparation is not deterministic", err)
	}
	selection, ok := audit["transportation_selection"].(transportationSelection)
	if !ok || selection.NamedRoads == 0 || selection.UnnamedRoads == 0 || selection.NonRoads == 0 {
		t.Fatalf("incomplete transportation selection audit: %#v", audit["transportation_selection"])
	}

	database := filepath.Join(t.TempDir(), "newport.sqlite")
	if err = Build(context.Background(), database, bundle); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadSnapshot(context.Background(), database)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, entity := range snapshot.Entities {
		counts[entity.Kind]++
	}
	wantCounts := map[string]int{"business": 2173, "address": 8545, "area": 4, "street": 1621}
	if !reflect.DeepEqual(counts, wantCounts) {
		t.Fatalf("unexpected pinned counts: got %v want %v", counts, wantCounts)
	}

	streetSources := 0
	for key, source := range snapshot.Sources {
		if snapshot.Entities[source.ID].Kind != "street" {
			continue
		}
		streetSources++
		if !strings.HasPrefix(key, "overture:segment:") || source.ID != PublicID(key) || !strings.Contains(string(source.Raw), `"sources"`) {
			t.Fatalf("street identity/provenance mismatch: %s %+v", key, source)
		}
	}
	if streetSources != wantCounts["street"] {
		t.Fatalf("got %d street sources", streetSources)
	}

	store, err := places.Open(database)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, input := range []string{"Thames", "Bellevue Avenue", "Marlborough", "West Broadway"} {
		results, err := store.Autocomplete(context.Background(), input)
		if err != nil || len(results) == 0 {
			t.Fatalf("%q: no results: %v", input, err)
		}
		if results[0].Kind != "street" {
			t.Fatalf("%q: first result is %s", input, results[0].Kind)
		}
		if input == "West Broadway" && results[0].Name != "Dr Marcus Wheatland Boulevard" {
			t.Fatalf("alias lookup returned %q", results[0].Name)
		}
		for _, result := range results {
			detail, err := store.Details(context.Background(), result.ID)
			if err != nil || detail.ID != result.ID {
				t.Fatalf("%q details failed for %s: %v", input, result.ID, err)
			}
		}
		t.Logf("%q first result: %s %s", input, results[0].Kind, results[0].Name)
	}

	db, err := sql.Open("sqlite", database)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var repeated, distinctIDs int
	if err = db.QueryRow("SELECT count(*),count(DISTINCT id) FROM entities WHERE kind='street' AND name='Thames Street'").Scan(&repeated, &distinctIDs); err != nil {
		t.Fatal(err)
	}
	if repeated < 2 || repeated != distinctIDs {
		t.Fatalf("repeated segment names were merged: %d rows, %d IDs", repeated, distinctIDs)
	}
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("foreign key check found a violation")
	}
	t.Logf("verified %d named roads from %d regional Transportation segments", selection.NamedRoads, selection.Segments)
}

func sqlRows(t *testing.T, db *sql.DB, query string) [][]any {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	out := [][]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err = rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		out = append(out, values)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
