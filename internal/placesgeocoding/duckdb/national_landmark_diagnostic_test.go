package duckdb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/parquet-go/parquet-go"

	"openmaps/internal/importer"
	"openmaps/internal/places"
)

// TestNationalLandmarkDiagnostic records the generic postings order and all
// exact-primary-name candidates for the maintained landmark checks, together
// with the complete retained source rows. It is opt-in because it requires the
// separately built national artifact.
func TestNationalLandmarkDiagnostic(t *testing.T) {
	artifact := os.Getenv("OPENMAPS_NATIONAL_LANDMARK_ARTIFACT")
	checksPath := os.Getenv("OPENMAPS_NATIONAL_LANDMARK_CHECKS")
	if artifact == "" || checksPath == "" {
		t.Skip("set OPENMAPS_NATIONAL_LANDMARK_ARTIFACT and OPENMAPS_NATIONAL_LANDMARK_CHECKS")
	}
	db, err := openDatabase(filepath.Join(artifact, IndexName))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := &Store{db: db}
	checks, err := importer.ReadQueryChecks(checksPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range checks {
		if check.Category != "landmark" {
			continue
		}
		normalized := places.Normalize(check.Input)
		results, queryErr := store.autocompletePostings(context.Background(), normalized)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		t.Logf("POSTINGS input=%q results=%s", check.Input, diagnosticJSON(results))
		rows, queryErr := db.Query(`SELECT e.entity_seq,e.id,e.kind,e.name,e.address,e.subtype,
       l.source_file,l.source_start,l.source_count
FROM search_entities e JOIN entity_locator l USING(id)
WHERE e.normalized_name=? AND NOT e.closed
ORDER BY e.kind,e.entity_seq`, normalized)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		for rows.Next() {
			var sequence, sourceStart, sourceCount int
			var entity places.Entity
			var sourceFile string
			if err = rows.Scan(&sequence, &entity.ID, &entity.Kind, &entity.Name, &entity.Address, &entity.Subtype,
				&sourceFile, &sourceStart, &sourceCount); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			sources, readErr := diagnosticSources(filepath.Join(artifact, sourceFile), sourceStart, sourceCount)
			if readErr != nil {
				rows.Close()
				t.Fatal(readErr)
			}
			t.Logf("EXACT input=%q sequence=%d entity=%s sources=%s", check.Input, sequence, diagnosticJSON(entity), diagnosticJSON(sources))
		}
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// TestNationalLandmarkRankingDiagnostic exercises the production unstructured
// ranking without loading the unrelated national parent-area graph. Artifact
// integrity is qualified separately; this diagnostic opens only the immutable
// source shards needed by exact-name ranking.
func TestNationalLandmarkRankingDiagnostic(t *testing.T) {
	artifact := os.Getenv("OPENMAPS_NATIONAL_LANDMARK_ARTIFACT")
	checksPath := os.Getenv("OPENMAPS_NATIONAL_LANDMARK_CHECKS")
	if artifact == "" || checksPath == "" {
		t.Skip("set OPENMAPS_NATIONAL_LANDMARK_ARTIFACT and OPENMAPS_NATIONAL_LANDMARK_CHECKS")
	}
	rawManifest, err := os.ReadFile(filepath.Join(artifact, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err = json.Unmarshal(rawManifest, &manifest); err != nil {
		t.Fatal(err)
	}
	db, err := openDatabase(filepath.Join(artifact, IndexName))
	if err != nil {
		t.Fatal(err)
	}
	store := &Store{
		db: db, files: map[string]*os.File{}, sourceParquet: map[string]*parquet.File{},
		primaryCandidateCache: map[string][]primaryCandidate{},
	}
	defer store.Close()
	for _, entry := range manifest.Files {
		if entry.Role != "sources" {
			continue
		}
		path := filepath.Join(artifact, entry.Name)
		file, openErr := os.Open(path)
		if openErr != nil {
			t.Fatal(openErr)
		}
		stat, statErr := file.Stat()
		if statErr != nil {
			file.Close()
			t.Fatal(statErr)
		}
		parquetFile, parquetErr := parquet.OpenFile(file, stat.Size())
		if parquetErr != nil {
			file.Close()
			t.Fatal(parquetErr)
		}
		store.files[entry.Name] = file
		store.sourceParquet[entry.Name] = parquetFile
	}
	checks, err := importer.ReadQueryChecks(checksPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range checks {
		if check.Category != "landmark" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		started := time.Now()
		results, queryErr := store.Autocomplete(ctx, check.Input)
		elapsed := time.Since(started)
		cancel()
		if queryErr != nil {
			t.Errorf("input=%q elapsed=%s error=%v", check.Input, elapsed, queryErr)
			continue
		}
		t.Logf("RANKED input=%q elapsed=%s results=%s", check.Input, elapsed, diagnosticJSON(results))
	}
}
