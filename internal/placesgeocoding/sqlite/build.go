package sqlite

import (
	"container/heap"
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/parquet-go/parquet-go"
	_ "modernc.org/sqlite"

	"openmaps/internal/importer"
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
)

//go:embed schema.sql
var schema string

const (
	ManifestName   = "manifest.json"
	IncompleteName = "INCOMPLETE.json"
	SchemaVersion  = 1
	defaultBatch   = 10_000
	shortHeadLimit = 64
)

type BuildOptions struct {
	Generation   string
	Output       string
	Shards       int
	BatchSize    int
	ReserveBytes int64
	Observe      func(BuildStats)
}

type BuildStats struct {
	Expected           int64         `json:"expected"`
	Attempted          int64         `json:"attempted"`
	Accepted           int64         `json:"accepted"`
	Rejected           int64         `json:"rejected"`
	Retried            int64         `json:"retried"`
	Final              int64         `json:"final"`
	Batches            int64         `json:"batches"`
	Started            time.Time     `json:"started"`
	Elapsed            time.Duration `json:"elapsed"`
	FinalizeElapsed    time.Duration `json:"finalize_elapsed"`
	MinimumFreeBytes   int64         `json:"minimum_free_bytes"`
	PeakTemporaryBytes int64         `json:"peak_temporary_bytes"`
	MainBytes          int64         `json:"main_bytes"`
	WALBytes           int64         `json:"wal_bytes"`
	JournalBytes       int64         `json:"journal_bytes"`
	SQLiteVersion      string        `json:"sqlite_version"`
	SQLiteSourceID     string        `json:"sqlite_source_id"`
	CompileOptions     []string      `json:"compile_options"`
	IntegrityCheck     string        `json:"integrity_check"`
	FTSIntegrityCheck  string        `json:"fts_integrity_check"`
	Error              string        `json:"error,omitempty"`
}

type Manifest struct {
	Schema               int         `json:"schema"`
	CoordinateOrder      string      `json:"coordinate_order"`
	SourcePath           string      `json:"source_path"`
	SourceManifestSHA256 string      `json:"source_manifest_sha256"`
	SourceEntities       int64       `json:"source_entities"`
	Shards               []ShardFile `json:"shards"`
	BuildPragmas         []string    `json:"build_pragmas"`
	ServingPragmas       []string    `json:"serving_pragmas"`
	Stats                BuildStats  `json:"stats"`
}

type ShardFile struct {
	Name, SHA256          string
	Rows                  int64
	Bytes, AllocatedBytes int64
}

var buildPragmas = []string{
	"journal_mode=WAL", "synchronous=FULL", "foreign_keys=ON", "temp_store=FILE",
	"cache_size=-262144", "wal_autocheckpoint=10000", "mmap_size=0",
}
var servingPragmas = []string{
	"mode=ro", "immutable=1", "query_only=ON", "foreign_keys=ON", "temp_store=MEMORY",
	"cache_size=-65536", "mmap_size=0",
}

type sourceReader struct {
	name             string
	file             *os.File
	reader           *parquet.GenericReader[sourceRow]
	pos, bufferStart int64
	buffer           []sourceRow
}

func (r *sourceReader) close() error {
	if r.reader == nil {
		return nil
	}
	return errors.Join(r.reader.Close(), r.file.Close())
}
func (r *sourceReader) rows(generation, name string, start, count int64) ([]sourceRow, error) {
	if r.name != name {
		if err := r.close(); err != nil {
			return nil, err
		}
		f, err := os.Open(filepath.Join(generation, name))
		if err != nil {
			return nil, err
		}
		r.name, r.file, r.reader, r.pos, r.bufferStart, r.buffer = name, f, parquet.NewGenericReader[sourceRow](f), 0, 0, nil
	}
	if start != r.bufferStart || int64(len(r.buffer)) < count {
		if start != r.pos {
			if err := r.reader.SeekToRow(start); err != nil {
				return nil, err
			}
			r.pos = start
		}
		n := 1024
		if count > int64(n) {
			n = int(count)
		}
		buffer := make([]sourceRow, n)
		got, err := r.reader.Read(buffer)
		if got < int(count) || err != nil && err != io.EOF {
			return nil, fmt.Errorf("read source span %s:%d+%d: rows=%d: %w", name, start, count, got, err)
		}
		r.buffer, r.bufferStart, r.pos = buffer[:got], start, start+int64(got)
	}
	out := append([]sourceRow(nil), r.buffer[:count]...)
	r.buffer, r.bufferStart = r.buffer[count:], r.bufferStart+count
	return out, nil
}

type headCandidate struct {
	rowid int64
	doc   document
}
type candidateHeap []headCandidate

func (h candidateHeap) Len() int           { return len(h) }
func (h candidateHeap) Less(i, j int) bool { return betterHead(h[j], h[i]) } // worst at root
func (h candidateHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *candidateHeap) Push(x any)        { *h = append(*h, x.(headCandidate)) }
func (h *candidateHeap) Pop() any          { old := *h; x := old[len(old)-1]; *h = old[:len(old)-1]; return x }
func betterHead(a, b headCandidate) bool {
	if a.doc.Closed != b.doc.Closed {
		return !a.doc.Closed
	}
	ka, kb := kindRank(a.doc.Kind), kindRank(b.doc.Kind)
	if ka != kb {
		return ka < kb
	}
	if a.doc.AreaProminence != b.doc.AreaProminence {
		return a.doc.AreaProminence > b.doc.AreaProminence
	}
	if a.doc.ConfidenceTier != b.doc.ConfidenceTier {
		return a.doc.ConfidenceTier > b.doc.ConfidenceTier
	}
	return a.doc.ID < b.doc.ID
}

type shardBuilder struct {
	index int
	path  string
	db    *sql.DB
	heads map[string]*candidateHeap
	stats BuildStats
}

func Build(ctx context.Context, options BuildOptions) (stats BuildStats, err error) {
	stats.Started = time.Now().UTC()
	stats.MinimumFreeBytes = 1 << 62
	if options.Shards <= 0 {
		options.Shards = 4
	}
	if options.Shards > 16 {
		return stats, fmt.Errorf("shards must be 1..16")
	}
	if options.BatchSize <= 0 {
		options.BatchSize = defaultBatch
	}
	if options.Generation == "" || options.Output == "" {
		return stats, fmt.Errorf("generation and output are required")
	}
	if _, e := os.Stat(options.Output); e == nil {
		return stats, fmt.Errorf("output exists: %s", options.Output)
	} else if !os.IsNotExist(e) {
		return stats, e
	}
	manifest, err := placeduckdb.Verify(options.Generation)
	if err != nil {
		return stats, err
	}
	for _, f := range manifest.Files {
		if f.Role == "entities" {
			stats.Expected += int64(f.Rows)
		}
	}
	if stats.Expected == 0 {
		return stats, fmt.Errorf("source generation has no entities")
	}
	if err = os.MkdirAll(options.Output, 0755); err != nil {
		return stats, err
	}
	incomplete := map[string]any{"status": "incomplete", "started": stats.Started, "source": options.Generation, "expected": stats.Expected}
	if err = importer.WriteJSON(filepath.Join(options.Output, IncompleteName), incomplete); err != nil {
		return stats, err
	}
	defer func() {
		stats.Elapsed = time.Since(stats.Started)
		if err != nil {
			stats.Error = err.Error()
			_ = importer.WriteJSON(filepath.Join(options.Output, IncompleteName), stats)
		}
	}()
	files := []placeduckdb.File{}
	for _, f := range manifest.Files {
		if f.Role == "entities" {
			files = append(files, f)
		}
	}
	builders := make([]*shardBuilder, options.Shards)
	for i := range builders {
		b, e := newShardBuilder(ctx, options.Output, i)
		if e != nil {
			return stats, e
		}
		builders[i] = b
		defer b.db.Close()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make([]chan placeduckdb.File, len(builders))
	errs := make(chan error, len(builders))
	var wg sync.WaitGroup
	var mu sync.Mutex
	for i, b := range builders {
		jobs[i] = make(chan placeduckdb.File)
		wg.Add(1)
		go func(b *shardBuilder, ch <-chan placeduckdb.File) {
			defer wg.Done()
			var sr sourceReader
			defer sr.close()
			for f := range ch {
				if e := b.addFile(ctx, options.Generation, f, options.BatchSize, func() error {
					mu.Lock()
					mergeStats(&stats, b.stats)
					b.stats = BuildStats{}
					sampleErr := sampleBuild(options.Output, &stats, options.ReserveBytes)
					snapshot := stats
					mu.Unlock()
					if options.Observe != nil {
						options.Observe(snapshot)
					}
					return sampleErr
				}); e != nil {
					errs <- e
					cancel()
					return
				}
			}
		}(b, jobs[i])
	}
	for i, f := range files {
		select {
		case jobs[i%len(jobs)] <- f:
		case <-ctx.Done():
			break
		}
		if ctx.Err() != nil {
			break
		}
	}
	for _, ch := range jobs {
		close(ch)
	}
	wg.Wait()
	close(errs)
	mu.Lock()
	for _, b := range builders {
		mergeStats(&stats, b.stats)
		b.stats = BuildStats{}
	}
	if sampleErr := sampleBuild(options.Output, &stats, options.ReserveBytes); sampleErr != nil && err == nil {
		err = sampleErr
	}
	mu.Unlock()
	for e := range errs {
		if err == nil {
			err = e
		}
	}
	if err != nil {
		return stats, err
	}
	finalStarted := time.Now()
	for _, b := range builders {
		if e := b.finalize(ctx, &stats); e != nil {
			return stats, e
		}
	}
	stats.FinalizeElapsed = time.Since(finalStarted)
	if stats.Attempted != stats.Expected || stats.Accepted != stats.Expected || stats.Rejected != 0 {
		return stats, fmt.Errorf("export accounting attempted=%d accepted=%d rejected=%d expected=%d", stats.Attempted, stats.Accepted, stats.Rejected, stats.Expected)
	}
	sourceDigest, err := importer.Checksum(filepath.Join(options.Generation, placeduckdb.ManifestName))
	if err != nil {
		return stats, err
	}
	stats.Elapsed = time.Since(stats.Started)
	outManifest := Manifest{Schema: SchemaVersion, CoordinateOrder: "longitude,latitude", SourcePath: options.Generation, SourceManifestSHA256: sourceDigest, SourceEntities: stats.Expected, BuildPragmas: buildPragmas, ServingPragmas: servingPragmas, Stats: stats}
	for _, b := range builders {
		count, e := countRows(b.path)
		if e != nil {
			return stats, e
		}
		stats.Final += count
		digest, e := importer.Checksum(b.path)
		if e != nil {
			return stats, e
		}
		st, e := os.Stat(b.path)
		if e != nil {
			return stats, e
		}
		allocated := allocatedBytes(st)
		outManifest.Shards = append(outManifest.Shards, ShardFile{Name: filepath.Base(b.path), SHA256: digest, Rows: count, Bytes: st.Size(), AllocatedBytes: allocated})
		stats.MainBytes += st.Size()
	}
	if stats.Final != stats.Expected {
		return stats, fmt.Errorf("final count=%d expected=%d", stats.Final, stats.Expected)
	}
	outManifest.Stats = stats
	if err = importer.WriteJSON(filepath.Join(options.Output, ManifestName), outManifest); err != nil {
		return stats, err
	}
	if err = os.Remove(filepath.Join(options.Output, IncompleteName)); err != nil {
		return stats, err
	}
	return stats, nil
}

func mergeStats(dst *BuildStats, src BuildStats) {
	dst.Attempted += src.Attempted
	dst.Accepted += src.Accepted
	dst.Rejected += src.Rejected
	dst.Retried += src.Retried
	dst.Batches += src.Batches
}
func sampleBuild(path string, stats *BuildStats, reserve int64) error {
	var fs syscall.Statfs_t
	if syscall.Statfs(path, &fs) == nil {
		free := int64(fs.Bavail) * int64(fs.Bsize)
		if free < stats.MinimumFreeBytes {
			stats.MinimumFreeBytes = free
		}
		if reserve > 0 && free < reserve {
			return fmt.Errorf("disk reserve violated: free=%d reserve=%d", free, reserve)
		}
	}
	entries, _ := os.ReadDir(path)
	var aux, wal, journal int64
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), "-wal") || strings.HasSuffix(e.Name(), "-journal") || strings.Contains(e.Name(), "-tmp") {
			if st, x := e.Info(); x == nil {
				aux += st.Size()
				if strings.HasSuffix(e.Name(), "-wal") {
					wal += st.Size()
				}
				if strings.HasSuffix(e.Name(), "-journal") {
					journal += st.Size()
				}
			}
		}
	}
	if aux > stats.PeakTemporaryBytes {
		stats.PeakTemporaryBytes = aux
	}
	if wal > stats.WALBytes {
		stats.WALBytes = wal
	}
	if journal > stats.JournalBytes {
		stats.JournalBytes = journal
	}
	return nil
}
func allocatedBytes(st os.FileInfo) int64 {
	if raw, ok := st.Sys().(*syscall.Stat_t); ok {
		return raw.Blocks * 512
	}
	return st.Size()
}

func newShardBuilder(ctx context.Context, dir string, index int) (*shardBuilder, error) {
	path := filepath.Join(dir, fmt.Sprintf("serving-%03d.sqlite", index))
	dsn := "file:" + path
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	b := &shardBuilder{index: index, path: path, db: db, heads: map[string]*candidateHeap{}}
	for _, p := range buildPragmas {
		if _, err = db.ExecContext(ctx, "PRAGMA "+p); err != nil {
			db.Close()
			return nil, fmt.Errorf("pragma %s: %w", p, err)
		}
	}
	if _, err = db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO metadata VALUES('shard',?),('shard_count','pending')", fmt.Sprint(index)); err != nil {
		db.Close()
		return nil, err
	}
	return b, nil
}

func (b *shardBuilder) addFile(ctx context.Context, generation string, file placeduckdb.File, batch int, observe func() error) error {
	f, err := os.Open(filepath.Join(generation, file.Name))
	if err != nil {
		return err
	}
	reader := parquet.NewGenericReader[entityRow](f)
	defer func() { reader.Close(); f.Close() }()
	rows := make([]entityRow, 1024)
	var sr sourceReader
	defer sr.close()
	pending := make([]document, 0, batch)
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		if err := b.insertBatch(ctx, pending); err != nil {
			return err
		}
		pending = pending[:0]
		b.stats.Batches++
		return observe()
	}
	last := ""
	for {
		n, re := reader.Read(rows)
		for i := 0; i < n; i++ {
			row := rows[i]
			b.stats.Attempted++
			if row.ID <= last {
				b.stats.Rejected++
				return fmt.Errorf("entity IDs not strictly ordered in %s", file.Name)
			}
			last = row.ID
			sources, e := sr.rows(generation, row.SourceFile, row.SourceStart, row.SourceCount)
			if e != nil {
				b.stats.Rejected++
				return e
			}
			doc, e := project(row, sources)
			if e != nil {
				b.stats.Rejected++
				return e
			}
			pending = append(pending, doc)
			if len(pending) >= batch {
				if e = flush(); e != nil {
					return e
				}
			}
		}
		if re == io.EOF {
			break
		}
		if re != nil {
			return re
		}
	}
	return flush()
}

func (b *shardBuilder) insertBatch(ctx context.Context, docs []document) error {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	entitySQL := `INSERT INTO entities(id,kind,subtype,name,normalized_name,aliases,formatted_address,normalized_address,locality,region,region_code,country,postal_code,hierarchy,lat,lng,closed,area_prominence,settlement_tier,destination_class,specificity,confidence_tier,area_override) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	e, err := tx.PrepareContext(ctx, entitySQL)
	if err != nil {
		return err
	}
	defer e.Close()
	f, err := tx.PrepareContext(ctx, "INSERT INTO entity_fts(rowid,name,aliases,address,hierarchy) VALUES(?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer f.Close()
	r, err := tx.PrepareContext(ctx, "INSERT INTO entity_rtree VALUES(?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer r.Close()
	for _, d := range docs {
		res, x := e.ExecContext(ctx, d.ID, d.Kind, d.Subtype, d.Name, d.NormalizedName, d.Aliases, d.FormattedAddress, d.NormalizedAddress, d.Locality, d.Region, d.RegionCode, d.Country, d.PostalCode, d.Hierarchy, d.Lat, d.Lng, d.Closed, d.AreaProminence, d.SettlementTier, d.DestinationClass, d.Specificity, d.ConfidenceTier, d.AreaOverride)
		if x != nil {
			b.stats.Rejected++
			return x
		}
		rowid, x := res.LastInsertId()
		if x != nil {
			return x
		}
		if _, x = f.ExecContext(ctx, rowid, d.NormalizedName, d.Aliases, d.NormalizedAddress, strings.ToLower(d.Hierarchy)); x != nil {
			b.stats.Rejected++
			return x
		}
		if _, x = r.ExecContext(ctx, rowid, d.Lng, d.Lng, d.Lat, d.Lat); x != nil {
			b.stats.Rejected++
			return x
		}
		b.addHeads(rowid, d)
		b.stats.Accepted++
	}
	return tx.Commit()
}

func (b *shardBuilder) addHeads(rowid int64, d document) {
	seen := map[string]bool{}
	for _, text := range []string{d.NormalizedName, d.Aliases} {
		for _, token := range strings.Fields(text) {
			r := []rune(token)
			for _, n := range []int{1, 2} {
				if len(r) < n {
					continue
				}
				p := string(r[:n])
				if !asciiPrefix(p) || seen[p] {
					continue
				}
				seen[p] = true
				h := b.heads[p]
				if h == nil {
					x := candidateHeap{}
					heap.Init(&x)
					h = &x
					b.heads[p] = h
				}
				c := headCandidate{rowid: rowid, doc: d}
				if h.Len() < shortHeadLimit {
					heap.Push(h, c)
				} else if betterHead(c, (*h)[0]) {
					heap.Pop(h)
					heap.Push(h, c)
				}
			}
		}
	}
}
func asciiPrefix(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return utf8.RuneCountInString(s) <= 2
}

func (b *shardBuilder) finalize(ctx context.Context, stats *BuildStats) error {
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(b.heads))
	for k := range b.heads {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	stmt, err := tx.PrepareContext(ctx, "INSERT INTO short_prefix_head VALUES(?,?,?)")
	if err != nil {
		return err
	}
	for _, k := range keys {
		h := b.heads[k]
		items := make([]headCandidate, len(*h))
		copy(items, *h)
		sort.Slice(items, func(i, j int) bool { return betterHead(items[i], items[j]) })
		for rank, c := range items {
			if _, err = stmt.ExecContext(ctx, k, rank, c.rowid); err != nil {
				return err
			}
		}
	}
	stmt.Close()
	if err = tx.Commit(); err != nil {
		return err
	}
	b.heads = nil
	if _, err = b.db.ExecContext(ctx, "INSERT INTO entity_fts(entity_fts,rank) VALUES('rank','bm25(10.0,5.0,1.0,2.0)')"); err != nil {
		return err
	}
	if _, err = b.db.ExecContext(ctx, "INSERT INTO entity_fts(entity_fts) VALUES('integrity-check')"); err != nil {
		return err
	}
	stats.FTSIntegrityCheck = "ok"
	if _, err = b.db.ExecContext(ctx, "PRAGMA optimize"); err != nil {
		return err
	}
	var check string
	if err = b.db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&check); err != nil {
		return err
	}
	if check != "ok" {
		return fmt.Errorf("integrity check: %s", check)
	}
	stats.IntegrityCheck = "ok"
	if stats.SQLiteVersion == "" {
		_ = b.db.QueryRowContext(ctx, "SELECT sqlite_version(),sqlite_source_id()").Scan(&stats.SQLiteVersion, &stats.SQLiteSourceID)
		rows, e := b.db.QueryContext(ctx, "PRAGMA compile_options")
		if e == nil {
			for rows.Next() {
				var x string
				if rows.Scan(&x) == nil {
					stats.CompileOptions = append(stats.CompileOptions, x)
				}
			}
			rows.Close()
			sort.Strings(stats.CompileOptions)
		}
	}
	if _, err = b.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return err
	}
	if _, err = b.db.ExecContext(ctx, "PRAGMA journal_mode=DELETE"); err != nil {
		return err
	}
	return b.db.Close()
}

func countRows(path string) (int64, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&immutable=1")
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var n int64
	err = db.QueryRow("SELECT count(*) FROM entities").Scan(&n)
	return n, err
}
