package duckdb_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/duckdb/duckdb-go/v2"

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

func store(t *testing.T) (*placeduckdb.Store, string) {
	t.Helper()
	bundle := fixture(t)
	dir := t.TempDir()
	candidatePath := filepath.Join(dir, "duckdb")
	if err := placeduckdb.Build(context.Background(), candidatePath, bundle); err != nil {
		t.Fatal(err)
	}
	candidate, err := placeduckdb.Open(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = candidate.Close() })
	return candidate, candidatePath
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

func TestParquetEvidence(t *testing.T) {
	candidate, _ := store(t)
	id := importer.PublicID("fixture:place:tavern")
	evidence, err := candidate.Evidence(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if evidence.Entity.ID != id || len(evidence.Sources) != 1 || len(evidence.Attributes) == 0 || json.Unmarshal(evidence.Sources[0].Raw, &raw) != nil || raw["fixture"] != true {
		t.Fatalf("incomplete Parquet evidence: %+v", evidence)
	}
}

func TestConcurrentReadsMatchGoldenExpectations(t *testing.T) {
	candidate, _ := store(t)
	queries := []string{"W", "White", "26 Marl", "Marlborough", "Newport"}
	want := map[string]struct {
		count int
		first string
	}{
		"W":           {2, "White Horse Tavern"},
		"White":       {1, "White Horse Tavern"},
		"26 Marl":     {3, "26 Marlborough Street"},
		"Marlborough": {4, "Marlborough Street"},
		"Newport":     {3, "Newport"},
	}
	var wg sync.WaitGroup
	errors := make(chan error, 4)
	for worker := 0; worker < 4; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				query := queries[(worker+i)%len(queries)]
				got, err := candidate.Autocomplete(context.Background(), query)
				expected := want[query]
				if err != nil || len(got) != expected.count || len(got) > 0 && got[0].Name != expected.first {
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

func TestStreamBuildMatchesRegionalBundleWithConcurrentBatches(t *testing.T) {
	bundle := fixture(t)
	root := t.TempDir()
	regional, streamed := filepath.Join(root, "regional"), filepath.Join(root, "streamed")
	if err := placeduckdb.Build(context.Background(), regional, bundle); err != nil {
		t.Fatal(err)
	}
	maxBatch := 2
	produce := func(ctx context.Context, out placeduckdb.StreamWriter) error {
		var work sync.WaitGroup
		errs := make(chan error, len(bundle.Records)+len(bundle.Relationships)+len(bundle.Rejections))
		for end := len(bundle.Records); end > 0; {
			start := max(0, end-maxBatch)
			batch := bundle.Records[start:end]
			work.Add(1)
			go func() {
				defer work.Done()
				if err := out.WriteRecords(ctx, batch); err != nil {
					errs <- err
				}
			}()
			end = start
		}
		for i := range bundle.Relationships {
			batch := bundle.Relationships[i : i+1]
			work.Add(1)
			go func() {
				defer work.Done()
				if err := out.WriteRelationships(ctx, batch); err != nil {
					errs <- err
				}
			}()
		}
		for i := range bundle.Rejections {
			batch := bundle.Rejections[i : i+1]
			work.Add(1)
			go func() {
				defer work.Done()
				if err := out.WriteRejections(ctx, batch); err != nil {
					errs <- err
				}
			}()
		}
		work.Wait()
		close(errs)
		for err := range errs {
			return err
		}
		return nil
	}
	phases := map[string]bool{}
	if err := placeduckdb.BuildStreamConfigured(context.Background(), streamed, bundle.Manifest, bundle.Identities, "128MB", "256MB", 4, 4, "", func(name string, _ time.Duration) {
		phases[name] = true
	}, produce); err != nil {
		t.Fatal(err)
	}
	for _, bucket := range "0123456789abcdef" {
		if !phases["normalization_entities_"+string(bucket)] {
			t.Fatalf("missing normalization phase for entity bucket %c", bucket)
		}
		if !phases["normalization_relationship_keys_"+string(bucket)] {
			t.Fatalf("missing normalization phase for relationship-key bucket %c", bucket)
		}
		if !phases["normalization_relationship_from_"+string(bucket)] {
			t.Fatalf("missing normalization phase for relationship-source bucket %c", bucket)
		}
		if !phases["normalization_relationship_to_"+string(bucket)] {
			t.Fatalf("missing normalization phase for relationship-target bucket %c", bucket)
		}
		if !phases["normalization_relationships_"+string(bucket)] {
			t.Fatalf("missing normalization phase for relationship-output bucket %c", bucket)
		}
	}
	if !phases["normalization_rejections"] {
		t.Fatal("missing bounded rejection normalization phase")
	}
	want, err := placeduckdb.Verify(regional)
	if err != nil {
		t.Fatal(err)
	}
	got, err := placeduckdb.Verify(streamed)
	if err != nil {
		t.Fatal(err)
	}
	if got.NormalizedSHA256 != want.NormalizedSHA256 {
		readMetadata := func(path string) map[string]string {
			db, openErr := sql.Open("duckdb", ":memory:")
			if openErr != nil {
				t.Fatal(openErr)
			}
			defer db.Close()
			rows, queryErr := db.Query("SELECT key,value FROM read_parquet(?) ORDER BY key", filepath.Join(path, placeduckdb.MetadataName))
			if queryErr != nil {
				t.Fatal(queryErr)
			}
			defer rows.Close()
			values := map[string]string{}
			for rows.Next() {
				var key, value string
				if scanErr := rows.Scan(&key, &value); scanErr != nil {
					t.Fatal(scanErr)
				}
				values[key] = value
			}
			return values
		}
		t.Fatalf("streamed normalized output differs: got %s want %s\ngot metadata=%q\nwant metadata=%q\ngot files=%+v\nwant files=%+v", got.NormalizedSHA256, want.NormalizedSHA256, readMetadata(streamed), readMetadata(regional), got.Files, want.Files)
	}
	if got.DataSHA256 == "" || got.DataSHA256 != want.DataSHA256 {
		t.Fatalf("streamed normalized data differs: got %s want %s", got.DataSHA256, want.DataSHA256)
	}
	comparison, err := sql.Open("duckdb", filepath.Join(streamed, placeduckdb.IndexName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = comparison.Exec("ATTACH '" + strings.ReplaceAll(filepath.Join(regional, placeduckdb.IndexName), "'", "''") + "' AS baseline (READ_ONLY)"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"entity_locator", "search_entities", "names", "tokens", "postings", "short_prefix_head", "address_lookup", "address_spatial", "corpus_stats"} {
		var differences int
		query := fmt.Sprintf(`SELECT count(*) FROM (
(SELECT * FROM main.%[1]s EXCEPT SELECT * FROM baseline.%[1]s)
UNION ALL
(SELECT * FROM baseline.%[1]s EXCEPT SELECT * FROM main.%[1]s))`, table)
		if err = comparison.QueryRow(query).Scan(&differences); err != nil || differences != 0 {
			t.Fatalf("partitioned catalog differs in %s: differences=%d err=%v", table, differences, err)
		}
	}
	if err = comparison.Close(); err != nil {
		t.Fatal(err)
	}
	legacyManifest := want
	legacyManifest.DataSHA256 = ""
	if err = importer.WriteJSON(filepath.Join(regional, placeduckdb.ManifestName), legacyManifest); err != nil {
		t.Fatal(err)
	}
	legacyVerified, err := placeduckdb.Verify(regional)
	if err != nil || legacyVerified.DataSHA256 != want.DataSHA256 {
		t.Fatalf("could not derive retained-data checksum for a legacy manifest: got %+v err=%v", legacyVerified, err)
	}
	rejected := filepath.Join(root, "rejected")
	if err = placeduckdb.BuildStreamConfigured(context.Background(), rejected, bundle.Manifest, bundle.Identities, "128MB", "256MB", 4, 4, strings.Repeat("0", 64), nil, produce); err == nil || !strings.Contains(err.Error(), "data checksum mismatch") {
		t.Fatal("accepted an unexpected normalized data checksum", err)
	}
	if _, err = os.Stat(rejected); !os.IsNotExist(err) {
		t.Fatal("published a generation after its data checksum failed", err)
	}
	dangling := filepath.Join(root, "dangling")
	danglingBundle := bundle
	danglingBundle.Relationships = slices.Clone(bundle.Relationships)
	danglingBundle.Relationships[0].To = "fixture:missing"
	if err = placeduckdb.BuildStreamConfigured(context.Background(), dangling, danglingBundle.Manifest, danglingBundle.Identities, "128MB", "256MB", 4, 4, "", nil, func(ctx context.Context, out placeduckdb.StreamWriter) error {
		if writeErr := out.WriteRecords(ctx, danglingBundle.Records); writeErr != nil {
			return writeErr
		}
		return out.WriteRelationships(ctx, danglingBundle.Relationships)
	}); err == nil || !strings.Contains(err.Error(), "invalid relationship") {
		t.Fatal("accepted a dangling relationship", err)
	}
	store, err := placeduckdb.Open(streamed)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err = store.Details(context.Background(), importer.PublicID("fixture:place:tavern")); err != nil {
		t.Fatal("streamed relationship fixture lost its business entity", err)
	}
}

func TestStreamBuildResumesFromValidatedInputCheckpoint(t *testing.T) {
	bundle := fixture(t)
	root := t.TempDir()
	output := filepath.Join(root, "resumed")
	checkpoint := filepath.Join(root, "resumed-checkpoint")
	wantState := json.RawMessage(`{"preparation":"complete"}`)
	producerCalls := 0
	produce := func(ctx context.Context, out placeduckdb.StreamWriter) (json.RawMessage, error) {
		producerCalls++
		if err := out.WriteRecords(ctx, bundle.Records); err != nil {
			return nil, err
		}
		if err := out.WriteRelationships(ctx, bundle.Relationships); err != nil {
			return nil, err
		}
		if err := out.WriteRejections(ctx, bundle.Rejections); err != nil {
			return nil, err
		}
		return wantState, nil
	}
	options := placeduckdb.StreamCheckpointOptions{Path: checkpoint, BuildIdentity: "test-revision/linux-amd64"}
	ctx, cancel := context.WithCancel(context.Background())
	_, err := placeduckdb.BuildStreamResumable(ctx, output, bundle.Manifest, bundle.Identities, "128MB", "256MB", 2, 2, "", options, func(name string, _ time.Duration) {
		if name == "input_staging" {
			cancel()
		}
	}, produce)
	if err == nil || !strings.Contains(err.Error(), "checkpoint retained") {
		t.Fatalf("interrupted build did not retain a checkpoint: %v", err)
	}
	if producerCalls != 1 {
		t.Fatalf("producer calls=%d want 1", producerCalls)
	}
	if _, err = os.Stat(checkpoint); err != nil {
		t.Fatal("checkpoint was not published", err)
	}

	mismatched := options
	mismatched.Resume = true
	mismatched.BuildIdentity = "different-revision/linux-amd64"
	if _, err = placeduckdb.BuildStreamResumable(context.Background(), output, bundle.Manifest, bundle.Identities, "128MB", "256MB", 2, 2, "", mismatched, nil, produce); err == nil || !strings.Contains(err.Error(), "identity does not match") {
		t.Fatal("accepted a checkpoint from a different build identity", err)
	}

	options.Resume = true
	resumedState, err := placeduckdb.BuildStreamResumable(context.Background(), output, bundle.Manifest, bundle.Identities, "128MB", "256MB", 2, 2, "", options, nil, func(context.Context, placeduckdb.StreamWriter) (json.RawMessage, error) {
		return nil, fmt.Errorf("producer must not run during resume")
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(resumedState, wantState) {
		t.Fatalf("resumed state=%s want %s", resumedState, wantState)
	}
	if producerCalls != 1 {
		t.Fatalf("producer ran during resume: calls=%d", producerCalls)
	}
	if _, err = os.Stat(checkpoint); !os.IsNotExist(err) {
		t.Fatal("checkpoint remained after publication", err)
	}
	if _, err = placeduckdb.Verify(output); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogResumesFromImmutableNormalizedCheckpoint(t *testing.T) {
	bundle := fixture(t)
	root := t.TempDir()
	output := filepath.Join(root, "output")
	normalized := filepath.Join(root, "normalized")
	producerCalls := 0
	ctx, cancel := context.WithCancel(context.Background())
	_, err := placeduckdb.BuildStreamResumable(ctx, output, bundle.Manifest, bundle.Identities, "128MB", "256MB", 2, 2, "", placeduckdb.StreamCheckpointOptions{
		NormalizedPath: normalized, NormalizedBuildIdentity: "test-revision/linux-amd64",
	}, func(name string, _ time.Duration) {
		if name == "normalized_checkpoint" {
			cancel()
		}
	}, func(ctx context.Context, out placeduckdb.StreamWriter) (json.RawMessage, error) {
		producerCalls++
		if err := out.WriteRecords(ctx, bundle.Records); err != nil {
			return nil, err
		}
		if err := out.WriteRelationships(ctx, bundle.Relationships); err != nil {
			return nil, err
		}
		return nil, out.WriteRejections(ctx, bundle.Rejections)
	})
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("catalog interruption did not fail after normalized publication: %v", err)
	}
	if producerCalls != 1 {
		t.Fatalf("producer calls=%d want 1", producerCalls)
	}
	if _, err = os.Stat(normalized); err != nil {
		t.Fatal("normalized checkpoint missing", err)
	}
	if _, err = os.Stat(output); !os.IsNotExist(err) {
		t.Fatal("failed catalog published output", err)
	}
	if err = placeduckdb.BuildCatalogFromNormalized(context.Background(), output, placeduckdb.NormalizedResumeOptions{
		Path: normalized, Manifest: bundle.Manifest, Identities: bundle.Identities,
		MemoryLimit: "128MB", DatabaseThreads: 2, BuildIdentity: "test-revision/linux-amd64",
	}, "256MB", 1, nil); err != nil {
		t.Fatal(err)
	}
	if producerCalls != 1 {
		t.Fatalf("catalog resume reran producer: calls=%d", producerCalls)
	}
	if _, err = placeduckdb.Verify(output); err != nil {
		t.Fatal(err)
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
	manifestChecksum, err := importer.Checksum(filepath.Join(corrupt, placeduckdb.ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	badReference := placeduckdb.Reference{Path: corrupt, SHA256: manifestChecksum}
	if err = importer.WriteJSON(state, placeduckdb.Selection{Schema: 1, Current: badReference, Previous: &firstReference}); err != nil {
		t.Fatal(err)
	}
	assertStatus(http.StatusOK)
	current, reloadError := handler.Status()
	if current != firstReference || reloadError == "" {
		t.Fatalf("corrupt reload did not retain and report the active generation: current=%+v error=%q", current, reloadError)
	}
}

func TestMissingShardInterruptedBuildAndGenerationComparison(t *testing.T) {
	bundle := fixture(t)
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	if err := placeduckdb.Build(context.Background(), first, bundle); err != nil {
		t.Fatal(err)
	}
	if err := placeduckdb.Build(context.Background(), second, bundle); err != nil {
		t.Fatal(err)
	}
	comparison, err := placeduckdb.Compare(context.Background(), first, second, []importer.QueryCheck{{Input: "White", FirstID: importer.PublicID("fixture:place:tavern")}})
	if err != nil || len(comparison.Violations) != 0 || comparison.Added != 0 || comparison.Removed != 0 || comparison.Changed != 0 {
		t.Fatalf("identical generation comparison failed: %+v: %v", comparison, err)
	}
	missing := filepath.Join(root, "missing-source.parquet")
	if err = os.Rename(filepath.Join(second, placeduckdb.SourcesName), missing); err != nil {
		t.Fatal(err)
	}
	if _, err = placeduckdb.Open(second); err == nil {
		t.Fatal("opened generation with missing source shard")
	}
	raw, err := json.Marshal(bundle)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	interrupted := filepath.Join(root, "interrupted")
	if err = placeduckdb.BuildJSON(ctx, interrupted, bytes.NewReader(raw)); err == nil {
		t.Fatal("completed a canceled build")
	}
	if _, err = os.Stat(interrupted); !os.IsNotExist(err) {
		t.Fatalf("interrupted generation was published: %v", err)
	}
}
