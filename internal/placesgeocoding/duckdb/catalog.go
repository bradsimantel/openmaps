package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// catalogPartitionRows is the maximum number of input rows admitted to a
// catalog sort, grouping, or window partition. The national normalization gate
// already qualified this cardinality under the production memory controls.
const catalogPartitionRows int64 = 4_000_000

type catalogPhaseObserver func(string, time.Duration)

type catalogBuilder struct {
	ctx          context.Context
	db           *sql.DB
	entitiesPath string
	entityCount  int
	observe      catalogPhaseObserver
}

func (b *catalogBuilder) phase(name string, work func() error) error {
	started := time.Now()
	profilePath := ""
	if profileDir := os.Getenv("OPENMAPS_DUCKDB_PROFILE_DIR"); profileDir != "" {
		if err := os.MkdirAll(profileDir, 0700); err != nil {
			return fmt.Errorf("catalog phase %s profile directory: %w", name, err)
		}
		profilePath = filepath.Join(profileDir, name+".json")
		if _, err := b.db.ExecContext(b.ctx, "SET enable_profiling='json'; SET profiling_coverage='ALL'; SET profiling_mode='detailed'; SET profiling_output="+sqlString(profilePath)); err != nil {
			return fmt.Errorf("catalog phase %s profiling: %w", name, err)
		}
	}
	if err := work(); err != nil {
		return fmt.Errorf("catalog phase %s: %w", name, err)
	}
	if profilePath != "" {
		if _, err := b.db.ExecContext(b.ctx, "PRAGMA disable_profiling"); err != nil {
			return fmt.Errorf("catalog phase %s disable profiling: %w", name, err)
		}
	}
	if b.observe != nil {
		b.observe("catalog_"+name, time.Since(started))
	}
	return nil
}

func (b *catalogBuilder) exec(query string, args ...any) error {
	_, err := b.db.ExecContext(b.ctx, query, args...)
	return err
}

func (b *catalogBuilder) build(sourceManifest, identitiesJSON string) error {
	if err := b.phase("metadata", func() error {
		if err := b.exec("CREATE TABLE metadata(key VARCHAR, value VARCHAR NOT NULL)"); err != nil {
			return err
		}
		_, err := b.db.ExecContext(b.ctx, "INSERT INTO metadata VALUES ('schema_version','2'),('source_manifest',?),('identities',?)", sourceManifest, identitiesJSON)
		return err
	}); err != nil {
		return err
	}
	if err := b.buildEntities(); err != nil {
		return err
	}
	if err := b.buildNamesAndRanks(); err != nil {
		return err
	}
	if err := b.buildTokens(); err != nil {
		return err
	}
	if err := b.buildPostingsAndPrefixes(); err != nil {
		return err
	}
	if err := b.buildAddresses(); err != nil {
		return err
	}
	if err := b.validateAndAttest(); err != nil {
		return err
	}
	return b.phase("checkpoint", func() error { return b.exec("CHECKPOINT") })
}

func (b *catalogBuilder) validateAndAttest() error {
	return b.phase("bounded_validation", func() error {
		var locatorRows, searchRows int64
		if err := b.db.QueryRowContext(b.ctx, "SELECT count(*) FROM entity_locator").Scan(&locatorRows); err != nil {
			return err
		}
		if err := b.db.QueryRowContext(b.ctx, "SELECT count(*) FROM search_entities").Scan(&searchRows); err != nil {
			return err
		}
		if locatorRows != int64(b.entityCount) || searchRows != int64(b.entityCount) {
			return fmt.Errorf("entity coverage locator=%d search=%d want=%d", locatorRows, searchRows, b.entityCount)
		}
		for _, prefix := range hexadecimalPrefixes(2) {
			lower, upper := "om_"+prefix, prefixUpperASCII("om_"+prefix)
			var duplicateLocators, duplicateSearch, mismatched int64
			if err := b.db.QueryRowContext(b.ctx, "SELECT count(*)-count(DISTINCT id) FROM entity_locator WHERE id>=? AND id<?", lower, upper).Scan(&duplicateLocators); err != nil {
				return err
			}
			if err := b.db.QueryRowContext(b.ctx, "SELECT count(*)-count(DISTINCT id) FROM search_entities WHERE id>=? AND id<?", lower, upper).Scan(&duplicateSearch); err != nil {
				return err
			}
			if err := b.db.QueryRowContext(b.ctx, `SELECT count(*) FROM
(SELECT id,kind FROM search_entities WHERE id>=? AND id<?) s
FULL OUTER JOIN (SELECT id,kind FROM entity_locator WHERE id>=? AND id<?) l USING(id)
WHERE s.id IS NULL OR l.id IS NULL OR s.kind<>l.kind`, lower, upper, lower, upper).Scan(&mismatched); err != nil {
				return err
			}
			if duplicateLocators != 0 || duplicateSearch != 0 || mismatched != 0 {
				return fmt.Errorf("entity prefix %s integrity locator_duplicates=%d search_duplicates=%d mismatched=%d", prefix, duplicateLocators, duplicateSearch, mismatched)
			}
		}
		var tokenCount, tokenMin, tokenMax, badPostings, badPrefixes int64
		if err := b.db.QueryRowContext(b.ctx, "SELECT count(*),coalesce(min(token_id),0),coalesce(max(token_id),0) FROM tokens").Scan(&tokenCount, &tokenMin, &tokenMax); err != nil {
			return err
		}
		if tokenCount != tokenMax || tokenCount > 0 && tokenMin != 1 {
			return fmt.Errorf("token IDs are not contiguous: count=%d min=%d max=%d", tokenCount, tokenMin, tokenMax)
		}
		if err := b.db.QueryRowContext(b.ctx, "SELECT count(*) FROM postings WHERE token_id<1 OR token_id>? OR entity_seq<1 OR entity_seq>?", tokenMax, b.entityCount).Scan(&badPostings); err != nil {
			return err
		}
		if err := b.db.QueryRowContext(b.ctx, "SELECT count(*) FROM short_prefix_head WHERE rank>4 OR entity_seq<1 OR entity_seq>?", b.entityCount).Scan(&badPrefixes); err != nil {
			return err
		}
		if badPostings != 0 || badPrefixes != 0 {
			return fmt.Errorf("search integrity postings=%d prefixes=%d", badPostings, badPrefixes)
		}
		var excessPrefixes int64
		if err := b.db.QueryRowContext(b.ctx, "SELECT count(*) FROM (SELECT prefix FROM short_prefix_head GROUP BY prefix HAVING count(*)>5)").Scan(&excessPrefixes); err != nil {
			return err
		}
		if excessPrefixes != 0 {
			return fmt.Errorf("prefix result limit exceeded: %d", excessPrefixes)
		}
		var addresses, spatial int64
		if err := b.db.QueryRowContext(b.ctx, "SELECT count(*) FROM address_lookup").Scan(&addresses); err != nil {
			return err
		}
		if err := b.db.QueryRowContext(b.ctx, "SELECT count(*) FROM address_spatial").Scan(&spatial); err != nil {
			return err
		}
		if addresses != spatial {
			return fmt.Errorf("address coverage lookup=%d spatial=%d", addresses, spatial)
		}
		return b.exec("INSERT INTO metadata VALUES ('bounded_validation_version','1')")
	})
}

func (b *catalogBuilder) buildEntities() error {
	if err := b.phase("entity_locator", func() error {
		if err := b.exec(`CREATE TABLE entity_locator(
id VARCHAR,kind VARCHAR,entity_file VARCHAR,entity_row_group BIGINT,entity_row BIGINT,
source_file VARCHAR,source_start BIGINT,source_count BIGINT,
provenance_file VARCHAR,provenance_start BIGINT,provenance_count BIGINT)`); err != nil {
			return err
		}
		for _, prefix := range hexadecimalPrefixes(2) {
			lower, upper := "om_"+prefix, prefixUpperASCII("om_"+prefix)
			if err := b.exec(`INSERT INTO entity_locator
SELECT id,kind,regexp_extract(filename,'[^/\\\\]+$'),entity_row_group,entity_row,
       source_file,source_start,source_count,provenance_file,provenance_start,provenance_count
FROM read_parquet(?,filename=true) WHERE id>=? AND id<? ORDER BY id`, b.entitiesPath, lower, upper); err != nil {
				return fmt.Errorf("id prefix %s: %w", prefix, err)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return b.phase("search_entities", func() error {
		if err := b.exec(`CREATE TABLE search_entities(
entity_seq INTEGER,id VARCHAR,kind VARCHAR,name VARCHAR,normalized_name VARCHAR,address VARCHAR,
normalized_address VARCHAR,normalized_aliases VARCHAR,subtype VARCHAR,closed BOOLEAN)`); err != nil {
			return err
		}
		var offset int64
		for _, prefix := range hexadecimalPrefixes(2) {
			lower, upper := "om_"+prefix, prefixUpperASCII("om_"+prefix)
			var count int64
			if err := b.db.QueryRowContext(b.ctx, "SELECT count(*) FROM read_parquet(?) WHERE id>=? AND id<?", b.entitiesPath, lower, upper).Scan(&count); err != nil {
				return err
			}
			if count == 0 {
				continue
			}
			if err := b.exec(`INSERT INTO search_entities
SELECT (?+row_number() OVER (ORDER BY id))::INTEGER,id,kind,name,normalized_name,address,
       normalized_address,normalized_aliases,subtype,closed
FROM read_parquet(?) WHERE id>=? AND id<? ORDER BY id`, offset, b.entitiesPath, lower, upper); err != nil {
				return fmt.Errorf("id prefix %s: %w", prefix, err)
			}
			offset += count
		}
		if offset != int64(b.entityCount) {
			return fmt.Errorf("search entity rows=%d want %d", offset, b.entityCount)
		}
		return nil
	})
}

func (b *catalogBuilder) buildNamesAndRanks() error {
	if err := b.phase("names", func() error {
		if err := b.exec("CREATE TABLE names(name_id INTEGER,normalized_name VARCHAR)"); err != nil {
			return err
		}
		partitions, err := planStringPartitions(b.ctx, b.db, "search_entities", "normalized_name", catalogPartitionRows, true)
		if err != nil {
			return err
		}
		var offset int64
		for _, partition := range partitions {
			predicate := "starts_with(normalized_name,?)"
			if partition.exact {
				predicate = "normalized_name=?"
			}
			var count int64
			if err = b.db.QueryRowContext(b.ctx, "SELECT count(DISTINCT normalized_name) FROM search_entities WHERE "+predicate, partition.prefix).Scan(&count); err != nil {
				return err
			}
			if err = b.exec(`INSERT INTO names
SELECT (?+row_number() OVER (ORDER BY normalized_name))::INTEGER,normalized_name
FROM (SELECT DISTINCT normalized_name FROM search_entities WHERE `+predicate+` ORDER BY normalized_name)`, offset, partition.prefix); err != nil {
				return err
			}
			offset += count
		}
		return nil
	}); err != nil {
		return err
	}
	if err := b.phase("entity_ranks", func() error {
		if err := b.exec(`CREATE TEMP TABLE entity_ranks_stage(
entity_seq INTEGER,name_id INTEGER,kind VARCHAR,closed BOOLEAN,doc_len USMALLINT)`); err != nil {
			return err
		}
		partitions, err := planStringPartitions(b.ctx, b.db, "search_entities", "normalized_name", catalogPartitionRows, true)
		if err != nil {
			return err
		}
		for _, partition := range partitions {
			predicate := "starts_with(e.normalized_name,?)"
			if partition.exact {
				predicate = "e.normalized_name=?"
			}
			if err = b.exec(`INSERT INTO entity_ranks_stage
SELECT e.entity_seq,n.name_id,e.kind,e.closed,
       (CASE WHEN e.normalized_name='' THEN 0 ELSE len(string_split(e.normalized_name,' ')) END +
        CASE WHEN e.normalized_aliases='' THEN 0 ELSE len(string_split(e.normalized_aliases,' ')) END +
        CASE WHEN e.normalized_address='' THEN 0 ELSE len(string_split(e.normalized_address,' ')) END)::USMALLINT
FROM search_entities e JOIN names n USING(normalized_name) WHERE `+predicate, partition.prefix); err != nil {
				return err
			}
		}
		if err = b.exec(`CREATE TEMP TABLE entity_ranks(
entity_seq INTEGER,name_id INTEGER,kind VARCHAR,closed BOOLEAN,doc_len USMALLINT)`); err != nil {
			return err
		}
		for start := int64(1); start <= int64(b.entityCount); start += catalogPartitionRows {
			end := min(start+catalogPartitionRows-1, int64(b.entityCount))
			if err = b.exec("INSERT INTO entity_ranks SELECT * FROM entity_ranks_stage WHERE entity_seq BETWEEN ? AND ? ORDER BY entity_seq", start, end); err != nil {
				return err
			}
		}
		if err = b.exec("DROP TABLE entity_ranks_stage"); err != nil {
			return err
		}
		return b.exec(`CREATE TABLE corpus_stats AS
SELECT count(*)::BIGINT AS n_docs,avg(doc_len)::DOUBLE AS avg_doc_len FROM entity_ranks`)
	}); err != nil {
		return err
	}
	return nil
}

func (b *catalogBuilder) buildTokens() error {
	if err := b.phase("token_candidates", func() error {
		if err := b.exec("CREATE TEMP TABLE token_candidates(token VARCHAR)"); err != nil {
			return err
		}
		for start := 1; start <= b.entityCount; start += postingChunk {
			end := min(start+postingChunk-1, b.entityCount)
			if err := b.exec(`INSERT INTO token_candidates
WITH fields AS (
 SELECT unnest(string_split(normalized_name,' ')) token FROM search_entities WHERE entity_seq BETWEEN ? AND ?
 UNION ALL SELECT unnest(string_split(normalized_aliases,' ')) FROM search_entities WHERE entity_seq BETWEEN ? AND ? AND normalized_aliases<>''
 UNION ALL SELECT unnest(string_split(normalized_address,' ')) FROM search_entities WHERE entity_seq BETWEEN ? AND ? AND normalized_address<>''
)
SELECT DISTINCT token FROM fields WHERE length(token)>=1`, start, end, start, end, start, end); err != nil {
				return fmt.Errorf("entities %d-%d: %w", start, end, err)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	return b.phase("tokens", func() error {
		if err := b.exec("CREATE TABLE tokens(token_id INTEGER,token VARCHAR)"); err != nil {
			return err
		}
		partitions, err := planStringPartitions(b.ctx, b.db, "token_candidates", "token", catalogPartitionRows, true)
		if err != nil {
			return err
		}
		var offset int64
		for _, partition := range partitions {
			predicate := "starts_with(token,?)"
			if partition.exact {
				predicate = "token=?"
			}
			var count int64
			if err = b.db.QueryRowContext(b.ctx, "SELECT count(DISTINCT token) FROM token_candidates WHERE "+predicate, partition.prefix).Scan(&count); err != nil {
				return err
			}
			if err = b.exec(`INSERT INTO tokens
SELECT (?+row_number() OVER (ORDER BY token))::INTEGER,token
FROM (SELECT DISTINCT token FROM token_candidates WHERE `+predicate+` ORDER BY token)`, offset, partition.prefix); err != nil {
				return err
			}
			offset += count
		}
		return b.exec("DROP TABLE token_candidates")
	})
}

func (b *catalogBuilder) buildPostingsAndPrefixes() error {
	if err := b.phase("posting_generation", func() error {
		if err := b.exec(`CREATE TEMP TABLE postings_stage(
token_id INTEGER,entity_seq INTEGER,name_tf USMALLINT,alias_tf USMALLINT,address_tf USMALLINT)`); err != nil {
			return err
		}
		if err := b.exec(`CREATE TEMP TABLE prefix_candidates(
prefix VARCHAR,entity_seq INTEGER,name_id INTEGER,kind VARCHAR,closed BOOLEAN,doc_len USMALLINT,
name_tf INTEGER,alias_tf INTEGER,address_tf INTEGER,exact_rank INTEGER,name_prefix_rank INTEGER,kind_rank INTEGER,score DOUBLE)`); err != nil {
			return err
		}
		statement, err := b.db.PrepareContext(b.ctx, postingsSQL)
		if err != nil {
			return err
		}
		defer statement.Close()
		for start := 1; start <= b.entityCount; start += postingChunk {
			end := min(start+postingChunk-1, b.entityCount)
			if _, err = statement.ExecContext(b.ctx, start, end, start, end, start, end); err != nil {
				return fmt.Errorf("entities %d-%d: %w", start, end, err)
			}
			var covered int64
			if err = b.db.QueryRowContext(b.ctx, "SELECT count(DISTINCT entity_seq) FROM postings_stage WHERE entity_seq BETWEEN ? AND ?", start, end).Scan(&covered); err != nil {
				return err
			}
			if covered != int64(end-start+1) {
				return fmt.Errorf("posting coverage entities %d-%d: got %d", start, end, covered)
			}
			if err = b.exec(prefixCandidateSQL, start, end, start, end); err != nil {
				return fmt.Errorf("prefix candidates entities %d-%d: %w", start, end, err)
			}
		}
		return statement.Close()
	}); err != nil {
		return err
	}
	if err := b.phase("posting_sort", func() error {
		if err := b.exec(`CREATE TABLE postings(
token_id INTEGER,entity_seq INTEGER,name_id INTEGER,kind VARCHAR,closed BOOLEAN,doc_len USMALLINT,
name_tf USMALLINT,alias_tf USMALLINT,address_tf USMALLINT)`); err != nil {
			return err
		}
		partitions, err := planPostingPartitions(b.ctx, b.db, catalogPartitionRows)
		if err != nil {
			return err
		}
		for _, p := range partitions {
			query := `INSERT INTO postings
SELECT p.token_id,p.entity_seq,r.name_id,r.kind,r.closed,r.doc_len,p.name_tf,p.alias_tf,p.address_tf
FROM postings_stage p JOIN entity_ranks r USING(entity_seq)
WHERE p.token_id BETWEEN ? AND ?`
			args := []any{p.tokenLow, p.tokenHigh}
			if p.entityLow != 0 {
				query += " AND p.entity_seq BETWEEN ? AND ?"
				args = append(args, p.entityLow, p.entityHigh)
			}
			query += " ORDER BY p.token_id,p.entity_seq"
			if err = b.exec(query, args...); err != nil {
				return err
			}
		}
		return b.exec("DROP TABLE postings_stage")
	}); err != nil {
		return err
	}
	return b.phase("short_prefix_head", func() error {
		if err := b.exec("CREATE TABLE short_prefix_head(prefix VARCHAR,rank UTINYINT,entity_seq INTEGER)"); err != nil {
			return err
		}
		partitions, err := planStringPartitions(b.ctx, b.db, "prefix_candidates", "prefix", catalogPartitionRows, true)
		if err != nil {
			return err
		}
		for _, partition := range partitions {
			predicate := "starts_with(prefix,?)"
			if partition.exact {
				predicate = "prefix=?"
			}
			rows, queryErr := b.db.QueryContext(b.ctx, "SELECT DISTINCT prefix FROM prefix_candidates WHERE "+predicate+" ORDER BY prefix", partition.prefix)
			if queryErr != nil {
				return queryErr
			}
			var prefixes []string
			for rows.Next() {
				var prefix string
				if queryErr = rows.Scan(&prefix); queryErr != nil {
					rows.Close()
					return queryErr
				}
				prefixes = append(prefixes, prefix)
			}
			if queryErr = rows.Close(); queryErr != nil {
				return queryErr
			}
			for _, prefix := range prefixes {
				if err = b.exec(finalPrefixSQL, prefix); err != nil {
					return fmt.Errorf("prefix %q: %w", prefix, err)
				}
			}
		}
		return b.exec("DROP TABLE prefix_candidates")
	})
}

func (b *catalogBuilder) buildAddresses() error {
	if err := b.phase("address_stage", func() error {
		if err := b.exec(`CREATE TEMP TABLE address_stage(
entity_seq INTEGER,entity_id VARCHAR,address_key VARCHAR,context VARCHAR,lat DOUBLE,lng DOUBLE)`); err != nil {
			return err
		}
		for _, prefix := range hexadecimalPrefixes(2) {
			lower, upper := "om_"+prefix, prefixUpperASCII("om_"+prefix)
			if err := b.exec(`INSERT INTO address_stage
SELECT s.entity_seq,l.id,e.address_key,e.address_context,e.lat,e.lng
FROM read_parquet(?) e JOIN entity_locator l USING(id) JOIN search_entities s USING(id)
WHERE e.id>=? AND e.id<? AND l.id>=? AND l.id<? AND s.id>=? AND s.id<?
  AND l.kind='address' AND e.address_key<>''`, b.entitiesPath, lower, upper, lower, upper, lower, upper); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := b.phase("address_lookup", func() error {
		if err := b.exec(`CREATE TABLE address_lookup(
entity_seq INTEGER,entity_id VARCHAR,address_key VARCHAR,context VARCHAR,lat DOUBLE,lng DOUBLE)`); err != nil {
			return err
		}
		partitions, err := planStringPartitions(b.ctx, b.db, "address_stage", "address_key", catalogPartitionRows, false)
		if err != nil {
			return err
		}
		for _, partition := range partitions {
			predicate := "starts_with(address_key,?)"
			if partition.exact {
				predicate = "address_key=?"
			}
			if err = b.exec("INSERT INTO address_lookup SELECT * FROM address_stage WHERE "+predicate+" ORDER BY address_key,entity_id", partition.prefix); err != nil {
				return err
			}
		}
		return b.exec("DROP TABLE address_stage")
	}); err != nil {
		return err
	}
	return b.phase("address_spatial", func() error {
		if err := b.exec(`CREATE TEMP TABLE address_spatial_stage AS
SELECT *,floor((lat+90.0)*1000.0)::BIGINT AS lat_cell,
       floor((lng+180.0)*1000.0)::BIGINT AS lng_cell,
       (lat_cell*360001+lng_cell) AS cell_id FROM address_lookup`); err != nil {
			return err
		}
		if err := b.exec(`CREATE TABLE address_spatial(
entity_seq INTEGER,entity_id VARCHAR,address_key VARCHAR,context VARCHAR,lat DOUBLE,lng DOUBLE,
lat_cell BIGINT,lng_cell BIGINT,cell_id BIGINT)`); err != nil {
			return err
		}
		partitions, err := planIntegerPartitions(b.ctx, b.db, "address_spatial_stage", "cell_id", catalogPartitionRows)
		if err != nil {
			return err
		}
		for _, p := range partitions {
			if err = b.exec("INSERT INTO address_spatial SELECT * FROM address_spatial_stage WHERE cell_id BETWEEN ? AND ? ORDER BY cell_id,entity_id", p.low, p.high); err != nil {
				return err
			}
		}
		return b.exec("DROP TABLE address_spatial_stage")
	})
}

const prefixCandidateSQL = `INSERT INTO prefix_candidates
WITH prefix_postings AS (
 SELECT left(t.token,1) prefix,p.* FROM tokens t JOIN postings_stage p USING(token_id)
 WHERE p.entity_seq BETWEEN ? AND ?
 UNION ALL
 SELECT left(t.token,2) prefix,p.* FROM tokens t JOIN postings_stage p USING(token_id)
 WHERE p.entity_seq BETWEEN ? AND ? AND length(t.token)>=2
), matches AS (
 SELECT prefix,entity_seq,any_value(r.name_id) name_id,any_value(r.kind) kind,
        any_value(r.closed) closed,any_value(r.doc_len) doc_len,
        sum(name_tf)::INTEGER name_tf,sum(alias_tf)::INTEGER alias_tf,sum(address_tf)::INTEGER address_tf
 FROM prefix_postings JOIN entity_ranks r USING(entity_seq) GROUP BY prefix,entity_seq
), scored AS (
 SELECT m.*,CASE WHEN n.normalized_name=m.prefix THEN 0 ELSE 1 END exact_rank,
        CASE WHEN starts_with(n.normalized_name,m.prefix) THEN 0 ELSE 1 END name_prefix_rank,
        CASE m.kind WHEN 'area' THEN 0 WHEN 'street' THEN 1 WHEN 'business' THEN 2 ELSE 3 END kind_rank,
        (((10.0*m.name_tf+5.0*m.alias_tf+m.address_tf)*2.2)/
         ((10.0*m.name_tf+5.0*m.alias_tf+m.address_tf)+1.2*(0.25+0.75*m.doc_len/s.avg_doc_len))) score
 FROM matches m JOIN names n USING(name_id) CROSS JOIN corpus_stats s WHERE NOT m.closed
), street_collapsed AS (
 SELECT *,row_number() OVER (PARTITION BY prefix,(kind='street'),CASE WHEN kind='street' THEN name_id ELSE entity_seq END
 ORDER BY exact_rank,name_prefix_rank,kind_rank,score DESC,entity_seq) street_rank FROM scored
), ranked AS (
 SELECT *,row_number() OVER (PARTITION BY prefix ORDER BY exact_rank,name_prefix_rank,kind_rank,score DESC,entity_seq) result_rank
 FROM street_collapsed WHERE street_rank=1
)
SELECT prefix,entity_seq,name_id,kind,closed,doc_len,name_tf,alias_tf,address_tf,
       exact_rank,name_prefix_rank,kind_rank,score FROM ranked WHERE result_rank<=5`

const finalPrefixSQL = `INSERT INTO short_prefix_head
WITH street_collapsed AS (
 SELECT *,row_number() OVER (PARTITION BY (kind='street'),CASE WHEN kind='street' THEN name_id ELSE entity_seq END
 ORDER BY exact_rank,name_prefix_rank,kind_rank,score DESC,entity_seq) street_rank
 FROM prefix_candidates WHERE prefix=?
), ranked AS (
 SELECT *,row_number() OVER (ORDER BY exact_rank,name_prefix_rank,kind_rank,score DESC,entity_seq)-1 result_rank
 FROM street_collapsed WHERE street_rank=1
)
SELECT prefix,result_rank::UTINYINT,entity_seq FROM ranked WHERE result_rank<5 ORDER BY prefix,result_rank`

func hexadecimalPrefixes(width int) []string {
	prefixes := []string{""}
	for range width {
		var next []string
		for _, prefix := range prefixes {
			for _, digit := range normalizationEntityBuckets {
				next = append(next, prefix+string(digit))
			}
		}
		prefixes = next
	}
	return prefixes
}

func prefixUpperASCII(prefix string) string {
	b := []byte(prefix)
	b[len(b)-1]++
	return string(b)
}

type stringPartition struct {
	prefix string
	exact  bool
	rows   int64
}

var catalogStringColumns = map[string]map[string]bool{
	"search_entities":   {"normalized_name": true},
	"token_candidates":  {"token": true},
	"prefix_candidates": {"prefix": true},
	"address_stage":     {"address_key": true},
}

func planStringPartitions(ctx context.Context, db *sql.DB, table, column string, maxRows int64, allowOversizedExact bool) ([]stringPartition, error) {
	if maxRows < 1 || !catalogStringColumns[table][column] {
		return nil, fmt.Errorf("invalid catalog string partition request")
	}
	type count struct {
		prefix string
		rows   int64
	}
	var out []stringPartition
	var visit func(string, int) error
	visit = func(prefix string, depth int) error {
		nextDepth := depth + 1
		query := fmt.Sprintf("SELECT substr(%s,1,?),count(*) FROM %s", column, table)
		args := []any{nextDepth}
		if prefix != "" {
			query += fmt.Sprintf(" WHERE starts_with(%s,?)", column)
			args = append(args, prefix)
		}
		query += " GROUP BY 1 ORDER BY 1"
		rows, err := db.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		var children []count
		for rows.Next() {
			var child count
			if err = rows.Scan(&child.prefix, &child.rows); err != nil {
				rows.Close()
				return err
			}
			children = append(children, child)
		}
		if err = rows.Close(); err != nil {
			return err
		}
		for _, child := range children {
			exact := utf8.RuneCountInString(child.prefix) < nextDepth
			if child.rows <= maxRows || exact && allowOversizedExact {
				out = append(out, stringPartition{child.prefix, exact, child.rows})
			} else if exact {
				return fmt.Errorf("exact %s value %q has %d rows above partition limit %d", column, child.prefix, child.rows, maxRows)
			} else if err = visit(child.prefix, nextDepth); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit("", 0); err != nil {
		return nil, err
	}
	return out, nil
}

type integerPartition struct{ low, high int64 }

func planIntegerPartitions(ctx context.Context, db *sql.DB, table, column string, maxRows int64) ([]integerPartition, error) {
	if table != "address_spatial_stage" || column != "cell_id" || maxRows < 1 {
		return nil, fmt.Errorf("invalid integer partition request")
	}
	var low, high sql.NullInt64
	if err := db.QueryRowContext(ctx, fmt.Sprintf("SELECT min(%s),max(%s) FROM %s", column, column, table)).Scan(&low, &high); err != nil {
		return nil, err
	}
	if !low.Valid {
		return nil, nil
	}
	var out []integerPartition
	var visit func(int64, int64) error
	visit = func(lo, hi int64) error {
		var count int64
		if err := db.QueryRowContext(ctx, fmt.Sprintf("SELECT count(*) FROM %s WHERE %s BETWEEN ? AND ?", table, column), lo, hi).Scan(&count); err != nil {
			return err
		}
		if count <= maxRows {
			out = append(out, integerPartition{lo, hi})
			return nil
		}
		if lo == hi {
			return fmt.Errorf("cell %d has %d rows above partition limit %d", lo, count, maxRows)
		}
		mid := lo + (hi-lo)/2
		if err := visit(lo, mid); err != nil {
			return err
		}
		return visit(mid+1, hi)
	}
	if err := visit(low.Int64, high.Int64); err != nil {
		return nil, err
	}
	return out, nil
}

type postingPartition struct{ tokenLow, tokenHigh, entityLow, entityHigh int64 }

func planPostingPartitions(ctx context.Context, db *sql.DB, maxRows int64) ([]postingPartition, error) {
	var low, high sql.NullInt64
	if err := db.QueryRowContext(ctx, "SELECT min(token_id),max(token_id) FROM postings_stage").Scan(&low, &high); err != nil {
		return nil, err
	}
	if !low.Valid {
		return nil, nil
	}
	var out []postingPartition
	var visit func(int64, int64) error
	visit = func(lo, hi int64) error {
		var count int64
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM postings_stage WHERE token_id BETWEEN ? AND ?", lo, hi).Scan(&count); err != nil {
			return err
		}
		if count <= maxRows {
			out = append(out, postingPartition{tokenLow: lo, tokenHigh: hi})
			return nil
		}
		if lo < hi {
			mid := lo + (hi-lo)/2
			if err := visit(lo, mid); err != nil {
				return err
			}
			return visit(mid+1, hi)
		}
		var entityLow, entityHigh sql.NullInt64
		if err := db.QueryRowContext(ctx, "SELECT min(entity_seq),max(entity_seq) FROM postings_stage WHERE token_id=?", lo).Scan(&entityLow, &entityHigh); err != nil {
			return err
		}
		for start := entityLow.Int64; start <= entityHigh.Int64; start += maxRows {
			out = append(out, postingPartition{lo, hi, start, min(start+maxRows-1, entityHigh.Int64)})
		}
		return nil
	}
	if err := visit(low.Int64, high.Int64); err != nil {
		return nil, err
	}
	return out, nil
}

func catalogSpillPath(databasePath string) string {
	return filepath.Join(filepath.Dir(databasePath), "duckdb-spill")
}

func catalogTableNames() string {
	return strings.Join([]string{"metadata", "entity_locator", "search_entities", "names", "corpus_stats", "tokens", "postings", "short_prefix_head", "address_lookup", "address_spatial"}, ",")
}
