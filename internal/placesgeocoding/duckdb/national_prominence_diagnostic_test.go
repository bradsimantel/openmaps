package duckdb

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/parquet-go/parquet-go"

	"openmaps/internal/importer"
	"openmaps/internal/places"
)

// TestNationalProminenceDiagnostic records the underlying postings order and
// every exact-name area candidate, including its retained source evidence. It
// is opt-in because it requires the separately built national artifact.
func TestNationalProminenceDiagnostic(t *testing.T) {
	artifact := os.Getenv("OPENMAPS_NATIONAL_PROMINENCE_ARTIFACT")
	checksPath := os.Getenv("OPENMAPS_NATIONAL_PROMINENCE_CHECKS")
	if artifact == "" || checksPath == "" {
		t.Skip("set OPENMAPS_NATIONAL_PROMINENCE_ARTIFACT and OPENMAPS_NATIONAL_PROMINENCE_CHECKS")
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
		if check.Category != "city" && check.Category != "state" {
			continue
		}
		normalized := places.Normalize(check.Input)
		results, queryErr := store.autocompletePostings(context.Background(), normalized)
		if queryErr != nil {
			t.Fatal(queryErr)
		}
		t.Logf("POSTINGS category=%s input=%q results=%s", check.Category, check.Input, diagnosticJSON(results))
		rows, queryErr := db.Query(`SELECT e.entity_seq,e.id,e.kind,e.name,e.address,e.subtype,
       l.source_file,l.source_start,l.source_count
FROM search_entities e JOIN entity_locator l USING(id)
WHERE e.normalized_name=? AND e.kind='area' AND NOT e.closed
ORDER BY e.entity_seq`, normalized)
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
			t.Logf("AREA input=%q sequence=%d entity=%s sources=%s", check.Input, sequence, diagnosticJSON(entity), diagnosticJSON(sources))
		}
		if err = rows.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func diagnosticSources(path string, start, count int) ([]sourceRow, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	reader := parquet.NewGenericReader[sourceRow](file)
	defer reader.Close()
	if err = reader.SeekToRow(int64(start)); err != nil {
		return nil, err
	}
	rows := make([]sourceRow, count)
	n, err := reader.Read(rows)
	if n != count || err != nil && err != io.EOF {
		return nil, fmt.Errorf("read diagnostic sources: rows=%d/%d: %w", n, count, err)
	}
	return rows[:n], nil
}

func diagnosticJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
