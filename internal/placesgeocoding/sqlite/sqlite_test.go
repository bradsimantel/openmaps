package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"openmaps/internal/api"
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
		bundle.Records = append(bundle.Records, fixtureRecord(id, "business", "Common Coffee", places.Location{Lat: 41.50 + float64(i)/10000, Lng: -71.291}, false, nil))
	}
	bundle.Records = append(bundle.Records,
		fixtureRecord("local", "business", "Common Coffee", places.Location{Lat: 41.471, Lng: -71.329}, false, nil),
		fixtureRecord("alias-local", "business", "Elsewhere", places.Location{Lat: 41.4705, Lng: -71.329}, false, []string{"Common Coffee"}),
		fixtureRecord("springfield-north", "area", "Springfield", places.Location{Lat: 41.505, Lng: -71.291}, false, nil),
		fixtureRecord("springfield-south", "area", "Springfield", places.Location{Lat: 41.472, Lng: -71.328}, false, nil),
		fixtureRecord("closed-common", "business", "Common Coffee", places.Location{Lat: 41.471, Lng: -71.329}, true, nil),
		fixtureRecord("strong-global", "business", "Regional Museum", places.Location{Lat: 41.60, Lng: -71.20}, false, nil),
		fixtureRecord("weak-local", "business", "Corner Shop", places.Location{Lat: 41.471, Lng: -71.329}, false, []string{"Regional Museum"}),
	)
	for _, record := range []struct {
		id, name, subtype string
		location          places.Location
	}{{"rhode-island", "Rhode Island", "region", places.Location{Lat: 41.58, Lng: -71.47}}, {"newport-oregon", "Newport", "locality", places.Location{Lat: 44.64, Lng: -124.05}}} {
		item := fixtureRecord(record.id, "area", record.name, record.location, false, nil)
		item.Attributes["subtype"], _ = json.Marshal(record.subtype)
		item.Paths["subtype"] = "/fixture/subtype"
		bundle.Records = append(bundle.Records, item)
	}
	path := filepath.Join(t.TempDir(), "lookup")
	if err = placeduckdb.Build(context.Background(), path, bundle); err != nil {
		t.Fatal(err)
	}
	return path, bundle
}
func fixtureRecord(id, kind, name string, location places.Location, closed bool, aliases []string) importer.Record {
	nameJSON, _ := json.Marshal(name)
	locationJSON, _ := json.Marshal(location)
	closedJSON, _ := json.Marshal(closed)
	aliasJSON, _ := json.Marshal(aliases)
	return importer.Record{Source: "fixture:place", SourceID: id, Release: "fixture-v1", Priority: 100, Kind: kind, Attributes: map[string]json.RawMessage{"name": nameJSON, "location": locationJSON, "closed": closedJSON, "aliases": aliasJSON}, Paths: map[string]string{"name": "/fixture/name", "location": "/fixture/location", "closed": "/fixture/closed", "aliases": "/fixture/aliases"}, Raw: json.RawMessage(`{"fixture":true}`), Attributions: []places.Attribution{{Provider: "Synthetic test data", URI: "https://example.org/fixtures"}}}
}

func buildFixture(t *testing.T) (*Store, string, string, BuildStats) {
	t.Helper()
	generation, bundle := fixtureGeneration(t)
	out := filepath.Join(t.TempDir(), "sqlite")
	stats, err := Build(context.Background(), BuildOptions{Generation: generation, Output: out, Shards: 2, BatchSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Expected != int64(len(bundle.Records)) || stats.Attempted != stats.Expected || stats.Accepted != stats.Expected || stats.Rejected != 0 || stats.Final != stats.Expected {
		t.Fatalf("stats=%+v records=%d", stats, len(bundle.Records))
	}
	details, err := placeduckdb.Open(generation)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { details.Close() })
	store, err := Open(out, details)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, generation, out, stats
}

func TestBuildSearchBiasFuzzyIntegrityAndDetails(t *testing.T) {
	store, _, out, stats := buildFixture(t)
	if stats.IntegrityCheck != "ok" || stats.FTSIntegrityCheck != "ok" {
		t.Fatalf("integrity=%+v", stats)
	}
	for _, test := range []struct{ input, name, kind string }{{"White Horse", "White Horse Tavern", "business"}, {"Bellevue Coffee", "Café Bellevue", "business"}, {"Newp", "Newport", "area"}, {"Marlborough Str", "Marlborough Street", "street"}, {"Marlborogh Str", "Marlborough Street", "street"}} {
		results, err := store.Autocomplete(context.Background(), test.input)
		if err != nil {
			t.Errorf("%s: %v", test.input, err)
			continue
		}
		if len(results) == 0 || results[0].Name != test.name || results[0].Kind != test.kind {
			t.Errorf("%s results=%+v", test.input, results)
		}
		for _, result := range results {
			if result.Name == "White Horse Closed" || result.ID == "" {
				t.Errorf("invalid result %+v", result)
			}
		}
	}
	contextual, err := store.Autocomplete(context.Background(), "Newport, RI")
	if err != nil || len(contextual) == 0 || contextual[0].Name != "Newport" || places.DistanceMeters(contextual[0].Location, places.Location{Lat: 41.49, Lng: -71.31}) > 100_000 {
		t.Fatalf("contextual Newport results=%+v err=%v", contextual, err)
	}
	global, err := store.Autocomplete(context.Background(), "Common Cof")
	if err != nil {
		t.Fatal(err)
	}
	localID := importer.PublicID("fixture:place:local")
	for _, x := range global {
		if x.ID == localID {
			t.Fatalf("local fixture unexpectedly in global top five: %+v", global)
		}
	}
	bias := &places.Viewport{South: 41.4709, West: -71.3291, North: 41.4711, East: -71.3289}
	local, err := store.AutocompleteWithBias(context.Background(), "Common Cof", bias)
	if err != nil || len(local) == 0 || local[0].ID != localID {
		t.Fatalf("viewport result=%+v err=%v", local, err)
	}
	strong, err := store.AutocompleteWithBias(context.Background(), "Regional Museum", bias)
	if err != nil || len(strong) == 0 || strong[0].ID != importer.PublicID("fixture:place:strong-global") {
		t.Fatalf("strong global result=%+v err=%v", strong, err)
	}
	plans, err := store.QueryPlans(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"exact", "prefix", "contextual", "viewport"} {
		if len(plans[name]) == 0 {
			t.Errorf("missing %s plan", name)
		}
	}
	var indexCount int
	if err = store.shards[0].QueryRow("SELECT count(*) FROM pragma_index_list('entities')").Scan(&indexCount); err != nil || indexCount != 2 {
		t.Fatalf("entities indexes=%d err=%v", indexCount, err)
	}
	if _, err = os.Stat(filepath.Join(out, IncompleteName)); !os.IsNotExist(err) {
		t.Fatalf("final build retained incomplete marker: %v", err)
	}
}

func TestStableIDsAcrossRebuildsAndIncompleteIneligible(t *testing.T) {
	_, generation, first, _ := buildFixture(t)
	second := filepath.Join(t.TempDir(), "sqlite")
	if _, err := Build(context.Background(), BuildOptions{Generation: generation, Output: second, Shards: 2, BatchSize: 5}); err != nil {
		t.Fatal(err)
	}
	readIDs := func(path string) []string {
		raw, err := os.ReadFile(filepath.Join(path, ManifestName))
		if err != nil {
			t.Fatal(err)
		}
		var m Manifest
		if json.Unmarshal(raw, &m) != nil {
			t.Fatal("manifest")
		}
		ids := []string{}
		for _, f := range m.Shards {
			db, err := openReadOnly(filepath.Join(path, f.Name))
			if err != nil {
				t.Fatal(err)
			}
			rows, err := db.Query("SELECT id FROM entities ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var id string
				rows.Scan(&id)
				ids = append(ids, id)
			}
			rows.Close()
			db.Close()
		}
		return ids
	}
	a, b := readIDs(first), readIDs(second)
	if len(a) != len(b) {
		t.Fatalf("ids %d != %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("id %d: %s != %s", i, a[i], b[i])
		}
	}
	incomplete := filepath.Join(t.TempDir(), "incomplete")
	if err := os.Mkdir(incomplete, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(incomplete, IncompleteName), []byte(`{"status":"incomplete"}`), 0600); err != nil {
		t.Fatal(err)
	}
	details, err := placeduckdb.Open(generation)
	if err != nil {
		t.Fatal(err)
	}
	defer details.Close()
	if _, err = Open(incomplete, details); err == nil {
		t.Fatal("incomplete build opened")
	}
	if err := os.WriteFile(filepath.Join(first, IncompleteName), []byte(`{"status":"incomplete"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Open(first, details); err == nil {
		t.Fatal("generation with both manifest and incomplete marker opened")
	}
}

func openReadOnly(path string) (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
}

func TestCitySettlementTierRanksAheadOfMinorLocality(t *testing.T) {
	city := candidate{id: "city", kind: "area", subtype: "locality", settlementTier: int(places.SettlementCity)}
	minor := candidate{id: "minor", kind: "area", subtype: "locality", areaProminence: 100}
	if !betterCandidate("springfield", city, minor, nil) {
		t.Fatal("city settlement tier did not outrank a minor locality")
	}
}

func TestInventoryRecordsStorageAndServingPragmas(t *testing.T) {
	store, _, _, _ := buildFixture(t)
	inventory, err := store.Inventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory) != 2 {
		t.Fatalf("inventory shards=%d want 2", len(inventory))
	}
	for _, shard := range inventory {
		if shard.LogicalBytes == 0 || shard.ObjectBytes["entities"] == 0 || shard.ObjectBytes["entity_fts_data"] == 0 || shard.ObjectBytes["entity_rtree_node"] == 0 {
			t.Fatalf("missing storage evidence: %+v", shard)
		}
		if shard.JournalMode != "delete" || shard.QueryOnly != 1 || shard.MemoryMapBytes != 0 {
			t.Fatalf("unexpected serving pragmas: %+v", shard)
		}
	}
}

func TestExperimentalHTTPAutocompleteAndDetails(t *testing.T) {
	store, _, _, _ := buildFixture(t)
	handler := api.Handler{Places: store}
	request := httptest.NewRequest(http.MethodPost, "/v1/places:autocomplete", bytes.NewBufferString(`{"input":"White Horse"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("autocomplete status=%d body=%s", response.Code, response.Body.String())
	}
	var predictions struct {
		Suggestions []struct {
			PlacePrediction struct {
				PlaceID string `json:"placeId"`
			} `json:"placePrediction"`
		} `json:"suggestions"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &predictions); err != nil {
		t.Fatal(err)
	}
	if len(predictions.Suggestions) == 0 || len(predictions.Suggestions) > 5 {
		t.Fatalf("suggestions=%d", len(predictions.Suggestions))
	}
	for _, suggestion := range predictions.Suggestions {
		id := suggestion.PlacePrediction.PlaceID
		if id == "" {
			t.Fatal("empty public ID")
		}
		request = httptest.NewRequest(http.MethodGet, "/v1/places/"+id, nil)
		request.Header.Set("X-Goog-FieldMask", "id,displayName,location,types")
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("Details %s status=%d body=%s", id, response.Code, response.Body.String())
		}
		var detail struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &detail); err != nil || detail.ID != id {
			t.Fatalf("Details %s returned %q: %v", id, detail.ID, err)
		}
	}
}
