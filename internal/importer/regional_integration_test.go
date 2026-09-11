//go:build integration

package importer_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"openmaps/internal/importer"
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
)

// TestPinnedNewportLookup rebuilds the checked-in pin from downloaded inputs
// and verifies the normalized relations and DuckDB serving generation directly.
func TestPinnedNewportLookup(t *testing.T) {
	dataDir := os.Getenv("OPENMAPS_DATA")
	if dataDir == "" {
		t.Fatal("set OPENMAPS_DATA to the prepared Newport source directory")
	}
	root := filepath.Join("..", "..")
	manifest, err := importer.ReadManifest(filepath.Join(root, "config/places-geocoding.json"))
	if err != nil {
		t.Fatal(err)
	}
	expectedBundleSHA256 := manifest.BundleSHA256
	manifest.BundleSHA256 = ""
	bundle, audit, err := importer.Prepare(context.Background(), manifest, dataDir, map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, _, err := importer.Prepare(context.Background(), manifest, dataDir, map[string]string{})
	if err != nil || !reflect.DeepEqual(bundle, rebuilt) {
		t.Fatal("regional preparation is not deterministic", err)
	}
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(append(encoded, '\n'))
	if got := hex.EncodeToString(sum[:]); got != expectedBundleSHA256 {
		t.Fatalf("normalized bundle checksum changed: got %s want %s", got, expectedBundleSHA256)
	}
	auditRaw, marshalErr := json.Marshal(audit["transportation_selection"])
	var selection map[string]int
	if marshalErr != nil || json.Unmarshal(auditRaw, &selection) != nil || selection["named_road_segments"] == 0 || selection["unnamed_road_segments"] == 0 || selection["non_road_segments"] == 0 {
		t.Fatalf("incomplete transportation selection audit: %#v", audit["transportation_selection"])
	}

	normalized, err := importer.Resolve(bundle)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	streetNames := map[string][]string{}
	for _, entity := range normalized.Entities {
		counts[entity.Kind]++
		if entity.Kind == "street" {
			streetNames[entity.Name] = append(streetNames[entity.Name], entity.ID)
		}
	}
	wantCounts := map[string]int{"business": 2173, "address": 8545, "area": 4, "street": 1621}
	if !reflect.DeepEqual(counts, wantCounts) {
		t.Fatalf("unexpected pinned counts: got %v want %v", counts, wantCounts)
	}
	streetSources := 0
	for _, source := range normalized.Sources {
		if !strings.HasPrefix(source.Record.Key(), "overture:segment:") {
			continue
		}
		streetSources++
		if source.EntityID != importer.PublicID(source.Record.Key()) || !strings.Contains(string(source.Record.Raw), `"sources"`) {
			t.Fatalf("street identity/provenance mismatch: %s", source.Record.Key())
		}
	}
	if streetSources != wantCounts["street"] || len(streetNames["Thames Street"]) < 2 {
		t.Fatalf("street identities lost: sources=%d Thames=%v", streetSources, streetNames["Thames Street"])
	}

	generation := filepath.Join(t.TempDir(), "newport")
	if err = placeduckdb.Build(context.Background(), generation, bundle); err != nil {
		t.Fatal(err)
	}
	store, err := placeduckdb.Open(generation)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, input := range []string{"Thames", "Bellevue Avenue", "Marlborough", "West Broadway"} {
		results, queryErr := store.Autocomplete(context.Background(), input)
		if queryErr != nil || len(results) == 0 || results[0].Kind != "street" {
			t.Fatalf("%q: unexpected results %+v: %v", input, results, queryErr)
		}
		if input == "West Broadway" && results[0].Name != "Dr Marcus Wheatland Boulevard" {
			t.Fatalf("alias lookup returned %q", results[0].Name)
		}
		for _, result := range results {
			detail, detailErr := store.Details(context.Background(), result.ID)
			if detailErr != nil || detail.ID != result.ID {
				t.Fatalf("%q details failed for %s: %v", input, result.ID, detailErr)
			}
		}
	}
	t.Logf("verified %d named roads from %d regional Transportation segments", selection["named_road_segments"], selection["segments"])
}
