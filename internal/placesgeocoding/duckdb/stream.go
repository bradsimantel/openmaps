package duckdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"

	"openmaps/internal/geocoding"
	"openmaps/internal/importer"
	"openmaps/internal/places"
)

// rejectionRow records a provider record that was deliberately excluded. The
// source adapter supplies the stable key, reason code and original record.
type rejectionRow struct {
	SourceKey string `parquet:"source_key"`
	Reason    string `parquet:"reason"`
	Raw       string `parquet:"raw"`
}

type parquetSink[T any] struct {
	basePath      string
	file          *os.File
	out           *parquet.GenericWriter[T]
	buf           []T
	rows          int
	localRows     int
	shard         int
	rotatePending bool
	active        bool
	files         []shardFile
}

type shardFile struct {
	name string
	rows int
}

var targetParquetShardBytes int64 = 384 << 20

func newParquetSink[T any](path string) (*parquetSink[T], error) {
	sink := &parquetSink[T]{basePath: path, buf: make([]T, 0, maxRowsPerRowGroup)}
	if err := sink.open(); err != nil {
		return nil, err
	}
	return sink, nil
}

func (sink *parquetSink[T]) add(row T) error {
	return sink.addGroup([]T{row})
}

// addGroup never splits one entity's contiguous source/provenance span across
// shards. Rotation happens at a row-group boundary before the next group.
func (sink *parquetSink[T]) addGroup(rows []T) error {
	if err := sink.prepareGroup(); err != nil {
		return err
	}
	for _, row := range rows {
		sink.buf = append(sink.buf, row)
		sink.rows++
		sink.localRows++
		if len(sink.buf) == cap(sink.buf) {
			if err := sink.flush(); err != nil {
				return err
			}
		}
	}
	return nil
}

func (sink *parquetSink[T]) prepareGroup() error {
	if sink.rotatePending && sink.localRows > 0 {
		return sink.rotate()
	}
	return nil
}

func (sink *parquetSink[T]) flush() error {
	if len(sink.buf) == 0 {
		return nil
	}
	_, err := sink.out.Write(sink.buf)
	if err == nil {
		err = sink.out.Flush()
	}
	sink.buf = sink.buf[:0]
	if err == nil && sink.out.Size() >= targetParquetShardBytes {
		sink.rotatePending = true
	}
	return err
}

func (sink *parquetSink[T]) name() string { return filepath.Base(sink.file.Name()) }

func (sink *parquetSink[T]) open() error {
	path := sink.basePath
	if sink.shard > 0 {
		ext := filepath.Ext(path)
		path = strings.TrimSuffix(path, ext) + fmt.Sprintf("-%05d", sink.shard) + ext
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	sink.file = file
	sink.out = parquet.NewGenericWriter[T](file,
		parquet.Compression(&zstd.Codec{Level: zstd.SpeedDefault}),
		parquet.DictionaryMaxBytes(4<<20),
		parquet.MaxRowsPerRowGroup(maxRowsPerRowGroup))
	sink.localRows = 0
	sink.rotatePending = false
	sink.active = true
	return nil
}

func (sink *parquetSink[T]) closeCurrent() (err error) {
	if !sink.active {
		return nil
	}
	if err = sink.flush(); err == nil {
		err = sink.out.Close()
	}
	if closeErr := sink.file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		sink.files = append(sink.files, shardFile{name: filepath.Base(sink.file.Name()), rows: sink.localRows})
	}
	sink.active = false
	return err
}

func (sink *parquetSink[T]) rotate() error {
	if err := sink.closeCurrent(); err != nil {
		return err
	}
	sink.shard++
	return sink.open()
}

func (sink *parquetSink[T]) close() error { return sink.closeCurrent() }

// BuildJSON consumes the normalized-provider stream without retaining the
// snapshot in Go slices or maps. DuckDB externally sorts identity groups and
// relationships; Go retains only one entity's contributing source records at
// a time while writing deterministic Parquet row groups.
func BuildJSON(ctx context.Context, path string, input io.Reader) (err error) {
	return buildStaged(ctx, path, "128MB", "256MB", nil, func(stage *sql.DB) (json.RawMessage, int, error) {
		return stageJSON(ctx, stage, input)
	})
}

// StreamWriter is the bounded input seam used by provider adapters. Each call
// is committed as one transaction; callers choose and test their own batch
// size. The writer never retains a submitted batch after the call returns.
type StreamWriter interface {
	WriteRecords(context.Context, []importer.Record) error
	WriteRelationships(context.Context, []importer.Relationship) error
	WriteRejections(context.Context, []importer.Rejection) error
}

type stageWriter struct{ db *sql.DB }

func (w stageWriter) WriteRecords(ctx context.Context, rows []importer.Record) error {
	return writeStageBatch(ctx, w.db, "INSERT INTO input_records VALUES (?,?,?,?)", len(rows), func(stmt *sql.Stmt, i int) error {
		raw, err := json.Marshal(rows[i])
		if err != nil {
			return err
		}
		_, err = stmt.ExecContext(ctx, rows[i].Key(), rows[i].Kind, rows[i].Priority, string(raw))
		return err
	})
}

func (w stageWriter) WriteRelationships(ctx context.Context, rows []importer.Relationship) error {
	return writeStageBatch(ctx, w.db, "INSERT INTO input_relationships VALUES (?,?,?,?)", len(rows), func(stmt *sql.Stmt, i int) error {
		_, err := stmt.ExecContext(ctx, rows[i].From, rows[i].To, rows[i].Kind, rows[i].Evidence)
		return err
	})
}

func (w stageWriter) WriteRejections(ctx context.Context, rows []importer.Rejection) error {
	return writeStageBatch(ctx, w.db, "INSERT INTO input_rejections VALUES (?,?,?)", len(rows), func(stmt *sql.Stmt, i int) error {
		_, err := stmt.ExecContext(ctx, rows[i].SourceKey, rows[i].Reason, string(rows[i].Raw))
		return err
	})
}

func writeStageBatch(ctx context.Context, db *sql.DB, query string, count int, write func(*sql.Stmt, int) error) error {
	if count == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i := 0; i < count; i++ {
		if err = write(stmt, i); err != nil {
			return err
		}
	}
	if err = stmt.Close(); err != nil {
		return err
	}
	return tx.Commit()
}

// BuildStream constructs a generation directly from a provider adapter. It is
// the national-safe counterpart to BuildJSON: both paths share the same
// bounded DuckDB normalization and Parquet/catalog publication code.
func BuildStream(ctx context.Context, path string, manifest json.RawMessage, identities map[string]string, memoryLimit, catalogMemoryLimit string, observe func(string, time.Duration), produce func(context.Context, StreamWriter) error) error {
	if len(manifest) == 0 || !json.Valid(manifest) || memoryLimit == "" || catalogMemoryLimit == "" || produce == nil {
		return fmt.Errorf("valid manifest, memory limits and stream producer are required")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, manifest); err != nil {
		return err
	}
	manifest = compact.Bytes()
	return buildStaged(ctx, path, memoryLimit, catalogMemoryLimit, observe, func(stage *sql.DB) (json.RawMessage, int, error) {
		writer := stageWriter{db: stage}
		identityRows := make([][2]string, 0, len(identities))
		for source, target := range identities {
			identityRows = append(identityRows, [2]string{source, target})
		}
		if err := writeStageBatch(ctx, stage, "INSERT INTO input_identities VALUES (?,?)", len(identityRows), func(stmt *sql.Stmt, i int) error {
			_, err := stmt.ExecContext(ctx, identityRows[i][0], identityRows[i][1])
			return err
		}); err != nil {
			return nil, 0, err
		}
		if err := produce(ctx, writer); err != nil {
			return nil, 0, err
		}
		return manifest, 1, nil
	})
}

func buildStaged(ctx context.Context, path, memoryLimit, catalogMemoryLimit string, observe func(string, time.Duration), stageInput func(*sql.DB) (json.RawMessage, int, error)) (err error) {
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
	temp, err := os.MkdirTemp(filepath.Dir(abs), ".duckdb-generation-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(temp) }()

	stagePath := filepath.Join(temp, "normalize.duckdb")
	spill := filepath.Join(temp, "normalize-spill")
	dsn := stagePath + "?threads=1&memory_limit=" + url.QueryEscape(memoryLimit) + "&preserve_insertion_order=false&temp_directory=" + url.QueryEscape(spill)
	stage, err := sql.Open("duckdb", dsn)
	if err != nil {
		return err
	}
	stage.SetMaxOpenConns(1)
	defer stage.Close()
	if _, err = stage.ExecContext(ctx, `SET autoinstall_known_extensions=false; SET autoload_known_extensions=false;
CREATE TABLE input_records(source_key VARCHAR,kind VARCHAR,priority INTEGER,record_json VARCHAR);
CREATE TABLE input_identities(source_key VARCHAR,target VARCHAR);
CREATE TABLE input_relationships(from_key VARCHAR,to_key VARCHAR,kind VARCHAR,evidence VARCHAR);
CREATE TABLE input_rejections(source_key VARCHAR,reason VARCHAR,raw VARCHAR);`); err != nil {
		return err
	}
	inputStarted := time.Now()
	manifestRaw, schemaVersion, err := stageInput(stage)
	if err != nil {
		return err
	}
	if observe != nil {
		observe("input_staging", time.Since(inputStarted))
	}
	if schemaVersion != 1 || len(manifestRaw) == 0 || !json.Valid(manifestRaw) {
		return fmt.Errorf("unsupported or empty bundle")
	}
	var scope importer.Manifest
	if err = json.Unmarshal(manifestRaw, &scope); err != nil {
		return err
	}
	normalizeStarted := time.Now()
	if err = validateStage(ctx, stage); err != nil {
		return err
	}
	if _, err = stage.ExecContext(ctx, `CREATE TABLE resolved_keys AS
SELECT r.source_key,r.kind,
       'om_'||substr(sha256('openmaps:entity:v1:'||coalesce(i.target,r.source_key)),1,32) AS entity_id
FROM input_records r LEFT JOIN input_identities i USING(source_key)
ORDER BY entity_id,source_key`); err != nil {
		return err
	}

	entities, err := newParquetSink[entityRow](filepath.Join(temp, EntitiesName))
	if err != nil {
		return err
	}
	defer entities.close()
	sources, err := newParquetSink[sourceRow](filepath.Join(temp, SourcesName))
	if err != nil {
		return err
	}
	defer sources.close()
	provenance, err := newParquetSink[provenanceRow](filepath.Join(temp, ProvenanceName))
	if err != nil {
		return err
	}
	defer provenance.close()

	rows, err := stage.QueryContext(ctx, `SELECT k.entity_id,r.record_json
FROM resolved_keys k JOIN input_records r USING(source_key)
ORDER BY k.entity_id,r.priority DESC,r.source_key`)
	if err != nil {
		return err
	}
	var currentID string
	group := []importer.Record{}
	writeGroup := func() error {
		if currentID == "" {
			return nil
		}
		entity, entitySources, entityProvenance, groupErr := importer.ResolveEntityGroup(currentID, group)
		if groupErr != nil {
			return groupErr
		}
		if groupErr = sources.prepareGroup(); groupErr != nil {
			return groupErr
		}
		if groupErr = provenance.prepareGroup(); groupErr != nil {
			return groupErr
		}
		sourceStart, provenanceStart := sources.localRows, provenance.localRows
		sourceFile, provenanceFile := sources.name(), provenance.name()
		sourceRows := make([]sourceRow, 0, len(entitySources))
		for _, source := range entitySources {
			record := source.Record
			sourceRows = append(sourceRows, sourceRow{EntityID: source.EntityID, SourceKey: record.Key(), Source: record.Source, SourceID: record.SourceID, Release: record.Release, Priority: int64(record.Priority), Attributes: encode(record.Attributes), Paths: encode(record.Paths), Raw: string(record.Raw)})
		}
		if groupErr = sources.addGroup(sourceRows); groupErr != nil {
			return groupErr
		}
		provenanceRows := make([]provenanceRow, 0, len(entityProvenance))
		for _, item := range entityProvenance {
			provenanceRows = append(provenanceRows, provenanceRow{item.EntityID, item.Attribute, item.SourceKey, item.SourcePath})
		}
		if groupErr = provenance.addGroup(provenanceRows); groupErr != nil {
			return groupErr
		}
		addressKey, addressContext := "", ""
		if entity.Kind == "address" && inside(entity.Location, scope.BBox) {
			addressKey, addressContext, _ = geocoding.AddressIndex(entity.Name, entity.Address)
		}
		if groupErr = entities.prepareGroup(); groupErr != nil {
			return groupErr
		}
		return entities.add(entityRow{
			ID: entity.ID, Kind: entity.Kind, Name: entity.Name, NormalizedName: entity.NormalizedName,
			Address: entity.Address, NormalizedAddress: places.Normalize(entity.Address),
			NormalizedAliases: places.Normalize(strings.Join(entity.Aliases, " ")), Website: entity.Website,
			Subtype: entity.Subtype, Lat: entity.Location.Lat, Lng: entity.Location.Lng, Closed: entity.Closed,
			Attributions: encode(entity.Attributions), EntityGroup: int64(entities.localRows / maxRowsPerRowGroup),
			EntityRow: int64(entities.localRows % maxRowsPerRowGroup), SourceStart: int64(sourceStart),
			SourceCount: int64(len(entitySources)), SourceFile: sourceFile, ProvenanceStart: int64(provenanceStart),
			ProvenanceCount: int64(len(entityProvenance)), ProvenanceFile: provenanceFile,
			AddressKey: addressKey, AddressContext: addressContext,
		})
	}
	for rows.Next() {
		var id string
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			rows.Close()
			return err
		}
		if currentID != "" && id != currentID {
			if err = writeGroup(); err != nil {
				rows.Close()
				return err
			}
			group = group[:0]
		}
		currentID = id
		var record importer.Record
		if err = json.Unmarshal(raw, &record); err != nil {
			rows.Close()
			return err
		}
		group = append(group, record)
	}
	if err = rows.Err(); err == nil {
		err = writeGroup()
	}
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err = entities.close(); err != nil {
		return err
	}
	if err = sources.close(); err != nil {
		return err
	}
	if err = provenance.close(); err != nil {
		return err
	}

	relationships, err := newParquetSink[relationshipRow](filepath.Join(temp, RelationshipsName))
	if err != nil {
		return err
	}
	defer relationships.close()
	relRows, err := stage.QueryContext(ctx, `SELECT f.entity_id,t.entity_id,r.kind,min(r.evidence),min(f.kind),min(t.kind)
FROM input_relationships r
LEFT JOIN resolved_keys f ON f.source_key=r.from_key
LEFT JOIN resolved_keys t ON t.source_key=r.to_key
GROUP BY f.entity_id,t.entity_id,r.kind
ORDER BY 1,2,3,4`)
	if err != nil {
		return err
	}
	for relRows.Next() {
		var row relationshipRow
		var fromKind, toKind sql.NullString
		if err = relRows.Scan(&row.FromID, &row.ToID, &row.Kind, &row.Evidence, &fromKind, &toKind); err != nil {
			return err
		}
		if row.FromID == "" || row.ToID == "" || row.FromID == row.ToID || !fromKind.Valid || !toKind.Valid {
			return fmt.Errorf("invalid relationship: %s %s %s", row.FromID, row.Kind, row.ToID)
		}
		if row.Kind == "address" && (fromKind.String != "business" || toKind.String != "address") || row.Kind == "parent_area" && (fromKind.String != "area" || toKind.String != "area") {
			return fmt.Errorf("invalid %s relationship", row.Kind)
		}
		if err = relationships.add(row); err != nil {
			return err
		}
	}
	if err = relRows.Err(); err != nil {
		return err
	}
	if err = relRows.Close(); err != nil {
		return err
	}
	if err = relationships.close(); err != nil {
		return err
	}

	rejections, err := newParquetSink[rejectionRow](filepath.Join(temp, RejectionsName))
	if err != nil {
		return err
	}
	defer rejections.close()
	rejectedRows, err := stage.QueryContext(ctx, "SELECT source_key,reason,raw FROM input_rejections ORDER BY source_key,reason")
	if err != nil {
		return err
	}
	for rejectedRows.Next() {
		var row rejectionRow
		if err = rejectedRows.Scan(&row.SourceKey, &row.Reason, &row.Raw); err != nil {
			return err
		}
		if err = rejections.add(row); err != nil {
			return err
		}
	}
	if err = rejectedRows.Err(); err != nil {
		return err
	}
	if err = rejectedRows.Close(); err != nil {
		return err
	}
	if err = rejections.close(); err != nil {
		return err
	}

	identitiesJSON, err := canonicalIdentities(ctx, stage)
	if err != nil {
		return err
	}
	metadataRows := []metadataRow{{Key: "identities", Value: identitiesJSON}, {Key: "source_manifest", Value: string(manifestRaw)}}
	if err = writeParquet(filepath.Join(temp, MetadataName), metadataRows); err != nil {
		return err
	}

	manifest := Manifest{Schema: 2, CoordinateOrder: "longitude,latitude"}
	var logical bytes.Buffer
	for _, set := range []struct {
		role  string
		files []shardFile
	}{{"entities", entities.files}, {"sources", sources.files}, {"provenance", provenance.files}, {"relationships", relationships.files}, {"rejections", rejections.files}, {"metadata", []shardFile{{name: MetadataName, rows: len(metadataRows)}}}} {
		for _, file := range set.files {
			digest, checksumErr := importer.Checksum(filepath.Join(temp, file.name))
			if checksumErr != nil {
				return checksumErr
			}
			manifest.Files = append(manifest.Files, File{Name: file.name, Role: set.role, SHA256: digest, Rows: file.rows})
			fmt.Fprintf(&logical, "%s\x00%s\x00%s\x00%d\n", set.role, file.name, digest, file.rows)
		}
	}
	logicalDigest := sha256.Sum256(logical.Bytes())
	manifest.NormalizedSHA256 = fmt.Sprintf("%x", logicalDigest)
	if err = stage.Close(); err != nil {
		return fmt.Errorf("close normalization stage: %w", err)
	}
	_ = os.Remove(stagePath)
	_ = os.RemoveAll(spill)
	if observe != nil {
		observe("normalization_parquet", time.Since(normalizeStarted))
	}
	catalogStarted := time.Now()
	if err = buildIndexJSONWithMemoryLimit(ctx, filepath.Join(temp, IndexName), filepath.Join(temp, "entities*.parquet"), entities.rows, manifestRaw, identitiesJSON, catalogMemoryLimit, shardedSchema); err != nil {
		return fmt.Errorf("build serving catalog: %w", err)
	}
	indexDigest, err := importer.Checksum(filepath.Join(temp, IndexName))
	if err != nil {
		return err
	}
	manifest.Files = append(manifest.Files, File{Name: IndexName, Role: "serving", SHA256: indexDigest, Rows: entities.rows})
	if observe != nil {
		observe("catalog", time.Since(catalogStarted))
	}
	if err = importer.WriteJSON(filepath.Join(temp, ManifestName), manifest); err != nil {
		return err
	}
	verifyStarted := time.Now()
	if _, err = Verify(temp); err != nil {
		return err
	}
	if observe != nil {
		observe("validation", time.Since(verifyStarted))
	}
	return os.Rename(temp, abs)
}

func stageJSON(ctx context.Context, db *sql.DB, input io.Reader) (json.RawMessage, int, error) {
	decoder := json.NewDecoder(input)
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, 0, fmt.Errorf("bundle must be a JSON object: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	var manifest json.RawMessage
	var schema int
	for decoder.More() {
		keyToken, tokenErr := decoder.Token()
		if tokenErr != nil {
			return nil, 0, tokenErr
		}
		key := keyToken.(string)
		switch key {
		case "schema":
			err = decoder.Decode(&schema)
		case "manifest":
			err = decoder.Decode(&manifest)
		case "identities":
			err = decodeObject(decoder, func(source string) error {
				var target string
				if decodeErr := decoder.Decode(&target); decodeErr != nil {
					return decodeErr
				}
				_, insertErr := tx.ExecContext(ctx, "INSERT INTO input_identities VALUES (?,?)", source, target)
				return insertErr
			})
		case "records":
			err = decodeArray(decoder, func() error {
				var record importer.Record
				if decodeErr := decoder.Decode(&record); decodeErr != nil {
					return decodeErr
				}
				raw, marshalErr := json.Marshal(record)
				if marshalErr != nil {
					return marshalErr
				}
				_, insertErr := tx.ExecContext(ctx, "INSERT INTO input_records VALUES (?,?,?,?)", record.Key(), record.Kind, record.Priority, string(raw))
				return insertErr
			})
		case "relationships":
			err = decodeArray(decoder, func() error {
				var relationship importer.Relationship
				if decodeErr := decoder.Decode(&relationship); decodeErr != nil {
					return decodeErr
				}
				_, insertErr := tx.ExecContext(ctx, "INSERT INTO input_relationships VALUES (?,?,?,?)", relationship.From, relationship.To, relationship.Kind, relationship.Evidence)
				return insertErr
			})
		case "rejections":
			err = decodeArray(decoder, func() error {
				var rejection importer.Rejection
				if decodeErr := decoder.Decode(&rejection); decodeErr != nil {
					return decodeErr
				}
				_, insertErr := tx.ExecContext(ctx, "INSERT INTO input_rejections VALUES (?,?,?)", rejection.SourceKey, rejection.Reason, string(rejection.Raw))
				return insertErr
			})
		default:
			return nil, 0, fmt.Errorf("unknown bundle field %q", key)
		}
		if err != nil {
			return nil, 0, err
		}
	}
	if _, err = decoder.Token(); err != nil {
		return nil, 0, err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, 0, fmt.Errorf("trailing JSON after bundle")
	}
	if err = tx.Commit(); err != nil {
		return nil, 0, err
	}
	return manifest, schema, nil
}

func decodeArray(decoder *json.Decoder, each func() error) error {
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return fmt.Errorf("expected array: %w", err)
	}
	for decoder.More() {
		if err = each(); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func decodeObject(decoder *json.Decoder, each func(string) error) error {
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("expected object: %w", err)
	}
	for decoder.More() {
		key, tokenErr := decoder.Token()
		if tokenErr != nil {
			return tokenErr
		}
		if err = each(key.(string)); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func validateStage(ctx context.Context, db *sql.DB) error {
	checks := []struct {
		query, message string
	}{
		{"SELECT count(*) FROM input_records", "bundle has no records"},
		{"SELECT count(*)-count(DISTINCT source_key) FROM input_records", "duplicate source key"},
		{"SELECT count(*)-count(DISTINCT source_key) FROM input_identities", "duplicate identity source"},
		{"SELECT count(*) FROM input_identities WHERE target=''", "empty identity anchor"},
		{`SELECT count(*) FROM input_identities a JOIN input_identities b ON a.target=b.source_key WHERE b.target<>a.target`, "identity chains are not allowed"},
		{"SELECT count(*) FROM input_rejections WHERE source_key='' OR reason='' OR NOT json_valid(raw)", "invalid rejection"},
		{"SELECT count(*) FROM input_rejections j JOIN input_records r USING(source_key)", "source is both accepted and rejected"},
	}
	for index, check := range checks {
		var count int
		if err := db.QueryRowContext(ctx, check.query).Scan(&count); err != nil {
			return err
		}
		if index == 0 && count == 0 || index > 0 && count != 0 {
			return fmt.Errorf("%s: %d", check.message, count)
		}
	}
	return nil
}

func canonicalIdentities(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, "SELECT source_key,target FROM input_identities ORDER BY source_key")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var out strings.Builder
	out.WriteByte('{')
	first := true
	for rows.Next() {
		var source, target string
		if err = rows.Scan(&source, &target); err != nil {
			return "", err
		}
		if !first {
			out.WriteByte(',')
		}
		first = false
		key, _ := json.Marshal(source)
		value, _ := json.Marshal(target)
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	out.WriteByte('}')
	return out.String(), nil
}
