// Package duckdb builds, verifies, selects and reads production Places and
// geocoding generations: normalized immutable Parquet plus a derived DuckDB
// serving catalog.
package duckdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/duckdb/duckdb-go/v2"
	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"

	"openmaps/internal/importer"
	"openmaps/internal/places"
)

//go:embed schema.sql
var schema string

//go:embed schema-sharded.sql
var shardedSchema string

//go:embed postings.sql
var postingsSQL string

//go:embed prefixes.sql
var prefixesSQL string

const (
	ManifestName       = "manifest.json"
	IndexName          = "serving.duckdb"
	EntitiesName       = "entities.parquet"
	SourcesName        = "source-records.parquet"
	ProvenanceName     = "attribute-provenance.parquet"
	RelationshipsName  = "relationships.parquet"
	RejectionsName     = "rejections.parquet"
	MetadataName       = "metadata.parquet"
	maxRowsPerRowGroup = 32768
	postingChunk       = 32768
)

type File struct {
	Name   string `json:"name"`
	Role   string `json:"role"`
	SHA256 string `json:"sha256"`
	Rows   int    `json:"rows"`
}

type Manifest struct {
	Schema           int    `json:"schema"`
	CoordinateOrder  string `json:"coordinate_order"`
	NormalizedSHA256 string `json:"normalized_sha256"`
	Files            []File `json:"files"`
}

// Build constructs a generation from a decoded bundle for deterministic tests.
// Production commands use BuildJSON, whose decoder and resolver are streaming.
func Build(ctx context.Context, path string, bundle importer.Bundle) error {
	var input bytes.Buffer
	if err := json.NewEncoder(&input).Encode(bundle); err != nil {
		return err
	}
	return BuildJSON(ctx, path, &input)
}

func encode(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func writeParquet[T any](path string, rows []T) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	w := parquet.NewGenericWriter[T](f,
		parquet.Compression(&zstd.Codec{Level: zstd.SpeedDefault}),
		parquet.MaxRowsPerRowGroup(maxRowsPerRowGroup),
	)
	if _, err = w.Write(rows); err == nil {
		err = w.Close()
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

func sqlString(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func buildIndex(ctx context.Context, path, entitiesPath string, entityCount int, sourceManifest json.RawMessage, identities map[string]string) error {
	return buildIndexWithMemoryLimit(ctx, path, entitiesPath, entityCount, sourceManifest, identities, "256MB")
}

func buildIndexWithMemoryLimit(ctx context.Context, path, entitiesPath string, entityCount int, sourceManifest json.RawMessage, identities map[string]string, memoryLimit string) error {
	return buildIndexJSONWithMemoryLimit(ctx, path, entitiesPath, entityCount, sourceManifest, encode(identities), memoryLimit, schema)
}

func buildIndexJSON(ctx context.Context, path, entitiesPath string, entityCount int, sourceManifest json.RawMessage, identitiesJSON string) error {
	return buildIndexJSONWithMemoryLimit(ctx, path, entitiesPath, entityCount, sourceManifest, identitiesJSON, "256MB", shardedSchema)
}

func buildIndexJSONWithMemoryLimit(ctx context.Context, path, entitiesPath string, entityCount int, sourceManifest json.RawMessage, identitiesJSON, memoryLimit, schemaSQL string) error {
	spill := filepath.Join(filepath.Dir(path), "duckdb-spill")
	dsn := path + "?threads=4&memory_limit=" + url.QueryEscape(memoryLimit) + "&preserve_insertion_order=false&temp_directory=" + url.QueryEscape(spill)
	db, err := sql.Open("duckdb", dsn)
	if err != nil {
		return err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()
	if _, err = db.ExecContext(ctx, "SET autoinstall_known_extensions=false; SET autoload_known_extensions=false"); err != nil {
		return err
	}
	preparedSchema := strings.ReplaceAll(schemaSQL, "?", sqlString(entitiesPath))
	if _, err = db.ExecContext(ctx, preparedSchema); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO metadata VALUES ('schema_version','2'),('source_manifest',?),('identities',?)", string(sourceManifest), identitiesJSON); err != nil {
		return err
	}
	statement, err := db.PrepareContext(ctx, postingsSQL)
	if err != nil {
		return err
	}
	for start := 1; start <= entityCount; start += postingChunk {
		end := min(start+postingChunk-1, entityCount)
		if _, err = statement.ExecContext(ctx, start, end, start, end, start, end); err != nil {
			statement.Close()
			return err
		}
	}
	if err = statement.Close(); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, "CREATE TABLE postings AS SELECT * FROM postings_stage ORDER BY token_id,entity_seq"); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, prefixesSQL); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, "CHECKPOINT"); err != nil {
		return err
	}
	return nil
}

func inside(p places.Location, bounds [4]float64) bool {
	return p.Lng >= bounds[0] && p.Lng <= bounds[2] && p.Lat >= bounds[1] && p.Lat <= bounds[3]
}

// Verify checks every manifest-bound file and the serving catalog before Open
// exposes a reader.
func Verify(path string) (Manifest, error) {
	var manifest Manifest
	raw, err := os.ReadFile(filepath.Join(path, ManifestName))
	if err != nil {
		return manifest, err
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return manifest, err
	}
	if manifest.Schema != 2 || manifest.CoordinateOrder != "longitude,latitude" || len(manifest.NormalizedSHA256) != 64 || len(manifest.Files) < 7 {
		return manifest, fmt.Errorf("unsupported DuckDB manifest")
	}
	roleOrder := map[string]int{"entities": 0, "sources": 1, "provenance": 2, "relationships": 3, "rejections": 4, "metadata": 5, "serving": 6}
	roleBase := map[string]string{"entities": EntitiesName, "sources": SourcesName, "provenance": ProvenanceName, "relationships": RelationshipsName, "rejections": RejectionsName, "metadata": MetadataName, "serving": IndexName}
	roleCounts := map[string]int{}
	seen := map[string]bool{}
	entityRows := 0
	var logical bytes.Buffer
	lastRole := -1
	for _, file := range manifest.Files {
		order, known := roleOrder[file.Role]
		base := roleBase[file.Role]
		stem, ext := strings.TrimSuffix(base, filepath.Ext(base)), filepath.Ext(base)
		validName := file.Name == base || strings.HasPrefix(file.Name, stem+"-") && strings.HasSuffix(file.Name, ext)
		if !known || order < lastRole || !validName || seen[file.Name] || filepath.Base(file.Name) != file.Name || file.Rows < 0 {
			return manifest, fmt.Errorf("invalid DuckDB artifact file: %s", file.Name)
		}
		lastRole = order
		roleCounts[file.Role]++
		if err = importer.Verify(filepath.Join(path, file.Name), file.SHA256); err != nil {
			return manifest, fmt.Errorf("verify %s: %w", file.Name, err)
		}
		if strings.HasSuffix(file.Name, ".parquet") {
			f, e := os.Open(filepath.Join(path, file.Name))
			if e != nil {
				return manifest, e
			}
			stat, e := f.Stat()
			if e != nil {
				f.Close()
				return manifest, e
			}
			pf, e := parquet.OpenFile(f, stat.Size())
			f.Close()
			if e != nil || pf.NumRows() != int64(file.Rows) {
				return manifest, fmt.Errorf("invalid %s row count: %v", file.Name, e)
			}
		}
		if file.Role == "entities" {
			entityRows += file.Rows
		}
		if file.Role != "serving" {
			fmt.Fprintf(&logical, "%s\x00%s\x00%s\x00%d\n", file.Role, file.Name, file.SHA256, file.Rows)
		}
		seen[file.Name] = true
	}
	for role := range roleOrder {
		if roleCounts[role] == 0 || (role == "metadata" || role == "serving") && roleCounts[role] != 1 {
			return manifest, fmt.Errorf("missing or duplicated %s artifact", role)
		}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return manifest, err
	}
	for _, entry := range entries {
		if entry.Name() != ManifestName && !seen[entry.Name()] {
			return manifest, fmt.Errorf("unmanifested artifact file: %s", entry.Name())
		}
	}
	logicalDigest := sha256.Sum256(logical.Bytes())
	if fmt.Sprintf("%x", logicalDigest) != manifest.NormalizedSHA256 {
		return manifest, fmt.Errorf("normalized generation checksum mismatch")
	}
	db, err := openDatabase(filepath.Join(path, IndexName))
	if err != nil {
		return manifest, err
	}
	defer db.Close()
	var version string
	if err = db.QueryRow("SELECT value FROM metadata WHERE key='schema_version'").Scan(&version); err != nil || version != "2" {
		return manifest, fmt.Errorf("DuckDB index schema: %s: %v", version, err)
	}
	var locators, searchRows, badPostings, uncoveredSearch, badPrefixHeads, badAddresses, spatialRows int
	if err = db.QueryRow("SELECT count(*) FROM entity_locator").Scan(&locators); err != nil {
		return manifest, err
	}
	if err = db.QueryRow("SELECT count(*) FROM search_entities").Scan(&searchRows); err != nil {
		return manifest, err
	}
	if locators != entityRows || searchRows != entityRows {
		return manifest, fmt.Errorf("DuckDB entity coverage: parquet=%d locator=%d search=%d", entityRows, locators, searchRows)
	}
	for _, file := range manifest.Files {
		if file.Role == "serving" && file.Rows != entityRows {
			return manifest, fmt.Errorf("DuckDB serving row count: got %d want %d", file.Rows, entityRows)
		}
	}
	var duplicateLocators, duplicateSearchRows, mismatchedEntities int
	if err = db.QueryRow("SELECT count(*)-count(DISTINCT id) FROM entity_locator").Scan(&duplicateLocators); err != nil {
		return manifest, err
	}
	if err = db.QueryRow("SELECT count(*)-count(DISTINCT id) FROM search_entities").Scan(&duplicateSearchRows); err != nil {
		return manifest, err
	}
	if duplicateLocators != 0 || duplicateSearchRows != 0 {
		return manifest, fmt.Errorf("DuckDB duplicate entities: locator=%d search=%d", duplicateLocators, duplicateSearchRows)
	}
	if err = db.QueryRow(`SELECT count(*) FROM search_entities s FULL OUTER JOIN entity_locator l USING(id)
WHERE s.id IS NULL OR l.id IS NULL OR s.kind<>l.kind`).Scan(&mismatchedEntities); err != nil {
		return manifest, err
	}
	if mismatchedEntities != 0 {
		return manifest, fmt.Errorf("DuckDB mismatched entities: %d", mismatchedEntities)
	}
	if err = db.QueryRow(`SELECT count(*) FROM postings p LEFT JOIN tokens t USING(token_id) LEFT JOIN search_entities e USING(entity_seq) WHERE t.token_id IS NULL OR e.entity_seq IS NULL`).Scan(&badPostings); err != nil {
		return manifest, err
	}
	if badPostings != 0 {
		return manifest, fmt.Errorf("DuckDB posting integrity: %d", badPostings)
	}
	if err = db.QueryRow(`SELECT count(*) FROM search_entities e LEFT JOIN postings p USING(entity_seq)
WHERE p.entity_seq IS NULL`).Scan(&uncoveredSearch); err != nil {
		return manifest, err
	}
	if uncoveredSearch != 0 {
		return manifest, fmt.Errorf("DuckDB search coverage: %d", uncoveredSearch)
	}
	if err = db.QueryRow(`SELECT count(*) FROM short_prefix_head h LEFT JOIN search_entities e USING(entity_seq)
WHERE e.entity_seq IS NULL`).Scan(&badPrefixHeads); err != nil {
		return manifest, err
	}
	if badPrefixHeads != 0 {
		return manifest, fmt.Errorf("DuckDB prefix-head integrity: %d", badPrefixHeads)
	}
	if err = db.QueryRow(`SELECT count(*) FROM address_lookup a LEFT JOIN entity_locator e ON e.id=a.entity_id WHERE e.id IS NULL OR e.kind<>'address'`).Scan(&badAddresses); err != nil {
		return manifest, err
	}
	if badAddresses != 0 {
		return manifest, fmt.Errorf("DuckDB address integrity: %d", badAddresses)
	}
	if err = db.QueryRow("SELECT count(*) FROM address_spatial").Scan(&spatialRows); err != nil {
		return manifest, err
	}
	var addressRows int
	if err = db.QueryRow("SELECT count(*) FROM address_lookup").Scan(&addressRows); err != nil {
		return manifest, err
	}
	if spatialRows != addressRows {
		return manifest, fmt.Errorf("DuckDB spatial coverage: address=%d spatial=%d", addressRows, spatialRows)
	}
	var expectedAddresses int
	if err = db.QueryRow(`SELECT count(*) FROM read_parquet(?) WHERE kind='address' AND address_key<>''`, filepath.Join(path, "entities*.parquet")).Scan(&expectedAddresses); err != nil {
		return manifest, err
	}
	if addressRows != expectedAddresses {
		return manifest, fmt.Errorf("DuckDB exact-address coverage: expected=%d actual=%d", expectedAddresses, addressRows)
	}
	var duplicateSources, badSources, badProvenance, badRelationships, duplicateRelationships, duplicateRejections, invalidRejections, indexes int
	sourcesPath := filepath.Join(path, "source-records*.parquet")
	provenancePath := filepath.Join(path, "attribute-provenance*.parquet")
	relationshipsPath := filepath.Join(path, "relationships*.parquet")
	if err = db.QueryRow(`SELECT count(*)-count(DISTINCT source_key) FROM read_parquet(?)`, sourcesPath).Scan(&duplicateSources); err != nil {
		return manifest, err
	}
	if duplicateSources != 0 {
		return manifest, fmt.Errorf("DuckDB duplicate source keys: %d", duplicateSources)
	}
	if err = db.QueryRow(`SELECT count(*) FROM read_parquet(?) s
LEFT JOIN entity_locator e ON e.id=s.entity_id WHERE e.id IS NULL`, sourcesPath).Scan(&badSources); err != nil {
		return manifest, err
	}
	if badSources != 0 {
		return manifest, fmt.Errorf("DuckDB source integrity: %d", badSources)
	}
	var sourceRows, locatedSources, provenanceRows, locatedProvenance int
	if err = db.QueryRow(`SELECT count(*) FROM read_parquet(?)`, sourcesPath).Scan(&sourceRows); err != nil {
		return manifest, err
	}
	if err = db.QueryRow(`SELECT coalesce(sum(source_count),0) FROM entity_locator`).Scan(&locatedSources); err != nil {
		return manifest, err
	}
	if sourceRows != locatedSources {
		return manifest, fmt.Errorf("DuckDB source locator coverage: parquet=%d located=%d", sourceRows, locatedSources)
	}
	if err = db.QueryRow(`SELECT count(*) FROM read_parquet(?) p
LEFT JOIN entity_locator e ON e.id=p.entity_id
LEFT JOIN read_parquet(?) s ON s.entity_id=p.entity_id AND s.source_key=p.source_key
WHERE e.id IS NULL OR s.source_key IS NULL`, provenancePath, sourcesPath).Scan(&badProvenance); err != nil {
		return manifest, err
	}
	if badProvenance != 0 {
		return manifest, fmt.Errorf("DuckDB provenance integrity: %d", badProvenance)
	}
	if err = db.QueryRow(`SELECT count(*) FROM read_parquet(?)`, provenancePath).Scan(&provenanceRows); err != nil {
		return manifest, err
	}
	if err = db.QueryRow(`SELECT coalesce(sum(provenance_count),0) FROM entity_locator`).Scan(&locatedProvenance); err != nil {
		return manifest, err
	}
	if provenanceRows != locatedProvenance {
		return manifest, fmt.Errorf("DuckDB provenance locator coverage: parquet=%d located=%d", provenanceRows, locatedProvenance)
	}
	if err = db.QueryRow(`SELECT count(*) FROM read_parquet(?) r
LEFT JOIN entity_locator f ON f.id=r.from_id
LEFT JOIN entity_locator t ON t.id=r.to_id
WHERE f.id IS NULL OR t.id IS NULL`, relationshipsPath).Scan(&badRelationships); err != nil {
		return manifest, err
	}
	if badRelationships != 0 {
		return manifest, fmt.Errorf("DuckDB relationship integrity: %d", badRelationships)
	}
	if err = db.QueryRow(`SELECT count(*)-count(DISTINCT (from_id,to_id,kind)) FROM read_parquet(?)`, relationshipsPath).Scan(&duplicateRelationships); err != nil {
		return manifest, err
	}
	if duplicateRelationships != 0 {
		return manifest, fmt.Errorf("DuckDB duplicate relationships: %d", duplicateRelationships)
	}
	if err = db.QueryRow(`SELECT count(*)-count(DISTINCT (source_key,reason)) FROM read_parquet(?)`, filepath.Join(path, "rejections*.parquet")).Scan(&duplicateRejections); err != nil {
		return manifest, err
	}
	if duplicateRejections != 0 {
		return manifest, fmt.Errorf("DuckDB duplicate rejections: %d", duplicateRejections)
	}
	if err = db.QueryRow(`SELECT count(*) FROM read_parquet(?) WHERE source_key='' OR reason='' OR NOT json_valid(raw)`, filepath.Join(path, "rejections*.parquet")).Scan(&invalidRejections); err != nil {
		return manifest, err
	}
	if invalidRejections != 0 {
		return manifest, fmt.Errorf("DuckDB invalid rejections: %d", invalidRejections)
	}
	if err = db.QueryRow("SELECT count(*) FROM duckdb_indexes()").Scan(&indexes); err != nil {
		return manifest, err
	}
	if indexes != 0 {
		return manifest, fmt.Errorf("DuckDB catalog unexpectedly contains %d persistent indexes", indexes)
	}
	return manifest, nil
}

func openDatabase(path string) (*sql.DB, error) {
	db, err := sql.Open("duckdb", path+"?access_mode=read_only&threads=1")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if err = db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
