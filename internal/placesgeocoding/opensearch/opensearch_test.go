package opensearch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"openmaps/internal/importer"
	"openmaps/internal/places"
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
)

func fixtureGeneration(t *testing.T) (string, importer.Bundle) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "importer", "testdata", "small.json"))
	if err != nil {
		t.Fatal(err)
	}
	var bundle importer.Bundle
	if err = json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"global-0", "global-1", "global-2", "global-3", "global-4", "global-5"} {
		bundle.Records = append(bundle.Records, fixtureRecord(id, "business", "Common Coffee", places.Location{Lat: 41.50 + float64(i)/10000, Lng: -71.291}))
	}
	bundle.Records = append(bundle.Records,
		fixtureRecord("local", "business", "Common Coffee", places.Location{Lat: 41.471, Lng: -71.329}),
		fixtureRecord("springfield-north", "area", "Springfield", places.Location{Lat: 41.505, Lng: -71.291}),
		fixtureRecord("springfield-south", "area", "Springfield", places.Location{Lat: 41.472, Lng: -71.328}),
	)
	path := filepath.Join(t.TempDir(), "lookup")
	if err = placeduckdb.Build(context.Background(), path, bundle); err != nil {
		t.Fatal(err)
	}
	return path, bundle
}

func fixtureRecord(id, kind, name string, location places.Location) importer.Record {
	nameJSON, _ := json.Marshal(name)
	locationJSON, _ := json.Marshal(location)
	return importer.Record{
		Source: "fixture:place", SourceID: id, Release: "fixture-v1", Priority: 100, Kind: kind,
		Attributes: map[string]json.RawMessage{"name": nameJSON, "location": locationJSON},
		Paths:      map[string]string{"name": "/fixture/name", "location": "/fixture/location"},
		Raw:        json.RawMessage(`{"fixture":true}`),
		Attributions: []places.Attribution{{
			Provider: "Synthetic test data", URI: "https://example.org/fixtures",
		}},
	}
}

func TestStreamingExporterAccountsForRetriesAndProjection(t *testing.T) {
	generation, bundle := fixtureGeneration(t)
	var mu sync.Mutex
	accepted := map[string]bool{}
	attempts := map[string]int{}
	var sawProjection bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/":
			json.NewEncoder(w).Encode(map[string]any{"version": map[string]string{"number": "3.8.0"}})
		case r.Method == http.MethodPut && r.URL.Path == "/test-index":
			body := json.RawMessage{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !strings.Contains(string(body), `"dynamic": "strict"`) {
				t.Errorf("invalid index body: %v %s", err, body)
			}
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/test-index/_bulk":
			if r.Header.Get("Content-Type") != "application/x-ndjson" {
				t.Errorf("bulk content type %q", r.Header.Get("Content-Type"))
			}
			scanner := bufio.NewScanner(r.Body)
			items := []any{}
			for scanner.Scan() {
				var action struct {
					Index struct {
						ID string `json:"_id"`
					} `json:"index"`
				}
				if err := json.Unmarshal(scanner.Bytes(), &action); err != nil {
					t.Error(err)
				}
				if !scanner.Scan() {
					t.Fatal("bulk action without source")
				}
				var doc Document
				if err := json.Unmarshal(scanner.Bytes(), &doc); err != nil {
					t.Error(err)
				}
				if doc.ID != action.Index.ID || doc.Name == "" || doc.Location == (GeoPoint{}) {
					t.Errorf("invalid projection: %+v action=%s", doc, action.Index.ID)
				}
				sawProjection = sawProjection || doc.NormalizedName != "" && doc.Aliases != nil && doc.SearchText != ""
				mu.Lock()
				attempts[doc.ID]++
				status := http.StatusCreated
				if len(attempts) == 1 && attempts[doc.ID] == 1 {
					status = http.StatusTooManyRequests
				} else {
					accepted[doc.ID] = true
				}
				mu.Unlock()
				items = append(items, map[string]any{"index": map[string]any{"status": status}})
			}
			json.NewEncoder(w).Encode(map[string]any{"items": items})
		case r.Method == http.MethodPost && r.URL.Path == "/test-index/_refresh":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/test-index/_count":
			mu.Lock()
			count := len(accepted)
			mu.Unlock()
			json.NewEncoder(w).Encode(map[string]int{"count": count})
		case r.Method == http.MethodPut && r.URL.Path == "/test-index/_settings":
			w.WriteHeader(http.StatusOK)
		default:
			http.Error(w, r.Method+" "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "test-index")
	if err != nil {
		t.Fatal(err)
	}
	stats, err := client.Export(context.Background(), ExportOptions{Generation: generation, BatchDocs: 3, BatchBytes: 1 << 20, Retries: 2})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Expected != len(bundle.Records) || stats.Read != stats.Expected || stats.Accepted != stats.Expected || stats.Rejected != 0 || stats.Retried != 1 || stats.IndexedCount != int64(stats.Expected) {
		t.Fatalf("stats=%+v records=%d", stats, len(bundle.Records))
	}
	if !sawProjection {
		t.Fatal("normalized projection was not observed")
	}
}

type fakeDetails map[string]places.Entity

func (f fakeDetails) Details(_ context.Context, id string) (places.Entity, error) {
	entity, ok := f[id]
	if !ok {
		return places.Entity{}, fmt.Errorf("missing %s", id)
	}
	return entity, nil
}

func TestSearchUsesCompletedTokensPrefixFallbackBiasAndDetails(t *testing.T) {
	queries := []map[string]any{}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/test-index/_search" {
			http.NotFound(w, r)
			return
		}
		var query map[string]any
		if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
			t.Fatal(err)
		}
		queries = append(queries, query)
		calls++
		ids := []string{"om_a", "om_b"}
		if calls == 4 {
			ids = []string{"om_b", "om_c", "om_d", "om_e"}
		}
		hits := []any{}
		for i, id := range ids {
			hits = append(hits, map[string]any{"_id": id, "_score": 10 - i, "fields": map[string]any{"location": []string{fmt.Sprintf("%f, -87.62", 41.88+float64(i)/100)}}})
		}
		json.NewEncoder(w).Encode(map[string]any{"hits": map[string]any{"hits": hits}})
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "test-index")
	if err != nil {
		t.Fatal(err)
	}
	details := fakeDetails{}
	for i, id := range []string{"om_a", "om_b", "om_c", "om_d", "om_e"} {
		details[id] = places.Entity{ID: id, Kind: "business", Name: "The UPS Store", Location: places.Location{Lat: 41.88 + float64(i)/100, Lng: -87.62}}
	}
	store, _ := NewStore(client, details)
	bias := &places.Viewport{South: 41.8, West: -87.7, North: 41.9, East: -87.5}
	results, err := store.AutocompleteWithBias(context.Background(), "ups st", bias)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 5 || results[0].ID != "om_a" || calls != 4 {
		t.Fatalf("results=%+v calls=%d", results, calls)
	}
	exact, _ := json.Marshal(queries[0])
	global, _ := json.Marshal(queries[1])
	strict, _ := json.Marshal(queries[2])
	fuzzy, _ := json.Marshal(queries[3])
	if !strings.Contains(string(exact), `"normalized_name":"ups street"`) || !strings.Contains(string(exact), `"geo_distance"`) {
		t.Fatalf("exact primary-name query is not locally bounded: %s", exact)
	}
	if !strings.Contains(string(global), `"normalized_name":"ups street"`) || strings.Contains(string(global), `"geo_distance"`) {
		t.Fatalf("exact fallback must retain global candidates: %s", global)
	}
	if !strings.Contains(string(strict), `"query":"ups"`) || !strings.Contains(string(strict), `"query":"street"`) || !strings.Contains(string(strict), `"gauss"`) {
		t.Fatalf("strict query does not preserve completed/final token or bias: %s", strict)
	}
	if !strings.Contains(string(fuzzy), `"fuzziness":"AUTO"`) || !strings.Contains(string(fuzzy), `"max_expansions":20`) {
		t.Fatalf("fallback is not bounded: %s", fuzzy)
	}
}

func TestMergeRankedHitsKeepsViewportBiasSoft(t *testing.T) {
	local := []searchHit{{ID: "local", Score: 310}, {ID: "duplicate", Score: 300}}
	global := []searchHit{{ID: "global", Score: 400}, {ID: "duplicate", Score: 10}}
	merged := mergeRankedHits(local, global)
	if len(merged) != 3 || merged[0].ID != "global" || merged[1].ID != "local" || merged[2].ID != "duplicate" || merged[2].Score != 300 {
		t.Fatalf("merged=%+v", merged)
	}
}

func TestMappingIsStrictAndComplete(t *testing.T) {
	raw := string(IndexDefinition())
	for _, required := range []string{`"dynamic": "strict"`, `"search_as_you_type"`, `"geo_point"`, `"area_prominence"`, `"destination_class"`, `"closed"`} {
		if !strings.Contains(raw, required) {
			t.Errorf("mapping missing %s", required)
		}
	}
}
