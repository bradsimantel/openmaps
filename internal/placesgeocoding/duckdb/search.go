package duckdb

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

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
			out = append(out, entity)
		}
		return out, rows.Err()
	}
	return s.autocompletePostings(ctx, normalized)
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
