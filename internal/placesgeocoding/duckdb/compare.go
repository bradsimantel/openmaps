package duckdb

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"openmaps/internal/importer"
	"openmaps/internal/places"
)

type QueryComparison struct {
	Check  importer.QueryCheck `json:"check"`
	Before []places.Entity     `json:"before"`
	After  []places.Entity     `json:"after"`
}

type Comparison struct {
	Schema               int               `json:"schema"`
	Baseline             Reference         `json:"baseline"`
	Candidate            Reference         `json:"candidate"`
	BaselineNormalized   string            `json:"baseline_normalized_sha256"`
	CandidateNormalized  string            `json:"candidate_normalized_sha256"`
	BeforeCounts         map[string]int    `json:"before_counts"`
	AfterCounts          map[string]int    `json:"after_counts"`
	Added                int               `json:"added"`
	Removed              int               `json:"removed"`
	Changed              int               `json:"changed"`
	RelationshipsAdded   int               `json:"relationships_added"`
	RelationshipsRemoved int               `json:"relationships_removed"`
	Queries              []QueryComparison `json:"queries"`
	Violations           []string          `json:"violations"`
}

// Compare checks immutable generations without loading either entity set into
// Go memory. DuckDB performs externally spillable joins over normalized
// Parquet; only aggregate counts and representative query results are retained.
func Compare(ctx context.Context, baselinePath, candidatePath string, checks []importer.QueryCheck) (Comparison, error) {
	report := Comparison{Schema: 1, BeforeCounts: map[string]int{}, AfterCounts: map[string]int{}, Queries: []QueryComparison{}, Violations: []string{}}
	var err error
	if report.Baseline, err = Describe(baselinePath); err != nil {
		return report, err
	}
	if report.Candidate, err = Describe(candidatePath); err != nil {
		return report, err
	}
	baselineManifest, err := Verify(baselinePath)
	if err != nil {
		return report, err
	}
	candidateManifest, err := Verify(candidatePath)
	if err != nil {
		return report, err
	}
	report.BaselineNormalized = baselineManifest.NormalizedSHA256
	report.CandidateNormalized = candidateManifest.NormalizedSHA256

	db, err := openDatabase(filepath.Join(baselinePath, IndexName))
	if err != nil {
		return report, err
	}
	defer db.Close()
	result, err := db.QueryContext(ctx, "SELECT kind,count(*) FROM main.search_entities GROUP BY kind ORDER BY kind")
	if err != nil {
		return report, err
	}
	for result.Next() {
		var kind string
		var count int
		if err = result.Scan(&kind, &count); err != nil {
			result.Close()
			return report, err
		}
		report.BeforeCounts[kind] = count
	}
	result.Close()
	candidateDB := filepath.Join(candidatePath, IndexName)
	if _, err = db.ExecContext(ctx, "ATTACH "+sqlString(candidateDB)+" AS candidate (READ_ONLY)"); err != nil {
		return report, err
	}
	rows, err := db.QueryContext(ctx, "SELECT kind,count(*) FROM candidate.search_entities GROUP BY kind ORDER BY kind")
	if err != nil {
		return report, err
	}
	for rows.Next() {
		var kind string
		var count int
		if err = rows.Scan(&kind, &count); err != nil {
			return report, err
		}
		report.AfterCounts[kind] = count
	}
	rows.Close()
	baseEntities, candidateEntities := filepath.Join(baselinePath, "entities*.parquet"), filepath.Join(candidatePath, "entities*.parquet")
	if err = db.QueryRowContext(ctx, `SELECT
count(*) FILTER (WHERE b.id IS NULL),
count(*) FILTER (WHERE c.id IS NULL),
count(*) FILTER (WHERE b.id IS NOT NULL AND c.id IS NOT NULL AND
	  (b.kind<>c.kind OR b.name<>c.name OR b.address<>c.address OR b.website<>c.website OR
	   b.subtype<>c.subtype OR b.lat<>c.lat OR b.lng<>c.lng OR b.closed<>c.closed OR
	   b.normalized_name<>c.normalized_name OR b.normalized_address<>c.normalized_address OR
	   b.normalized_aliases<>c.normalized_aliases OR b.address_key<>c.address_key OR
	   b.address_context<>c.address_context OR b.attributions<>c.attributions))
FROM read_parquet(?) b FULL OUTER JOIN read_parquet(?) c USING(id)`, baseEntities, candidateEntities).Scan(&report.Added, &report.Removed, &report.Changed); err != nil {
		return report, err
	}
	var changedKinds, changedSourceIDs int
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM read_parquet(?) b JOIN read_parquet(?) c USING(id) WHERE b.kind<>c.kind`, baseEntities, candidateEntities).Scan(&changedKinds); err != nil {
		return report, err
	}
	if changedKinds != 0 {
		report.Violations = append(report.Violations, fmt.Sprintf("%d continuing entities changed kind", changedKinds))
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM read_parquet(?) b JOIN read_parquet(?) c USING(source_key) WHERE b.entity_id<>c.entity_id`, filepath.Join(baselinePath, "source-records*.parquet"), filepath.Join(candidatePath, "source-records*.parquet")).Scan(&changedSourceIDs); err != nil {
		return report, err
	}
	if changedSourceIDs != 0 {
		report.Violations = append(report.Violations, fmt.Sprintf("%d continuing sources changed public ID", changedSourceIDs))
	}
	baseRelationships, candidateRelationships := filepath.Join(baselinePath, "relationships*.parquet"), filepath.Join(candidatePath, "relationships*.parquet")
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT * FROM read_parquet(?) EXCEPT SELECT * FROM read_parquet(?))`, candidateRelationships, baseRelationships).Scan(&report.RelationshipsAdded); err != nil {
		return report, err
	}
	if err = db.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT * FROM read_parquet(?) EXCEPT SELECT * FROM read_parquet(?))`, baseRelationships, candidateRelationships).Scan(&report.RelationshipsRemoved); err != nil {
		return report, err
	}
	before, err := Open(baselinePath)
	if err != nil {
		return report, err
	}
	defer before.Close()
	after, err := Open(candidatePath)
	if err != nil {
		return report, err
	}
	defer after.Close()
	if len(checks) == 0 {
		report.Violations = append(report.Violations, "representative query checks are required")
	}
	for _, check := range checks {
		oldResults, queryErr := before.Autocomplete(ctx, check.Input)
		if queryErr != nil {
			return report, queryErr
		}
		newResults, queryErr := after.Autocomplete(ctx, check.Input)
		if queryErr != nil {
			return report, queryErr
		}
		report.Queries = append(report.Queries, QueryComparison{Check: check, Before: oldResults, After: newResults})
		if strings.TrimSpace(check.Input) == "" || !check.Empty && check.FirstID == "" && check.FirstKind == "" {
			report.Violations = append(report.Violations, "query requires an expectation: "+check.Input)
		} else if check.Empty && len(newResults) != 0 || !check.Empty && (len(newResults) == 0 || check.FirstID != "" && newResults[0].ID != check.FirstID || check.FirstKind != "" && newResults[0].Kind != check.FirstKind) {
			report.Violations = append(report.Violations, "first result failed: "+check.Input)
		}
		for _, result := range newResults {
			detail, detailErr := after.Details(ctx, result.ID)
			if detailErr != nil {
				return report, detailErr
			}
			projection := places.Entity{ID: detail.ID, Kind: detail.Kind, Name: detail.Name, Address: detail.Address, Subtype: detail.Subtype}
			if !reflect.DeepEqual(projection, result) {
				report.Violations = append(report.Violations, "details mismatch: "+result.ID)
			}
		}
	}
	return report, nil
}
