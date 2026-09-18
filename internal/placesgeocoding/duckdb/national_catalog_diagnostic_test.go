package duckdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openmaps/internal/importer"
)

// TestLegacyNationalCatalogDiagnostic is an opt-in forensic benchmark for the
// pre-partitioned catalog. It executes the old multi-statement schema one
// statement at a time so retained evidence names the first failing operator.
// It is intentionally absent from routine tests: it needs national normalized
// Parquet, hours of host time, and hundreds of GiB of temporary capacity.
func TestLegacyNationalCatalogDiagnostic(t *testing.T) {
	normalized := os.Getenv("OPENMAPS_LEGACY_CATALOG_DIAGNOSTIC_INPUT")
	output := os.Getenv("OPENMAPS_LEGACY_CATALOG_DIAGNOSTIC_OUTPUT")
	reportPath := os.Getenv("OPENMAPS_LEGACY_CATALOG_DIAGNOSTIC_REPORT")
	if normalized == "" && output == "" && reportPath == "" {
		t.Skip("set OPENMAPS_LEGACY_CATALOG_DIAGNOSTIC_INPUT, _OUTPUT, and _REPORT")
	}
	if normalized == "" || output == "" || reportPath == "" {
		t.Fatal("all legacy catalog diagnostic paths are required")
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("diagnostic output must not exist: %s", output)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0700); err != nil {
		t.Fatal(err)
	}

	type phase struct {
		Name    string  `json:"name"`
		Seconds float64 `json:"seconds"`
		Error   string  `json:"error,omitempty"`
	}
	type result struct {
		Input       string  `json:"input"`
		Output      string  `json:"output"`
		MemoryLimit string  `json:"memory_limit"`
		Threads     int     `json:"threads"`
		Phases      []phase `json:"phases"`
		Failure     string  `json:"failure,omitempty"`
	}
	r := result{Input: normalized, Output: output, MemoryLimit: "32GB", Threads: 4}
	writeReport := func() {
		if err := importer.WriteJSON(reportPath, r); err != nil {
			t.Errorf("write diagnostic report: %v", err)
		}
	}
	run := func(name string, work func() error) bool {
		started := time.Now()
		err := work()
		p := phase{Name: name, Seconds: time.Since(started).Seconds()}
		if err != nil {
			p.Error = err.Error()
			r.Failure = name
		}
		r.Phases = append(r.Phases, p)
		writeReport()
		t.Logf("legacy catalog phase=%s elapsed=%s error=%v", name, time.Since(started).Round(time.Second), err)
		return err == nil
	}

	spill := output + "-spill"
	dsn := output + "?threads=4&memory_limit=" + url.QueryEscape("32GB") +
		"&preserve_insertion_order=false&temp_directory=" + url.QueryEscape(spill)
	db, err := sql.Open("duckdb", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	ctx := context.Background()
	if _, err = db.ExecContext(ctx, "SET autoinstall_known_extensions=false; SET autoload_known_extensions=false"); err != nil {
		t.Fatal(err)
	}

	entitiesPath := filepath.Join(normalized, "entities*.parquet")
	prepared := strings.ReplaceAll(shardedSchema, "?", sqlString(entitiesPath))
	statements := strings.Split(prepared, ";")
	names := []string{
		"metadata", "entity_locator", "search_entities", "names", "entity_ranks",
		"corpus_stats", "tokens", "postings_stage", "short_prefix_head",
		"address_lookup", "address_spatial",
	}
	statementIndex := 0
	for _, statement := range statements {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if statementIndex >= len(names) {
			t.Fatalf("unmapped legacy schema statement %d", statementIndex)
		}
		query := statement
		if !run(names[statementIndex], func() error {
			_, execErr := db.ExecContext(ctx, query)
			return execErr
		}) {
			return
		}
		statementIndex++
	}
	if statementIndex != len(names) {
		t.Fatalf("legacy schema statements=%d want %d", statementIndex, len(names))
	}

	var entityCount int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM search_entities").Scan(&entityCount); err != nil {
		t.Fatal(err)
	}
	legacyPostings := `INSERT INTO postings_stage
WITH fields AS (
 SELECT entity_seq,4::UTINYINT field,unnest(string_split(normalized_name,' ')) token FROM search_entities WHERE entity_seq BETWEEN ? AND ?
 UNION ALL SELECT entity_seq,2::UTINYINT,unnest(string_split(normalized_aliases,' ')) FROM search_entities WHERE entity_seq BETWEEN ? AND ? AND normalized_aliases<>''
 UNION ALL SELECT entity_seq,1::UTINYINT,unnest(string_split(normalized_address,' ')) FROM search_entities WHERE entity_seq BETWEEN ? AND ? AND normalized_address<>''
), grouped AS (
 SELECT token,entity_seq,
 sum(CASE WHEN field=4 THEN 1 ELSE 0 END)::USMALLINT name_tf,
 sum(CASE WHEN field=2 THEN 1 ELSE 0 END)::USMALLINT alias_tf,
 sum(CASE WHEN field=1 THEN 1 ELSE 0 END)::USMALLINT address_tf
 FROM fields WHERE length(token)>=1 GROUP BY token,entity_seq
)
SELECT t.token_id,g.entity_seq,r.name_id,r.kind,r.closed,r.doc_len,g.name_tf,g.alias_tf,g.address_tf
FROM grouped g JOIN tokens t USING(token) JOIN entity_ranks r USING(entity_seq)
ORDER BY t.token_id,g.entity_seq`
	if !run("posting_generation", func() error {
		statement, prepareErr := db.PrepareContext(ctx, legacyPostings)
		if prepareErr != nil {
			return prepareErr
		}
		defer statement.Close()
		for start := 1; start <= entityCount; start += postingChunk {
			end := min(start+postingChunk-1, entityCount)
			if _, execErr := statement.ExecContext(ctx, start, end, start, end, start, end); execErr != nil {
				return fmt.Errorf("entities %d-%d: %w", start, end, execErr)
			}
		}
		return nil
	}) {
		return
	}
	if !run("posting_sort", func() error {
		_, execErr := db.ExecContext(ctx, "CREATE TABLE postings AS SELECT * FROM postings_stage ORDER BY token_id,entity_seq")
		return execErr
	}) {
		return
	}
	if !run("short_prefix_head", func() error {
		_, execErr := db.ExecContext(ctx, prefixesSQL)
		return execErr
	}) {
		return
	}
	if !run("checkpoint", func() error {
		_, execErr := db.ExecContext(ctx, "CHECKPOINT")
		return execErr
	}) {
		return
	}
	raw, _ := json.Marshal(r)
	t.Logf("legacy catalog unexpectedly completed: %s", raw)
}

func TestLegacyNationalCatalogDiagnosticStatementMap(t *testing.T) {
	statements := 0
	for _, statement := range strings.Split(shardedSchema, ";") {
		if strings.TrimSpace(statement) != "" {
			statements++
		}
	}
	if statements != 11 {
		t.Fatalf("legacy schema statements=%d want 11; update diagnostic phase names", statements)
	}
}
