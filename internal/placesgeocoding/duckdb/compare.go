package duckdb

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"

	"openmaps/internal/geocoding"
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
	BaselineData         string            `json:"baseline_data_sha256,omitempty"`
	CandidateData        string            `json:"candidate_data_sha256,omitempty"`
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
	report.BaselineData = baselineManifest.DataSHA256
	report.CandidateData = candidateManifest.DataSHA256

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
		oldResults, queryErr := autocompleteForCheck(ctx, before, check)
		if queryErr != nil {
			return report, queryErr
		}
		newResults, queryErr := autocompleteForCheck(ctx, after, check)
		if queryErr != nil {
			return report, queryErr
		}
		report.Queries = append(report.Queries, QueryComparison{Check: check, Before: oldResults, After: newResults})
		violations, evaluateErr := queryCheckViolations(ctx, after, check, newResults)
		if evaluateErr != nil {
			return report, evaluateErr
		}
		report.Violations = append(report.Violations, violations...)
	}
	return report, nil
}

func autocompleteForCheck(ctx context.Context, store *Store, check importer.QueryCheck) ([]places.Entity, error) {
	if check.LocationBias != nil {
		return store.AutocompleteWithBias(ctx, check.Input, check.LocationBias)
	}
	return store.Autocomplete(ctx, check.Input)
}

func queryCheckViolations(ctx context.Context, store *Store, check importer.QueryCheck, results []places.Entity) ([]string, error) {
	violations := []string{}
	biasAssertion := check.FirstInsideBias || check.OutsideResultRequired || check.AllResultsOutsideBias || check.DistanceOrderedFromBiasCenter
	if check.LocationBias == nil && biasAssertion {
		violations = append(violations, "query has viewport assertion without location bias: "+check.Input)
	}
	if check.MinResults < 0 || check.MinResults > 5 {
		violations = append(violations, "query min_results must be in [0,5]: "+check.Input)
	}
	if check.FirstInsideBias && check.AllResultsOutsideBias {
		violations = append(violations, "query has contradictory viewport assertions: "+check.Input)
	}
	details := make([]places.Entity, len(results))
	for i, result := range results {
		detail, err := store.Details(ctx, result.ID)
		if err != nil {
			return nil, err
		}
		details[i] = detail
		projection := places.Entity{ID: detail.ID, Kind: detail.Kind, Name: detail.Name, Address: detail.Address, Subtype: detail.Subtype}
		resultProjection := places.Entity{ID: result.ID, Kind: result.Kind, Name: result.Name, Address: result.Address, Subtype: result.Subtype}
		if !reflect.DeepEqual(projection, resultProjection) {
			violations = append(violations, "details mismatch: "+result.ID)
		}
	}

	firstLocation := places.Location{}
	if len(details) != 0 {
		firstLocation = details[0].Location
	}
	hasFirstExpectation := check.FirstID != "" || check.FirstKind != "" || check.FirstName != "" || check.Near != nil
	if strings.TrimSpace(check.Input) == "" || check.Empty && hasFirstExpectation || !check.Empty && !hasFirstExpectation {
		violations = append(violations, "query requires an expectation: "+check.Input)
	} else if check.Near != nil && (check.Near.Lat < -90 || check.Near.Lat > 90 || check.Near.Lng < -180 || check.Near.Lng > 180 || check.Near.RadiusMeters <= 0) {
		violations = append(violations, "query has invalid location expectation: "+check.Input)
	} else if check.LocationBias != nil && check.LocationBias.Validate() != nil {
		violations = append(violations, "query has invalid location bias: "+check.Input)
	} else if check.Empty && len(results) != 0 || !check.Empty && (len(results) == 0 || check.FirstID != "" && results[0].ID != check.FirstID || check.FirstKind != "" && results[0].Kind != check.FirstKind || check.FirstName != "" && results[0].Name != check.FirstName || check.Near != nil && geocoding.DistanceMeters(firstLocation, places.Location{Lat: check.Near.Lat, Lng: check.Near.Lng}) > check.Near.RadiusMeters) {
		got := places.Entity{}
		if len(results) != 0 {
			got = results[0]
			got.Location = firstLocation
		}
		violations = append(violations, fmt.Sprintf("first result failed: %s: got id=%q kind=%q name=%q lat=%.7f lng=%.7f; want id=%q kind=%q name=%q near=%+v empty=%t", check.Input, got.ID, got.Kind, got.Name, got.Location.Lat, got.Location.Lng, check.FirstID, check.FirstKind, check.FirstName, check.Near, check.Empty))
	}
	if check.MinResults > 0 && len(results) < check.MinResults {
		violations = append(violations, fmt.Sprintf("minimum results failed: %s: got %d want at least %d", check.Input, len(results), check.MinResults))
	}
	if check.LocationBias == nil {
		return violations, nil
	}
	inside, outside := 0, 0
	previousDistance := -1.0
	for _, detail := range details {
		if check.LocationBias.Contains(detail.Location) {
			inside++
		} else {
			outside++
		}
		distance := places.DistanceMeters(check.LocationBias.Center(), detail.Location)
		if check.DistanceOrderedFromBiasCenter && previousDistance >= 0 && distance+0.001 < previousDistance {
			violations = append(violations, fmt.Sprintf("viewport distance order failed: %s: %.3fm followed %.3fm", check.Input, distance, previousDistance))
			break
		}
		previousDistance = distance
	}
	if check.FirstInsideBias && (len(details) == 0 || !check.LocationBias.Contains(details[0].Location)) {
		violations = append(violations, "first result is outside location bias: "+check.Input)
	}
	if check.OutsideResultRequired && outside == 0 {
		violations = append(violations, "outside result required by soft bias: "+check.Input)
	}
	if check.AllResultsOutsideBias && inside != 0 {
		violations = append(violations, fmt.Sprintf("all results should be outside location bias: %s: got %d inside", check.Input, inside))
	}
	return violations, nil
}
