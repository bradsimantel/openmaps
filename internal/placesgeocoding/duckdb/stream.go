package duckdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	duckdbdriver "github.com/duckdb/duckdb-go/v2"
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
	return buildStaged(ctx, path, "128MB", "256MB", 1, 1, "", nil, func(stage *sql.DB) (json.RawMessage, int, error) {
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
	return appendStageBatch(ctx, w.db, "input_records", len(rows), func(i int) ([]driver.Value, error) {
		raw, err := json.Marshal(rows[i])
		if err != nil {
			return nil, err
		}
		return []driver.Value{rows[i].Key(), rows[i].Kind, int64(rows[i].Priority), string(raw)}, nil
	})
}

func (w stageWriter) WriteRelationships(ctx context.Context, rows []importer.Relationship) error {
	return appendStageBatch(ctx, w.db, "input_relationships", len(rows), func(i int) ([]driver.Value, error) {
		return []driver.Value{rows[i].From, rows[i].To, rows[i].Kind, rows[i].Evidence}, nil
	})
}

func (w stageWriter) WriteRejections(ctx context.Context, rows []importer.Rejection) error {
	return appendStageBatch(ctx, w.db, "input_rejections", len(rows), func(i int) ([]driver.Value, error) {
		return []driver.Value{rows[i].SourceKey, rows[i].Reason, string(rows[i].Raw)}, nil
	})
}

func appendStageBatch(ctx context.Context, db *sql.DB, table string, count int, values func(int) ([]driver.Value, error)) error {
	if count == 0 {
		return nil
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(raw any) error {
		driverConn, ok := raw.(driver.Conn)
		if !ok {
			return fmt.Errorf("DuckDB driver connection does not implement database/sql/driver.Conn")
		}
		appender, appendErr := duckdbdriver.NewAppenderFromConn(driverConn, "main", table)
		if appendErr != nil {
			return appendErr
		}
		fail := func(primary error) error {
			return errors.Join(primary, appender.Clear(), appender.Close())
		}
		for i := 0; i < count; i++ {
			if contextErr := ctx.Err(); contextErr != nil {
				return fail(contextErr)
			}
			row, rowErr := values(i)
			if rowErr != nil {
				return fail(rowErr)
			}
			if rowErr = appender.AppendRow(row...); rowErr != nil {
				return fail(rowErr)
			}
		}
		return appender.CloseWithCancel(ctx)
	})
}

// BuildStream constructs a generation directly from a provider adapter. It is
// the national-safe counterpart to BuildJSON: both paths share the same
// bounded DuckDB normalization and Parquet/catalog publication code.
func BuildStream(ctx context.Context, path string, manifest json.RawMessage, identities map[string]string, memoryLimit, catalogMemoryLimit string, observe func(string, time.Duration), produce func(context.Context, StreamWriter) error) error {
	return BuildStreamConfigured(ctx, path, manifest, identities, memoryLimit, catalogMemoryLimit, 1, 1, "", observe, produce)
}

// BuildStreamConfigured is BuildStream with explicit DuckDB worker counts.
// Sorting at every publication boundary keeps normalized Parquet deterministic
// even when staging and catalog construction use multiple workers.
func BuildStreamConfigured(ctx context.Context, path string, manifest json.RawMessage, identities map[string]string, memoryLimit, catalogMemoryLimit string, databaseThreads, catalogThreads int, expectedDataSHA256 string, observe func(string, time.Duration), produce func(context.Context, StreamWriter) error) error {
	if produce == nil {
		return fmt.Errorf("stream producer is required")
	}
	_, err := BuildStreamResumable(ctx, path, manifest, identities, memoryLimit, catalogMemoryLimit, databaseThreads, catalogThreads, expectedDataSHA256, StreamCheckpointOptions{}, observe, func(ctx context.Context, writer StreamWriter) (json.RawMessage, error) {
		return nil, produce(ctx, writer)
	})
	return err
}

// StreamCheckpointOptions enables an explicit, durable boundary between input
// staging and normalization. BuildIdentity must identify an exact clean build
// revision and target architecture; callers are responsible for constructing it.
type StreamCheckpointOptions struct {
	Path          string
	Resume        bool
	BuildIdentity string
}

type streamCheckpointIdentity struct {
	OutputPath         string `json:"output_path"`
	ManifestSHA256     string `json:"manifest_sha256"`
	IdentitiesSHA256   string `json:"identities_sha256"`
	MemoryLimit        string `json:"memory_limit"`
	CatalogMemoryLimit string `json:"catalog_memory_limit"`
	DatabaseThreads    int    `json:"database_threads"`
	CatalogThreads     int    `json:"catalog_threads"`
	ExpectedDataSHA256 string `json:"expected_data_sha256,omitempty"`
	BuildIdentity      string `json:"build_identity"`
}

type streamCheckpoint struct {
	Schema              int                      `json:"schema"`
	Identity            streamCheckpointIdentity `json:"identity"`
	TableRows           map[string]int64         `json:"table_rows"`
	InputStagingSeconds float64                  `json:"input_staging_seconds"`
	State               json.RawMessage          `json:"state,omitempty"`
}

type stagedInput struct {
	manifest json.RawMessage
	schema   int
	state    json.RawMessage
}

const streamCheckpointName = "input-staging-checkpoint.json"

// BuildStreamResumable is BuildStreamConfigured with an optional durable input
// checkpoint. A resumed build reuses only fully checkpointed input tables; all
// derived normalization and catalog files are rebuilt and reverified.
func BuildStreamResumable(ctx context.Context, path string, manifest json.RawMessage, identities map[string]string, memoryLimit, catalogMemoryLimit string, databaseThreads, catalogThreads int, expectedDataSHA256 string, checkpoint StreamCheckpointOptions, observe func(string, time.Duration), produce func(context.Context, StreamWriter) (json.RawMessage, error)) (json.RawMessage, error) {
	if len(manifest) == 0 || !json.Valid(manifest) || memoryLimit == "" || catalogMemoryLimit == "" || produce == nil {
		return nil, fmt.Errorf("valid manifest, memory limits and stream producer are required")
	}
	if databaseThreads < 1 || databaseThreads > 64 || catalogThreads < 1 || catalogThreads > 64 {
		return nil, fmt.Errorf("database and catalog threads must be 1..64")
	}
	if expectedDataSHA256 != "" && (len(expectedDataSHA256) != 64 || strings.Trim(expectedDataSHA256, "0123456789abcdef") != "") {
		return nil, fmt.Errorf("expected data SHA-256 must be lowercase hexadecimal")
	}
	if checkpoint.Resume && checkpoint.Path == "" {
		return nil, fmt.Errorf("resume requires a checkpoint path")
	}
	if checkpoint.Path != "" && checkpoint.BuildIdentity == "" {
		return nil, fmt.Errorf("checkpoint build identity is required")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, manifest); err != nil {
		return nil, err
	}
	manifest = compact.Bytes()
	identityJSON, err := json.Marshal(identities)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	manifestDigest := sha256.Sum256(manifest)
	identitiesDigest := sha256.Sum256(identityJSON)
	checkpointIdentity := streamCheckpointIdentity{
		OutputPath: abs, ManifestSHA256: fmt.Sprintf("%x", manifestDigest), IdentitiesSHA256: fmt.Sprintf("%x", identitiesDigest),
		MemoryLimit: memoryLimit, CatalogMemoryLimit: catalogMemoryLimit, DatabaseThreads: databaseThreads,
		CatalogThreads: catalogThreads, ExpectedDataSHA256: expectedDataSHA256, BuildIdentity: checkpoint.BuildIdentity,
	}
	var checkpointState json.RawMessage
	err = buildStagedResumable(ctx, path, memoryLimit, catalogMemoryLimit, databaseThreads, catalogThreads, expectedDataSHA256, checkpoint, checkpointIdentity, manifest, &checkpointState, observe, func(stage *sql.DB) (stagedInput, error) {
		writer := stageWriter{db: stage}
		identityRows := make([][2]string, 0, len(identities))
		for source, target := range identities {
			identityRows = append(identityRows, [2]string{source, target})
		}
		if err := appendStageBatch(ctx, stage, "input_identities", len(identityRows), func(i int) ([]driver.Value, error) {
			return []driver.Value{identityRows[i][0], identityRows[i][1]}, nil
		}); err != nil {
			return stagedInput{}, err
		}
		state, err := produce(ctx, writer)
		if err != nil {
			return stagedInput{}, err
		}
		if len(state) != 0 && !json.Valid(state) {
			return stagedInput{}, fmt.Errorf("checkpoint state must be valid JSON")
		}
		return stagedInput{manifest: manifest, schema: 1, state: state}, nil
	})
	return checkpointState, err
}

func buildStaged(ctx context.Context, path, memoryLimit, catalogMemoryLimit string, databaseThreads, catalogThreads int, expectedDataSHA256 string, observe func(string, time.Duration), stageInput func(*sql.DB) (json.RawMessage, int, error)) (err error) {
	err = buildStagedResumable(ctx, path, memoryLimit, catalogMemoryLimit, databaseThreads, catalogThreads, expectedDataSHA256, StreamCheckpointOptions{}, streamCheckpointIdentity{}, nil, nil, observe, func(stage *sql.DB) (stagedInput, error) {
		manifest, schema, stageErr := stageInput(stage)
		return stagedInput{manifest: manifest, schema: schema}, stageErr
	})
	return err
}

func buildStagedResumable(ctx context.Context, path, memoryLimit, catalogMemoryLimit string, databaseThreads, catalogThreads int, expectedDataSHA256 string, checkpointOptions StreamCheckpointOptions, checkpointIdentity streamCheckpointIdentity, resumeManifest json.RawMessage, checkpointState *json.RawMessage, observe func(string, time.Duration), stageInput func(*sql.DB) (stagedInput, error)) (err error) {
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
	temp := ""
	preserveCheckpoint := false
	var savedCheckpoint streamCheckpoint
	if checkpointOptions.Path == "" {
		temp, err = os.MkdirTemp(filepath.Dir(abs), ".duckdb-generation-*")
		if err != nil {
			return err
		}
	} else {
		checkpointPath, pathErr := filepath.Abs(checkpointOptions.Path)
		if pathErr != nil {
			return pathErr
		}
		if checkpointPath == abs || filepath.Dir(checkpointPath) != filepath.Dir(abs) {
			return fmt.Errorf("checkpoint and output must be distinct sibling paths")
		}
		buildingPath := checkpointPath + ".building"
		if checkpointOptions.Resume {
			if _, statErr := os.Stat(buildingPath); statErr == nil {
				return fmt.Errorf("incomplete checkpoint staging directory requires inspection: %s", buildingPath)
			} else if !os.IsNotExist(statErr) {
				return statErr
			}
			if err = readStreamCheckpoint(checkpointPath, &savedCheckpoint); err != nil {
				return err
			}
			if savedCheckpoint.Schema != 1 || !reflect.DeepEqual(savedCheckpoint.Identity, checkpointIdentity) {
				return fmt.Errorf("checkpoint identity does not match this build")
			}
			temp = checkpointPath
			preserveCheckpoint = true
		} else {
			for _, reserved := range []string{checkpointPath, buildingPath} {
				if _, statErr := os.Stat(reserved); statErr == nil {
					return fmt.Errorf("checkpoint path exists; resume it or choose a new path: %s", reserved)
				} else if !os.IsNotExist(statErr) {
					return statErr
				}
			}
			if err = os.Mkdir(buildingPath, 0700); err != nil {
				return err
			}
			temp = buildingPath
		}
	}
	defer func() {
		if !preserveCheckpoint {
			_ = os.RemoveAll(temp)
		}
		if err != nil && preserveCheckpoint {
			err = fmt.Errorf("%w; resumable checkpoint retained at %s", err, temp)
		}
	}()

	stagePath := func() string { return filepath.Join(temp, "normalize.duckdb") }
	spill := func() string { return filepath.Join(temp, "normalize-spill") }
	openStage := func() (*sql.DB, error) {
		dsn := stagePath() + "?threads=" + strconv.Itoa(databaseThreads) + "&memory_limit=" + url.QueryEscape(memoryLimit) + "&preserve_insertion_order=false&temp_directory=" + url.QueryEscape(spill())
		db, openErr := sql.Open("duckdb", dsn)
		if openErr == nil {
			db.SetMaxOpenConns(databaseThreads)
			db.SetMaxIdleConns(databaseThreads)
		}
		return db, openErr
	}
	stage, err := openStage()
	if err != nil {
		return err
	}
	defer func() {
		if stage != nil {
			_ = stage.Close()
		}
	}()
	var input stagedInput
	if !checkpointOptions.Resume {
		if _, err = stage.ExecContext(ctx, `SET autoinstall_known_extensions=false; SET autoload_known_extensions=false;
CREATE TABLE input_records(source_key VARCHAR,kind VARCHAR,priority INTEGER,record_json VARCHAR);
CREATE TABLE input_identities(source_key VARCHAR,target VARCHAR);
CREATE TABLE input_relationships(from_key VARCHAR,to_key VARCHAR,kind VARCHAR,evidence VARCHAR);
CREATE TABLE input_rejections(source_key VARCHAR,reason VARCHAR,raw VARCHAR);`); err != nil {
			return err
		}
		inputStarted := time.Now()
		input, err = stageInput(stage)
		if err != nil {
			return err
		}
		if _, err = stage.ExecContext(ctx, "CHECKPOINT"); err != nil {
			return fmt.Errorf("checkpoint normalization input: %w", err)
		}
		rows, countErr := checkpointTableRows(ctx, stage)
		if countErr != nil {
			return countErr
		}
		if err = stage.Close(); err != nil {
			return fmt.Errorf("close normalization input: %w", err)
		}
		stage = nil
		inputElapsed := time.Since(inputStarted)
		if checkpointOptions.Path != "" {
			savedCheckpoint = streamCheckpoint{Schema: 1, Identity: checkpointIdentity, TableRows: rows, InputStagingSeconds: inputElapsed.Seconds(), State: input.state}
			if err = importer.WriteJSON(filepath.Join(temp, streamCheckpointName), savedCheckpoint); err != nil {
				return fmt.Errorf("write input checkpoint marker: %w", err)
			}
			checkpointPath, _ := filepath.Abs(checkpointOptions.Path)
			if err = os.Rename(temp, checkpointPath); err != nil {
				return fmt.Errorf("publish input checkpoint: %w", err)
			}
			temp = checkpointPath
			preserveCheckpoint = true
		}
		if observe != nil {
			observe("input_staging", inputElapsed)
		}
	} else {
		input = stagedInput{manifest: resumeManifest, schema: 1, state: savedCheckpoint.State}
	}
	if checkpointState != nil {
		*checkpointState = input.state
	}
	if input.schema != 1 || len(input.manifest) == 0 || !json.Valid(input.manifest) {
		return fmt.Errorf("unsupported or empty bundle")
	}
	manifestRaw := input.manifest
	var scope importer.Manifest
	if err = json.Unmarshal(manifestRaw, &scope); err != nil {
		return err
	}
	// Input staging can overlap the provider preparation database. Reopen the
	// stage at this boundary so DuckDB and Go ingestion buffers are not carried
	// into normalization. A resume additionally discards only derived state from
	// the previous normalization attempt.
	if checkpointOptions.Resume {
		if _, err = stage.ExecContext(ctx, "SET autoinstall_known_extensions=false; SET autoload_known_extensions=false"); err != nil {
			return err
		}
		rows, countErr := checkpointTableRows(ctx, stage)
		if countErr != nil {
			return countErr
		}
		if !reflect.DeepEqual(rows, savedCheckpoint.TableRows) {
			return fmt.Errorf("checkpoint staging row counts changed")
		}
		if _, err = stage.ExecContext(ctx, "DROP TABLE IF EXISTS resolved_keys; CHECKPOINT"); err != nil {
			return fmt.Errorf("reset resumed normalization state: %w", err)
		}
	}
	if stage != nil {
		if err = stage.Close(); err != nil {
			return fmt.Errorf("close normalization input boundary: %w", err)
		}
		stage = nil
	}
	if checkpointOptions.Resume {
		if err = os.RemoveAll(spill()); err != nil {
			return fmt.Errorf("remove abandoned normalization spill: %w", err)
		}
	}
	debug.FreeOSMemory()
	if stage, err = openStage(); err != nil {
		return fmt.Errorf("reopen normalization input: %w", err)
	}
	if _, err = stage.ExecContext(ctx, "SET autoinstall_known_extensions=false; SET autoload_known_extensions=false"); err != nil {
		return err
	}
	if checkpointOptions.Resume && observe != nil {
		observe("input_staging", time.Duration(savedCheckpoint.InputStagingSeconds*float64(time.Second)))
	}
	generation := temp
	if checkpointOptions.Path != "" {
		generation, err = os.MkdirTemp(filepath.Dir(abs), ".duckdb-generation-*")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(generation) }()
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

	entities, err := newParquetSink[entityRow](filepath.Join(generation, EntitiesName))
	if err != nil {
		return err
	}
	defer entities.close()
	sources, err := newParquetSink[sourceRow](filepath.Join(generation, SourcesName))
	if err != nil {
		return err
	}
	defer sources.close()
	provenance, err := newParquetSink[provenanceRow](filepath.Join(generation, ProvenanceName))
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

	relationships, err := newParquetSink[relationshipRow](filepath.Join(generation, RelationshipsName))
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

	rejections, err := newParquetSink[rejectionRow](filepath.Join(generation, RejectionsName))
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
	if err = writeParquet(filepath.Join(generation, MetadataName), metadataRows); err != nil {
		return err
	}

	manifest := Manifest{Schema: 2, CoordinateOrder: "longitude,latitude"}
	var logical bytes.Buffer
	var dataLogical bytes.Buffer
	for _, set := range []struct {
		role  string
		files []shardFile
	}{{"entities", entities.files}, {"sources", sources.files}, {"provenance", provenance.files}, {"relationships", relationships.files}, {"rejections", rejections.files}, {"metadata", []shardFile{{name: MetadataName, rows: len(metadataRows)}}}} {
		for _, file := range set.files {
			digest, checksumErr := importer.Checksum(filepath.Join(generation, file.name))
			if checksumErr != nil {
				return checksumErr
			}
			manifest.Files = append(manifest.Files, File{Name: file.name, Role: set.role, SHA256: digest, Rows: file.rows})
			fmt.Fprintf(&logical, "%s\x00%s\x00%s\x00%d\n", set.role, file.name, digest, file.rows)
			if set.role != "metadata" {
				fmt.Fprintf(&dataLogical, "%s\x00%s\x00%s\x00%d\n", set.role, file.name, digest, file.rows)
			}
		}
	}
	logicalDigest := sha256.Sum256(logical.Bytes())
	manifest.NormalizedSHA256 = fmt.Sprintf("%x", logicalDigest)
	dataDigest := sha256.Sum256(dataLogical.Bytes())
	manifest.DataSHA256 = fmt.Sprintf("%x", dataDigest)
	if expectedDataSHA256 != "" && manifest.DataSHA256 != expectedDataSHA256 {
		return fmt.Errorf("normalized data checksum mismatch: got %s want %s; output was not published", manifest.DataSHA256, expectedDataSHA256)
	}
	if err = stage.Close(); err != nil {
		return fmt.Errorf("close normalization stage: %w", err)
	}
	stage = nil
	if checkpointOptions.Path == "" {
		_ = os.Remove(stagePath())
		_ = os.RemoveAll(spill())
	}
	if observe != nil {
		observe("normalization_parquet", time.Since(normalizeStarted))
	}
	catalogStarted := time.Now()
	if err = buildIndexJSONWithOptions(ctx, filepath.Join(generation, IndexName), filepath.Join(generation, "entities*.parquet"), entities.rows, manifestRaw, identitiesJSON, catalogMemoryLimit, catalogThreads, shardedSchema); err != nil {
		return fmt.Errorf("build serving catalog: %w", err)
	}
	indexDigest, err := importer.Checksum(filepath.Join(generation, IndexName))
	if err != nil {
		return err
	}
	manifest.Files = append(manifest.Files, File{Name: IndexName, Role: "serving", SHA256: indexDigest, Rows: entities.rows})
	if observe != nil {
		observe("catalog", time.Since(catalogStarted))
	}
	if err = importer.WriteJSON(filepath.Join(generation, ManifestName), manifest); err != nil {
		return err
	}
	verifyStarted := time.Now()
	if _, err = Verify(generation); err != nil {
		return err
	}
	if observe != nil {
		observe("validation", time.Since(verifyStarted))
	}
	if err = os.Rename(generation, abs); err != nil {
		return err
	}
	if checkpointOptions.Path != "" {
		preserveCheckpoint = false
		if err = os.RemoveAll(temp); err != nil {
			return fmt.Errorf("candidate published but checkpoint cleanup failed: %w", err)
		}
	}
	return nil
}

func readStreamCheckpoint(path string, checkpoint *streamCheckpoint) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("read checkpoint: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("checkpoint is not a directory: %s", path)
	}
	raw, err := os.ReadFile(filepath.Join(path, streamCheckpointName))
	if err != nil {
		return fmt.Errorf("read checkpoint marker: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(checkpoint); err != nil {
		return fmt.Errorf("decode checkpoint marker: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("trailing data in checkpoint marker")
	}
	if checkpoint.InputStagingSeconds <= 0 || len(checkpoint.State) != 0 && !json.Valid(checkpoint.State) {
		return fmt.Errorf("invalid checkpoint marker")
	}
	return nil
}

func checkpointTableRows(ctx context.Context, db *sql.DB) (map[string]int64, error) {
	tables := []string{"input_records", "input_identities", "input_relationships", "input_rejections"}
	rows := make(map[string]int64, len(tables))
	for _, table := range tables {
		var count int64
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			return nil, fmt.Errorf("count checkpoint table %s: %w", table, err)
		}
		rows[table] = count
	}
	return rows, nil
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
