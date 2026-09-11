// Package duckdb builds and reads the non-default Parquet plus DuckDB lookup
// candidate. The production server continues to use its SQLite snapshot.
package duckdb

import (
	"context"
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

	"openmaps/internal/geocoding"
	"openmaps/internal/importer"
	"openmaps/internal/places"
)

//go:embed schema.sql
var schema string

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
	MetadataName       = "metadata.parquet"
	maxRowsPerRowGroup = 32768
	postingChunk       = 32768
)

type File struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Rows   int    `json:"rows"`
}

type Manifest struct {
	Schema          int    `json:"schema"`
	CoordinateOrder string `json:"coordinate_order"`
	Files           []File `json:"files"`
}

type locator struct {
	entityGroup, entityRow           int
	sourceStart, sourceCount         int
	provenanceStart, provenanceCount int
}

// Build writes a checksum-addressed immutable candidate directory. Search,
// address, and spatial serving structures are derived from entities.parquet by
// DuckDB rather than from the in-memory resolved bundle.
func Build(ctx context.Context, path string, bundle importer.Bundle) (err error) {
	resolved, err := importer.Resolve(bundle)
	if err != nil {
		return err
	}
	var scope importer.Manifest
	if err = json.Unmarshal(resolved.Manifest, &scope); err != nil {
		return err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if _, err = os.Stat(abs); err == nil {
		return fmt.Errorf("output exists; choose a new artifact directory")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return err
	}
	temp, err := os.MkdirTemp(filepath.Dir(abs), ".duckdb-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(temp)
		}
	}()

	locators := make(map[string]locator, len(resolved.Entities))
	for i, entity := range resolved.Entities {
		locators[entity.ID] = locator{entityGroup: i / maxRowsPerRowGroup, entityRow: i % maxRowsPerRowGroup}
	}
	sources := make([]sourceRow, 0, len(resolved.Sources))
	for _, source := range resolved.Sources {
		l := locators[source.EntityID]
		if l.sourceCount == 0 {
			l.sourceStart = len(sources)
		}
		l.sourceCount++
		locators[source.EntityID] = l
		r := source.Record
		sources = append(sources, sourceRow{
			EntityID: source.EntityID, SourceKey: r.Key(), Source: r.Source,
			SourceID: r.SourceID, Release: r.Release, Priority: int64(r.Priority),
			Attributes: encode(r.Attributes), Paths: encode(r.Paths), Raw: string(r.Raw),
		})
	}
	provenance := make([]provenanceRow, 0, len(resolved.Provenance))
	for _, p := range resolved.Provenance {
		l := locators[p.EntityID]
		if l.provenanceCount == 0 {
			l.provenanceStart = len(provenance)
		}
		l.provenanceCount++
		locators[p.EntityID] = l
		provenance = append(provenance, provenanceRow{p.EntityID, p.Attribute, p.SourceKey, p.SourcePath})
	}
	entities := make([]entityRow, 0, len(resolved.Entities))
	for _, entity := range resolved.Entities {
		l := locators[entity.ID]
		addressKey, addressContext := "", ""
		if entity.Kind == "address" && inside(entity.Location, scope.BBox) {
			addressKey, addressContext, _ = geocoding.AddressIndex(entity.Name, entity.Address)
		}
		entities = append(entities, entityRow{
			ID: entity.ID, Kind: entity.Kind, Name: entity.Name,
			NormalizedName: entity.NormalizedName, Address: entity.Address,
			NormalizedAddress: places.Normalize(entity.Address),
			NormalizedAliases: places.Normalize(strings.Join(entity.Aliases, " ")),
			Website:           entity.Website, Subtype: entity.Subtype,
			Lat: entity.Location.Lat, Lng: entity.Location.Lng, Closed: entity.Closed,
			Attributions: encode(entity.Attributions), EntityGroup: int64(l.entityGroup),
			EntityRow: int64(l.entityRow), SourceStart: int64(l.sourceStart),
			SourceCount: int64(l.sourceCount), ProvenanceStart: int64(l.provenanceStart),
			ProvenanceCount: int64(l.provenanceCount), AddressKey: addressKey,
			AddressContext: addressContext,
		})
	}
	relationships := make([]relationshipRow, 0, len(resolved.Relationships))
	for _, r := range resolved.Relationships {
		relationships = append(relationships, relationshipRow{r.FromID, r.ToID, r.Kind, r.Evidence})
	}
	metadata := []metadataRow{
		{Key: "identities", Value: encode(resolved.Identities)},
		{Key: "source_manifest", Value: string(resolved.Manifest)},
	}
	files := []struct {
		name  string
		rows  int
		write func(string) error
	}{
		{EntitiesName, len(entities), func(path string) error { return writeParquet(path, entities) }},
		{SourcesName, len(sources), func(path string) error { return writeParquet(path, sources) }},
		{ProvenanceName, len(provenance), func(path string) error { return writeParquet(path, provenance) }},
		{RelationshipsName, len(relationships), func(path string) error { return writeParquet(path, relationships) }},
		{MetadataName, len(metadata), func(path string) error { return writeParquet(path, metadata) }},
	}
	manifest := Manifest{Schema: 1, CoordinateOrder: "longitude,latitude"}
	for _, file := range files {
		if err = file.write(filepath.Join(temp, file.name)); err != nil {
			return err
		}
		digest, e := importer.Checksum(filepath.Join(temp, file.name))
		if e != nil {
			return e
		}
		manifest.Files = append(manifest.Files, File{Name: file.name, SHA256: digest, Rows: file.rows})
	}
	if err = buildIndex(ctx, filepath.Join(temp, IndexName), filepath.Join(temp, EntitiesName), len(entities), resolved.Manifest, resolved.Identities); err != nil {
		return err
	}
	digest, err := importer.Checksum(filepath.Join(temp, IndexName))
	if err != nil {
		return err
	}
	manifest.Files = append(manifest.Files, File{Name: IndexName, SHA256: digest, Rows: len(entities)})
	if err = importer.WriteJSON(filepath.Join(temp, ManifestName), manifest); err != nil {
		return err
	}
	if _, err = Verify(temp); err != nil {
		return err
	}
	return os.Rename(temp, abs)
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
	preparedSchema := strings.ReplaceAll(schema, "?", sqlString(entitiesPath))
	if _, err = db.ExecContext(ctx, preparedSchema); err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO metadata VALUES ('schema_version','1'),('source_manifest',?),('identities',?)", string(sourceManifest), encode(identities)); err != nil {
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
	if manifest.Schema != 1 || manifest.CoordinateOrder != "longitude,latitude" || len(manifest.Files) != 6 {
		return manifest, fmt.Errorf("unsupported DuckDB manifest")
	}
	expected := map[string]bool{EntitiesName: true, SourcesName: true, ProvenanceName: true, RelationshipsName: true, MetadataName: true, IndexName: true}
	seen := map[string]bool{}
	entityRows := 0
	for _, file := range manifest.Files {
		if !expected[file.Name] || seen[file.Name] || filepath.Base(file.Name) != file.Name || file.Rows < 0 {
			return manifest, fmt.Errorf("invalid DuckDB artifact file: %s", file.Name)
		}
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
		if file.Name == EntitiesName {
			entityRows = file.Rows
		}
		seen[file.Name] = true
	}
	db, err := openDatabase(filepath.Join(path, IndexName))
	if err != nil {
		return manifest, err
	}
	defer db.Close()
	var version string
	if err = db.QueryRow("SELECT value FROM metadata WHERE key='schema_version'").Scan(&version); err != nil || version != "1" {
		return manifest, fmt.Errorf("DuckDB index schema: %s: %v", version, err)
	}
	var locators, searchRows, badPostings, badAddresses, spatialRows int
	if err = db.QueryRow("SELECT count(*) FROM entity_locator").Scan(&locators); err != nil {
		return manifest, err
	}
	if err = db.QueryRow("SELECT count(*) FROM search_entities").Scan(&searchRows); err != nil {
		return manifest, err
	}
	if locators != entityRows || searchRows != entityRows {
		return manifest, fmt.Errorf("DuckDB entity coverage: parquet=%d locator=%d search=%d", entityRows, locators, searchRows)
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
	var duplicateSources, badSources, badProvenance, badRelationships, indexes int
	sourcesPath := filepath.Join(path, SourcesName)
	provenancePath := filepath.Join(path, ProvenanceName)
	relationshipsPath := filepath.Join(path, RelationshipsName)
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
	if err = db.QueryRow(`SELECT count(*) FROM read_parquet(?) p
LEFT JOIN entity_locator e ON e.id=p.entity_id
LEFT JOIN read_parquet(?) s ON s.entity_id=p.entity_id AND s.source_key=p.source_key
WHERE e.id IS NULL OR s.source_key IS NULL`, provenancePath, sourcesPath).Scan(&badProvenance); err != nil {
		return manifest, err
	}
	if badProvenance != 0 {
		return manifest, fmt.Errorf("DuckDB provenance integrity: %d", badProvenance)
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
