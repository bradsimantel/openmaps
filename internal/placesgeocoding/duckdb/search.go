package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/parquet-go/parquet-go"

	"openmaps/internal/geocoding"
	"openmaps/internal/places"
)

func prefixUpperBound(prefix string) (string, bool) {
	runes := []rune(prefix)
	for i := len(runes) - 1; i >= 0; i-- {
		if runes[i] >= utf8.MaxRune {
			continue
		}
		next := runes[i] + 1
		if next >= 0xD800 && next <= 0xDFFF {
			next = 0xE000
		}
		runes[i] = next
		return string(runes[:i+1]), true
	}
	return "", false
}

func (s *Store) Autocomplete(ctx context.Context, input string) ([]places.Entity, error) {
	if interpreted, ok := places.ParseAutocompleteContext(input); ok {
		return s.autocompleteContext(ctx, interpreted)
	}
	normalized := places.Normalize(input)
	if normalized == "" {
		return []places.Entity{}, nil
	}
	if !strings.Contains(normalized, " ") && utf8.RuneCountInString(normalized) <= 2 {
		rows, err := s.db.QueryContext(ctx, `SELECT e.id,e.kind,e.name,e.address,e.subtype
FROM short_prefix_head h JOIN search_entities e USING(entity_seq)
WHERE h.prefix=? ORDER BY h.rank`, normalized)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		out := []places.Entity{}
		for rows.Next() {
			var entity places.Entity
			if err = rows.Scan(&entity.ID, &entity.Kind, &entity.Name, &entity.Address, &entity.Subtype); err != nil {
				return nil, err
			}
			canonicalizeEntity(&entity)
			out = append(out, entity)
		}
		return out, rows.Err()
	}
	return s.autocompletePostings(ctx, normalized)
}

type primaryCandidate struct {
	sequence                  int
	entity                    places.Entity
	entityFile                string
	entityRowGroup, entityRow int
	location                  places.Location
}

const (
	contextStreetRadiusMeters      = 100_000
	contextStreetEarlyAcceptMeters = 25_000
	contextStreetCandidatePageSize = 1024
	contextAreaLocatorThreshold    = 64
	primaryCandidateCacheLimit     = 256
)

func (s *Store) primaryCandidates(ctx context.Context, normalized, kind string) ([]primaryCandidate, error) {
	cacheKey := kind + "\x00" + normalized
	s.primaryCandidateCacheMu.RLock()
	cached, ok := s.primaryCandidateCache[cacheKey]
	s.primaryCandidateCacheMu.RUnlock()
	if ok {
		return append([]primaryCandidate(nil), cached...), nil
	}
	tokens := strings.Fields(normalized)
	if len(tokens) == 0 {
		return []primaryCandidate{}, nil
	}
	selectiveToken := tokens[0]
	for _, token := range tokens[1:] {
		if len(token) > len(selectiveToken) {
			selectiveToken = token
		}
	}
	query := `WITH requested AS (
  SELECT name_id FROM names WHERE normalized_name=?
), matched AS (
  SELECT p.entity_seq
  FROM tokens t JOIN postings p USING(token_id) JOIN requested r USING(name_id)
  WHERE t.token=? AND p.name_tf>0 AND p.kind=? AND NOT p.closed
)
SELECT e.entity_seq,e.id,e.kind,e.name,e.address,e.subtype,
       l.entity_file,l.entity_row_group,l.entity_row
FROM matched
JOIN search_entities e USING(entity_seq)
JOIN entity_locator l USING(id)
ORDER BY e.entity_seq`
	rows, err := s.db.QueryContext(ctx, query, normalized, selectiveToken, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	candidates := []primaryCandidate{}
	for rows.Next() {
		var candidate primaryCandidate
		if err = rows.Scan(&candidate.sequence, &candidate.entity.ID, &candidate.entity.Kind,
			&candidate.entity.Name, &candidate.entity.Address, &candidate.entity.Subtype,
			&candidate.entityFile, &candidate.entityRowGroup, &candidate.entityRow); err != nil {
			return nil, err
		}
		canonicalizeEntity(&candidate.entity)
		candidates = append(candidates, candidate)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	s.primaryCandidateCacheMu.Lock()
	if s.primaryCandidateCache == nil {
		s.primaryCandidateCache = map[string][]primaryCandidate{}
	}
	if existing, exists := s.primaryCandidateCache[cacheKey]; exists {
		candidates = existing
	} else if len(s.primaryCandidateCache) < primaryCandidateCacheLimit {
		s.primaryCandidateCache[cacheKey] = append([]primaryCandidate(nil), candidates...)
	}
	s.primaryCandidateCacheMu.Unlock()
	return append([]primaryCandidate(nil), candidates...), nil
}

func (s *Store) autocompleteContext(ctx context.Context, query places.AutocompleteContext) ([]places.Entity, error) {
	regions, err := s.primaryCandidates(ctx, query.Region, "area")
	if err != nil {
		return nil, err
	}
	regionIDs := map[string]bool{}
	for _, region := range regions {
		if region.entity.Subtype == "region" {
			regionIDs[region.entity.ID] = true
		}
	}
	if len(regionIDs) == 0 {
		return []places.Entity{}, nil
	}
	localityName := query.Name
	if query.Locality != "" {
		localityName = query.Locality
	}
	localities, err := s.contextAreaCandidates(ctx, localityName, regionIDs)
	if err != nil {
		return nil, err
	}
	if len(localities) == 0 {
		return []places.Entity{}, nil
	}
	sort.SliceStable(localities, func(i, j int) bool {
		left, right := areaSubtypeRank(localities[i].entity.Subtype), areaSubtypeRank(localities[j].entity.Subtype)
		if left != right {
			return left < right
		}
		return localities[i].sequence < localities[j].sequence
	})
	if query.Locality == "" {
		return candidateEntities(localities, 5), nil
	}
	if err = s.loadCandidateLocations(localities[:1]); err != nil {
		return nil, err
	}
	anchor := localities[0].location
	street, found, err := s.contextStreet(ctx, query.Name, anchor)
	if err != nil {
		return nil, err
	}
	if !found {
		return []places.Entity{}, nil
	}
	// Equal street labels are individual source segments. As in unstructured
	// autocomplete, expose one representative rather than five indistinguishable
	// predictions; context chooses a segment within the intended locality's
	// vicinity.
	return []places.Entity{street.entity}, nil
}

func (s *Store) contextAreaCandidates(ctx context.Context, normalized string, regionIDs map[string]bool) ([]primaryCandidate, error) {
	sequences, err := s.exactCandidateSequences(ctx, normalized, "area")
	if err != nil {
		return nil, err
	}
	if len(sequences) <= contextAreaLocatorThreshold {
		candidates, err := s.primaryCandidates(ctx, normalized, "area")
		if err != nil {
			return nil, err
		}
		return filterLocalityTypes(filterAreaDescendants(candidates, regionIDs, s.areaParents)), nil
	}

	// The full search_entities join is disproportionately expensive for very
	// common area names in the national catalog. Resolve their compact locators
	// first, discard candidates outside the requested region, and materialize
	// only the surviving entities.
	candidates, err := s.candidateLocators(ctx, sequences)
	if err != nil {
		return nil, err
	}
	candidates = filterAreaDescendants(candidates, regionIDs, s.areaParents)
	for i := range candidates {
		entity, err := s.Details(ctx, candidates[i].entity.ID)
		if err != nil {
			return nil, err
		}
		candidates[i].entity = entity
		candidates[i].location = entity.Location
	}
	return filterLocalityTypes(candidates), nil
}

func (s *Store) exactCandidateSequences(ctx context.Context, normalized, kind string) ([]int, error) {
	tokens := strings.Fields(normalized)
	if len(tokens) == 0 {
		return []int{}, nil
	}
	selectiveToken := tokens[0]
	for _, token := range tokens[1:] {
		if len(token) > len(selectiveToken) {
			selectiveToken = token
		}
	}
	var nameID, tokenID int
	if err := s.db.QueryRowContext(ctx, "SELECT name_id FROM names WHERE normalized_name=?", normalized).Scan(&nameID); err != nil {
		if err == sql.ErrNoRows {
			return []int{}, nil
		}
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, "SELECT token_id FROM tokens WHERE token=?", selectiveToken).Scan(&tokenID); err != nil {
		if err == sql.ErrNoRows {
			return []int{}, nil
		}
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT entity_seq FROM postings
WHERE token_id=? AND name_id=? AND kind=? AND name_tf>0 AND NOT closed
ORDER BY entity_seq`, tokenID, nameID, kind)
	if err != nil {
		return nil, err
	}
	sequences := []int{}
	for rows.Next() {
		var sequence int
		if err = rows.Scan(&sequence); err != nil {
			rows.Close()
			return nil, err
		}
		sequences = append(sequences, sequence)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	return sequences, nil
}

func (s *Store) contextStreet(ctx context.Context, normalized string, anchor places.Location) (primaryCandidate, bool, error) {
	tokens := strings.Fields(normalized)
	if len(tokens) == 0 {
		return primaryCandidate{}, false, nil
	}
	selectiveToken := tokens[0]
	for _, token := range tokens[1:] {
		if len(token) > len(selectiveToken) {
			selectiveToken = token
		}
	}
	var nameID, tokenID int
	if err := s.db.QueryRowContext(ctx, "SELECT name_id FROM names WHERE normalized_name=?", normalized).Scan(&nameID); err != nil {
		if err == sql.ErrNoRows {
			return primaryCandidate{}, false, nil
		}
		return primaryCandidate{}, false, err
	}
	if err := s.db.QueryRowContext(ctx, "SELECT token_id FROM tokens WHERE token=?", selectiveToken).Scan(&tokenID); err != nil {
		if err == sql.ErrNoRows {
			return primaryCandidate{}, false, nil
		}
		return primaryCandidate{}, false, err
	}
	afterSequence := 0
	closestDistance := math.Inf(1)
	var closest primaryCandidate
	for {
		candidates, err := s.contextStreetCandidatePage(ctx, nameID, tokenID, afterSequence)
		if err != nil {
			return primaryCandidate{}, false, err
		}
		if len(candidates) == 0 {
			break
		}
		lastSequence := candidates[len(candidates)-1].sequence
		candidate, distance, err := s.contextStreetCandidate(ctx, candidates, anchor)
		if err != nil {
			return primaryCandidate{}, false, err
		}
		if distance < closestDistance {
			closest, closestDistance = candidate, distance
		}
		if distance <= contextStreetEarlyAcceptMeters {
			break
		}
		afterSequence = lastSequence
		if len(candidates) < contextStreetCandidatePageSize {
			break
		}
	}
	if closestDistance > contextStreetRadiusMeters {
		return primaryCandidate{}, false, nil
	}
	entity, err := s.Details(ctx, closest.entity.ID)
	if err != nil {
		return primaryCandidate{}, false, err
	}
	closest.entity = entity
	return closest, true, nil
}

func (s *Store) contextStreetCandidatePage(ctx context.Context, nameID, tokenID, afterSequence int) ([]primaryCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT entity_seq FROM postings
WHERE token_id=? AND name_id=? AND kind='street' AND name_tf>0 AND NOT closed AND entity_seq>?
ORDER BY entity_seq LIMIT ?`, tokenID, nameID, afterSequence, contextStreetCandidatePageSize)
	if err != nil {
		return nil, err
	}
	sequences := []int{}
	for rows.Next() {
		var sequence int
		if err = rows.Scan(&sequence); err != nil {
			rows.Close()
			return nil, err
		}
		sequences = append(sequences, sequence)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if len(sequences) == 0 {
		return []primaryCandidate{}, nil
	}
	return s.candidateLocators(ctx, sequences)
}

func (s *Store) candidateLocators(ctx context.Context, sequences []int) ([]primaryCandidate, error) {
	if len(sequences) == 0 {
		return []primaryCandidate{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(sequences)), ",")
	args := make([]any, len(sequences))
	for i, sequence := range sequences {
		// Both schema-2 tables are materialized in stable ID order, so the
		// one-based search sequence is the locator table's zero-based rowid.
		args[i] = sequence - 1
	}
	locatorRows, err := s.db.QueryContext(ctx, `SELECT rowid+1,id,entity_file,entity_row_group,entity_row
FROM entity_locator WHERE rowid IN (`+placeholders+") ORDER BY rowid", args...)
	if err != nil {
		return nil, err
	}
	defer locatorRows.Close()
	candidates := make([]primaryCandidate, 0, len(sequences))
	for locatorRows.Next() {
		var candidate primaryCandidate
		if err = locatorRows.Scan(&candidate.sequence, &candidate.entity.ID, &candidate.entityFile,
			&candidate.entityRowGroup, &candidate.entityRow); err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	if err = locatorRows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) != len(sequences) {
		return nil, fmt.Errorf("candidate locators: got %d want %d", len(candidates), len(sequences))
	}
	return candidates, nil
}

func filterLocalityTypes(candidates []primaryCandidate) []primaryCandidate {
	out := candidates[:0]
	for _, candidate := range candidates {
		if areaSubtypeRank(candidate.entity.Subtype) < 3 {
			out = append(out, candidate)
		}
	}
	return out
}

func filterAreaDescendants(candidates []primaryCandidate, ancestors map[string]bool, parents map[string][]string) []primaryCandidate {
	out := candidates[:0]
	for _, candidate := range candidates {
		pending := []string{candidate.entity.ID}
		seen := map[string]bool{}
		for len(pending) > 0 {
			id := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			if ancestors[id] {
				out = append(out, candidate)
				break
			}
			if seen[id] {
				continue
			}
			seen[id] = true
			pending = append(pending, parents[id]...)
		}
	}
	return out
}

func areaSubtypeRank(subtype string) int {
	switch subtype {
	case "locality":
		return 0
	case "macrohood":
		return 1
	case "neighborhood":
		return 2
	default:
		return 3
	}
}

func candidateEntities(candidates []primaryCandidate, limit int) []places.Entity {
	if len(candidates) < limit {
		limit = len(candidates)
	}
	out := make([]places.Entity, limit)
	for i := range limit {
		out[i] = candidates[i].entity
	}
	return out
}

func (s *Store) loadCandidateLocations(candidates []primaryCandidate) error {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].entityFile != candidates[j].entityFile {
			return candidates[i].entityFile < candidates[j].entityFile
		}
		if candidates[i].entityRowGroup != candidates[j].entityRowGroup {
			return candidates[i].entityRowGroup < candidates[j].entityRowGroup
		}
		return candidates[i].entityRow < candidates[j].entityRow
	})
	var currentFile string
	currentGroup := -1
	var reader *parquet.GenericReader[entityLocationRow]
	defer func() {
		if reader != nil {
			_ = reader.Close()
		}
	}()
	for i := range candidates {
		candidate := &candidates[i]
		file := s.entityParquet[candidate.entityFile]
		if file == nil || candidate.entityRowGroup < 0 || candidate.entityRowGroup >= len(file.RowGroups()) {
			return fmt.Errorf("invalid entity row group for %s", candidate.entity.ID)
		}
		if candidate.entityFile != currentFile || candidate.entityRowGroup != currentGroup {
			if reader != nil {
				if err := reader.Close(); err != nil {
					return err
				}
			}
			reader = parquet.NewGenericRowGroupReader[entityLocationRow](file.RowGroups()[candidate.entityRowGroup])
			currentFile, currentGroup = candidate.entityFile, candidate.entityRowGroup
		}
		if err := reader.SeekToRow(int64(candidate.entityRow)); err != nil {
			return err
		}
		row := []entityLocationRow{{}}
		if n, err := reader.Read(row); n != 1 || err != nil && err != io.EOF {
			return fmt.Errorf("read entity %s: rows=%d: %w", candidate.entity.ID, n, err)
		}
		if row[0].ID != candidate.entity.ID {
			return fmt.Errorf("entity locator mismatch: got %s want %s", row[0].ID, candidate.entity.ID)
		}
		candidate.location = places.Location{Lat: row[0].Lat, Lng: row[0].Lng}
	}
	return nil
}

func (s *Store) contextStreetCandidate(ctx context.Context, candidates []primaryCandidate, anchor places.Location) (primaryCandidate, float64, error) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].entityFile != candidates[j].entityFile {
			return candidates[i].entityFile < candidates[j].entityFile
		}
		if candidates[i].entityRowGroup != candidates[j].entityRowGroup {
			return candidates[i].entityRowGroup < candidates[j].entityRowGroup
		}
		return candidates[i].entityRow < candidates[j].entityRow
	})
	closestDistance := math.Inf(1)
	var closest primaryCandidate
	var currentFile string
	currentGroup := -1
	var reader *parquet.GenericReader[entityLocationRow]
	defer func() {
		if reader != nil {
			_ = reader.Close()
		}
	}()
	for i := range candidates {
		if err := ctx.Err(); err != nil {
			return primaryCandidate{}, math.Inf(1), err
		}
		candidate := &candidates[i]
		file := s.entityParquet[candidate.entityFile]
		if file == nil || candidate.entityRowGroup < 0 || candidate.entityRowGroup >= len(file.RowGroups()) {
			return primaryCandidate{}, math.Inf(1), fmt.Errorf("invalid entity row group for %s", candidate.entity.ID)
		}
		if candidate.entityFile != currentFile || candidate.entityRowGroup != currentGroup {
			if reader != nil {
				if err := reader.Close(); err != nil {
					return primaryCandidate{}, math.Inf(1), err
				}
			}
			reader = parquet.NewGenericRowGroupReader[entityLocationRow](file.RowGroups()[candidate.entityRowGroup])
			currentFile, currentGroup = candidate.entityFile, candidate.entityRowGroup
		}
		if err := reader.SeekToRow(int64(candidate.entityRow)); err != nil {
			return primaryCandidate{}, math.Inf(1), err
		}
		row := []entityLocationRow{{}}
		if n, err := reader.Read(row); n != 1 || err != nil && err != io.EOF {
			return primaryCandidate{}, math.Inf(1), fmt.Errorf("read entity %s location: rows=%d: %w", candidate.entity.ID, n, err)
		}
		if row[0].ID != candidate.entity.ID {
			return primaryCandidate{}, math.Inf(1), fmt.Errorf("entity locator mismatch: got %s want %s", row[0].ID, candidate.entity.ID)
		}
		candidate.location = places.Location{Lat: row[0].Lat, Lng: row[0].Lng}
		distance := geocoding.DistanceMeters(anchor, candidate.location)
		if distance < closestDistance {
			closest, closestDistance = *candidate, distance
		}
		if distance <= contextStreetEarlyAcceptMeters {
			return *candidate, distance, nil
		}
	}
	return closest, closestDistance, nil
}

func (s *Store) autocompletePostings(ctx context.Context, normalized string) ([]places.Entity, error) {
	tokens := strings.Fields(normalized)
	ctes := make([]string, 0, len(tokens)+2)
	args := make([]any, 0, len(tokens)*2+2)
	bm25 := make([]string, 0, len(tokens))
	for i, token := range tokens {
		predicate := "t.token>=?"
		args = append(args, token)
		if upper, ok := prefixUpperBound(token); ok {
			predicate += " AND t.token<?"
			args = append(args, upper)
		} else {
			predicate += " AND starts_with(t.token,?)"
			args = append(args, token)
		}
		metadata := ""
		if i == 0 {
			metadata = ",any_value(p.name_id) AS name_id,any_value(p.kind) AS kind,any_value(p.closed) AS closed,any_value(p.doc_len) AS doc_len"
		}
		ctes = append(ctes, fmt.Sprintf(`q%d AS (
SELECT p.entity_seq,sum(p.name_tf)::INTEGER AS name_tf%d,
       sum(p.alias_tf)::INTEGER AS alias_tf%d,
       sum(p.address_tf)::INTEGER AS address_tf%d,
       count(*) OVER () AS df%d%s
FROM tokens t JOIN postings p USING(token_id)
WHERE %s GROUP BY p.entity_seq)`, i, i, i, i, i, metadata, predicate))
		frequency := fmt.Sprintf("(10.0*name_tf%d+5.0*alias_tf%d+address_tf%d)", i, i, i)
		bm25 = append(bm25, fmt.Sprintf(
			"greatest(ln((s.n_docs-df%d+0.5)/(df%d+0.5)),0.000001)*((%s*2.2)/(%s+1.2*(0.25+0.75*q0.doc_len/s.avg_doc_len)))",
			i, i, frequency, frequency))
	}
	joins := make([]string, 0, len(tokens)-1)
	for i := 1; i < len(tokens); i++ {
		joins = append(joins, fmt.Sprintf("JOIN q%d USING(entity_seq)", i))
	}
	namePredicate := "normalized_name>=?"
	args = append(args, normalized)
	if upper, ok := prefixUpperBound(normalized); ok {
		namePredicate += " AND normalized_name<?"
		args = append(args, upper)
	} else {
		namePredicate += " AND starts_with(normalized_name,?)"
		args = append(args, normalized)
	}
	ctes = append(ctes, "name_matches AS (SELECT name_id,normalized_name FROM names WHERE "+namePredicate+")")
	order := "exact_rank,name_prefix_rank,kind_rank,bm25_score DESC,entity_seq"
	query := fmt.Sprintf(`WITH %s,
scored AS (
  SELECT q0.*,
    CASE WHEN n.normalized_name=? THEN 0 ELSE 1 END AS exact_rank,
    CASE WHEN n.name_id IS NOT NULL THEN 0 ELSE 1 END AS name_prefix_rank,
    CASE q0.kind WHEN 'area' THEN 0 WHEN 'street' THEN 1 WHEN 'business' THEN 2 ELSE 3 END AS kind_rank,
    %s AS bm25_score
  FROM q0 %s LEFT JOIN name_matches n USING(name_id)
  CROSS JOIN corpus_stats s WHERE NOT q0.closed
), deduplicated AS (
  SELECT *,row_number() OVER (
    PARTITION BY (kind='street'),CASE WHEN kind='street' THEN name_id ELSE entity_seq END
    ORDER BY %s) AS street_rank
  FROM scored
)
SELECT entity_seq FROM deduplicated
WHERE street_rank=1 ORDER BY %s LIMIT 5`,
		strings.Join(ctes, ","), strings.Join(bm25, "+"), strings.Join(joins, " "), order, order)
	args = append(args, normalized)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	type ranked struct {
		seq int
	}
	rankedRows := []ranked{}
	for rows.Next() {
		var row ranked
		if err = rows.Scan(&row.seq); err != nil {
			rows.Close()
			return nil, err
		}
		rankedRows = append(rankedRows, row)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if len(rankedRows) == 0 {
		return []places.Entity{}, nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(rankedRows)), ",")
	lookupArgs := make([]any, len(rankedRows))
	for i, row := range rankedRows {
		lookupArgs[i] = row.seq
	}
	detailRows, err := s.db.QueryContext(ctx, `SELECT entity_seq,id,kind,name,address,subtype
FROM search_entities WHERE entity_seq IN (`+placeholders+")", lookupArgs...)
	if err != nil {
		return nil, err
	}
	defer detailRows.Close()
	bySequence := map[int]places.Entity{}
	for detailRows.Next() {
		var seq int
		var entity places.Entity
		if err = detailRows.Scan(&seq, &entity.ID, &entity.Kind, &entity.Name, &entity.Address, &entity.Subtype); err != nil {
			return nil, err
		}
		canonicalizeEntity(&entity)
		bySequence[seq] = entity
	}
	if err = detailRows.Err(); err != nil {
		return nil, err
	}
	out := make([]places.Entity, 0, len(rankedRows))
	for _, row := range rankedRows {
		entity, ok := bySequence[row.seq]
		if !ok {
			return nil, fmt.Errorf("search entity missing for sequence %d", row.seq)
		}
		out = append(out, entity)
	}
	return out, nil
}
