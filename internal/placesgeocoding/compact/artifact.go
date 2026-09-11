// Package compact builds and reads the non-default Parquet plus SQLite lookup
// proof. It is intentionally separate from the production server snapshot.
package compact

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
	_ "modernc.org/sqlite"

	"openmaps/internal/geocoding"
	"openmaps/internal/importer"
	"openmaps/internal/places"
)

//go:embed schema.sql
var schema string

const (
	ManifestName       = "manifest.json"
	IndexName          = "serving.sqlite"
	EntitiesName       = "entities.parquet"
	SourcesName        = "source-records.parquet"
	ProvenanceName     = "attribute-provenance.parquet"
	RelationshipsName  = "relationships.parquet"
	MetadataName       = "metadata.parquet"
	maxRowsPerRowGroup = 32768
)

type File struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Rows   int    `json:"rows"`
}

// Manifest binds one immutable generation. The source manifest and identity
// mapping are also retained in metadata.parquet; serving.sqlite contains only
// the small copy required to open and query this generation.
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

// Build writes a checksum-addressed immutable directory and refuses to replace
// an existing path. It shares the existing importer's identity and conflict
// resolution, but is not used by the production server.
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
	temp, err := os.MkdirTemp(filepath.Dir(abs), ".compact-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			os.RemoveAll(temp)
		}
	}()

	entities := make([]entityRow, 0, len(resolved.Entities))
	sources := make([]sourceRow, 0, len(resolved.Sources))
	provenance := make([]provenanceRow, 0, len(resolved.Provenance))
	relationships := make([]relationshipRow, 0, len(resolved.Relationships))
	locators := map[string]locator{}
	for i, entity := range resolved.Entities {
		entities = append(entities, entityRow{
			ID: entity.ID, Kind: entity.Kind, Name: entity.Name, Address: entity.Address,
			Website: entity.Website, Subtype: entity.Subtype, Lat: entity.Location.Lat,
			Lng: entity.Location.Lng, Closed: entity.Closed,
			Attributions: encode(entity.Attributions),
		})
		locators[entity.ID] = locator{entityGroup: i / maxRowsPerRowGroup, entityRow: i % maxRowsPerRowGroup}
	}
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
	for _, p := range resolved.Provenance {
		l := locators[p.EntityID]
		if l.provenanceCount == 0 {
			l.provenanceStart = len(provenance)
		}
		l.provenanceCount++
		locators[p.EntityID] = l
		provenance = append(provenance, provenanceRow{p.EntityID, p.Attribute, p.SourceKey, p.SourcePath})
	}
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
	if err = buildIndex(ctx, filepath.Join(temp, IndexName), resolved, locators, scope); err != nil {
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
	if err = os.Rename(temp, abs); err != nil {
		return err
	}
	return nil
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

func buildIndex(ctx context.Context, path string, resolved importer.ResolvedBundle, locators map[string]locator, scope importer.Manifest) (err error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, schema); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO metadata VALUES('source_manifest',?)", string(resolved.Manifest)); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO metadata VALUES('identities',?)", encode(resolved.Identities)); err != nil {
		return err
	}
	shortPrefixes := map[string]bool{}
	for _, entity := range resolved.Entities {
		l := locators[entity.ID]
		if _, err = tx.Exec(`INSERT INTO entity_locator VALUES(?,?,?,?,?,?,?,?)`, entity.ID, entity.Kind, l.entityGroup, l.entityRow, l.sourceStart, l.sourceCount, l.provenanceStart, l.provenanceCount); err != nil {
			return err
		}
		result, e := tx.Exec(`INSERT INTO search_entities(id,kind,name,normalized_name,address,subtype,closed) VALUES(?,?,?,?,?,?,?)`, entity.ID, entity.Kind, entity.Name, entity.NormalizedName, entity.Address, entity.Subtype, entity.Closed)
		if e != nil {
			return e
		}
		rowid, e := result.LastInsertId()
		if e != nil {
			return e
		}
		if _, err = tx.Exec("INSERT INTO entity_fts(rowid,name,address,aliases) VALUES(?,?,?,?)", rowid, entity.NormalizedName, places.Normalize(entity.Address), places.Normalize(strings.Join(entity.Aliases, " "))); err != nil {
			return err
		}
		for _, text := range []string{entity.NormalizedName, places.Normalize(entity.Address), places.Normalize(strings.Join(entity.Aliases, " "))} {
			for _, token := range strings.Fields(text) {
				runes := []rune(token)
				if len(runes) >= 2 {
					shortPrefixes[string(runes[:2])] = true
				}
			}
		}
		if entity.Kind != "address" || !inside(entity.Location, scope.BBox) {
			continue
		}
		key, contextText, ok := geocoding.AddressIndex(entity.Name, entity.Address)
		if !ok {
			continue
		}
		result, e = tx.Exec(`INSERT INTO address_lookup(entity_id,address_key,context,lat,lng) VALUES(?,?,?,?,?)`, entity.ID, key, contextText, entity.Location.Lat, entity.Location.Lng)
		if e != nil {
			return e
		}
		addressRowID, e := result.LastInsertId()
		if e != nil {
			return e
		}
		if _, err = tx.Exec("INSERT INTO address_rtree VALUES(?,?,?,?,?)", addressRowID, entity.Location.Lng, entity.Location.Lng, entity.Location.Lat, entity.Location.Lat); err != nil {
			return err
		}
	}
	prefixes := make([]string, 0, len(shortPrefixes))
	for prefix := range shortPrefixes {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	for _, prefix := range prefixes {
		entities, e := queryFTS(ctx, tx, prefix)
		if e != nil {
			return e
		}
		for rank, entity := range entities {
			if _, err = tx.Exec("INSERT INTO short_prefix_head VALUES(?,?,?)", prefix, rank, entity.ID); err != nil {
				return err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	var check string
	if err = db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); err != nil {
		return err
	}
	if check != "ok" {
		return fmt.Errorf("compact index integrity: %s", check)
	}
	return nil
}

func inside(p places.Location, bounds [4]float64) bool {
	return p.Lng >= bounds[0] && p.Lng <= bounds[2] && p.Lat >= bounds[1] && p.Lat <= bounds[3]
}

// Verify checks every manifest-bound file before a serving reader is opened.
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
		return manifest, fmt.Errorf("unsupported compact manifest")
	}
	expected := map[string]bool{EntitiesName: true, SourcesName: true, ProvenanceName: true, RelationshipsName: true, MetadataName: true, IndexName: true}
	seen := map[string]bool{}
	entityRows := 0
	for _, file := range manifest.Files {
		if !expected[file.Name] || seen[file.Name] || filepath.Base(file.Name) != file.Name || file.Rows < 0 {
			return manifest, fmt.Errorf("invalid compact artifact file: %s", file.Name)
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
			if e != nil {
				return manifest, fmt.Errorf("open %s: %w", file.Name, e)
			}
			if pf.NumRows() != int64(file.Rows) {
				return manifest, fmt.Errorf("%s row count: got %d want %d", file.Name, pf.NumRows(), file.Rows)
			}
		}
		if file.Name == EntitiesName {
			entityRows = file.Rows
		}
		seen[file.Name] = true
	}
	u := url.URL{Scheme: "file", Path: filepath.Join(path, IndexName)}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return manifest, err
	}
	defer db.Close()
	var check, version string
	if err = db.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil {
		return manifest, err
	}
	if check != "ok" {
		return manifest, fmt.Errorf("compact index integrity: %s", check)
	}
	if err = db.QueryRow("SELECT value FROM metadata WHERE key='schema_version'").Scan(&version); err != nil {
		return manifest, err
	}
	if version != "1" {
		return manifest, fmt.Errorf("compact index schema: %s", version)
	}
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		return manifest, err
	}
	bad := rows.Next()
	err = rows.Err()
	rows.Close()
	if err != nil {
		return manifest, err
	}
	if bad {
		return manifest, fmt.Errorf("compact index foreign key check failed")
	}
	var entities, missingSearch, missingSpatial int
	if err = db.QueryRow("SELECT count(*) FROM entity_locator").Scan(&entities); err != nil {
		return manifest, err
	}
	if entities != entityRows {
		return manifest, fmt.Errorf("compact locator count: got %d want %d", entities, entityRows)
	}
	if err = db.QueryRow(`SELECT count(*) FROM entity_locator l LEFT JOIN search_entities s ON s.id=l.id LEFT JOIN entity_fts f ON f.rowid=s.rowid WHERE s.id IS NULL OR f.rowid IS NULL`).Scan(&missingSearch); err != nil {
		return manifest, err
	}
	if missingSearch != 0 {
		return manifest, fmt.Errorf("compact search coverage: %d", missingSearch)
	}
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM address_lookup) - (SELECT count(*) FROM address_rtree)`).Scan(&missingSpatial); err != nil {
		return manifest, err
	}
	if missingSpatial != 0 {
		return manifest, fmt.Errorf("compact spatial coverage: %d", missingSpatial)
	}
	return manifest, nil
}
