package opensearch

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"

	"openmaps/internal/importer"
	"openmaps/internal/places"
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
)

// TestOpenSearchIntegration is opt-in and exercises real index creation, Bulk
// ingestion, strict/fuzzy retrieval and existing Details resolution against a
// generated deterministic generation. It is skipped by the default suite.
func TestOpenSearchIntegration(t *testing.T) {
	endpoint := os.Getenv("OPENMAPS_OPENSEARCH_TEST_URL")
	if endpoint == "" {
		t.Skip("set OPENMAPS_OPENSEARCH_TEST_URL to an isolated OpenSearch instance")
	}
	generation, _ := fixtureGeneration(t)
	index := fmt.Sprintf("openmaps-opensearch-integration-%d", os.Getpid())
	client, err := NewClient(endpoint, index)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = client.request(context.Background(), http.MethodDelete, "/"+index, "", nil, nil) })
	if err = client.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = client.Export(context.Background(), ExportOptions{Generation: generation, BatchDocs: 3}); err != nil {
		t.Fatal(err)
	}
	details, err := placeduckdb.Open(generation)
	if err != nil {
		t.Fatal(err)
	}
	defer details.Close()
	store, err := NewStore(client, details)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		input, name, kind string
	}{
		{"White Horse", "White Horse Tavern", "business"},   // exact/prefix, closed exclusion
		{"Bellevue Coffee", "Café Bellevue", "business"},    // alias
		{"Newp", "Newport", "area"},                         // area prefix
		{"Marlborough Str", "Marlborough Street", "street"}, // normalized final prefix
	} {
		results, searchErr := store.Autocomplete(context.Background(), test.input)
		if searchErr != nil {
			t.Errorf("%s: %v", test.input, searchErr)
			continue
		}
		if len(results) == 0 || results[0].Name != test.name || results[0].Kind != test.kind {
			t.Errorf("%s: results=%+v", test.input, results)
		}
		for _, result := range results {
			if result.Name == "White Horse Closed" {
				t.Errorf("%s returned closed entity", test.input)
			}
		}
	}
	global, err := store.Autocomplete(context.Background(), "Common Cof")
	if err != nil {
		t.Fatal(err)
	}
	localID := importer.PublicID("fixture:place:local")
	for _, result := range global {
		if result.ID == localID {
			t.Fatalf("local fixture unexpectedly appeared in un-biased global top five: %+v", global)
		}
	}
	repeated, err := store.Autocomplete(context.Background(), "Common Cof")
	if err != nil || !sameIDs(global, repeated) {
		t.Fatalf("stable tie order changed: first=%+v repeated=%+v err=%v", global, repeated, err)
	}
	bias := &places.Viewport{South: 41.4709, West: -71.3291, North: 41.4711, East: -71.3289}
	local, err := store.AutocompleteWithBias(context.Background(), "Common Cof", bias)
	if err != nil || len(local) == 0 || local[0].ID != localID {
		t.Fatalf("viewport did not retrieve the local candidate outside the global top five: %+v err=%v", local, err)
	}
	areaBias := &places.Viewport{South: 41.471, West: -71.329, North: 41.473, East: -71.327}
	areas, err := store.AutocompleteWithBias(context.Background(), "Spring", areaBias)
	if err != nil || len(areas) < 2 || areas[0].ID != importer.PublicID("fixture:place:springfield-south") {
		t.Fatalf("ambiguous locality bias failed: %+v err=%v", areas, err)
	}
}

func sameIDs(a, b []places.Entity) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			return false
		}
	}
	return true
}
