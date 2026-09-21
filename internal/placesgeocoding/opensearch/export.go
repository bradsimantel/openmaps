package opensearch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/parquet-go/parquet-go"

	"openmaps/internal/importer"
	"openmaps/internal/places"
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
)

type ExportOptions struct {
	Generation string
	BatchDocs  int
	BatchBytes int
	Retries    int
	Workers    int
	Observe    func(ExportStats)
}

type ExportStats struct {
	Expected       int           `json:"expected"`
	Read           int           `json:"read"`
	Accepted       int           `json:"accepted"`
	Rejected       int           `json:"rejected"`
	Retried        int           `json:"retried"`
	Batches        int           `json:"batches"`
	Elapsed        time.Duration `json:"elapsed"`
	IndexedCount   int64         `json:"indexed_count"`
	MinimumFreeB   int64         `json:"minimum_free_bytes,omitempty"`
	FirstRejection string        `json:"first_rejection,omitempty"`
}

type entityRow struct {
	ID                string  `parquet:"id"`
	Kind              string  `parquet:"kind"`
	Name              string  `parquet:"name"`
	NormalizedName    string  `parquet:"normalized_name"`
	Address           string  `parquet:"address"`
	NormalizedAddress string  `parquet:"normalized_address"`
	NormalizedAliases string  `parquet:"normalized_aliases"`
	Website           string  `parquet:"website"`
	Subtype           string  `parquet:"subtype"`
	Lat               float64 `parquet:"lat"`
	Lng               float64 `parquet:"lng"`
	Closed            bool    `parquet:"closed"`
	Attributions      string  `parquet:"attributions"`
	EntityGroup       int64   `parquet:"entity_row_group"`
	EntityRow         int64   `parquet:"entity_row"`
	SourceStart       int64   `parquet:"source_start"`
	SourceCount       int64   `parquet:"source_count"`
	SourceFile        string  `parquet:"source_file"`
	ProvenanceStart   int64   `parquet:"provenance_start"`
	ProvenanceCount   int64   `parquet:"provenance_count"`
	ProvenanceFile    string  `parquet:"provenance_file"`
	AddressKey        string  `parquet:"address_key"`
	AddressContext    string  `parquet:"address_context"`
}

type sourceRow struct {
	EntityID   string `parquet:"entity_id"`
	SourceKey  string `parquet:"source_key"`
	Source     string `parquet:"source"`
	SourceID   string `parquet:"source_id"`
	Release    string `parquet:"release"`
	Priority   int64  `parquet:"priority"`
	Attributes string `parquet:"attributes"`
	Paths      string `parquet:"paths"`
	Raw        string `parquet:"raw"`
}

type sourceReader struct {
	name      string
	file      *os.File
	read      *parquet.GenericReader[sourceRow]
	readerPos int64
	bufferPos int64
	buffer    []sourceRow
}

func (r *sourceReader) close() error {
	if r.read == nil {
		return nil
	}
	return errors.Join(r.read.Close(), r.file.Close())
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
		r.name, r.file, r.read = name, f, parquet.NewGenericReader[sourceRow](f)
		r.readerPos, r.bufferPos, r.buffer = 0, 0, nil
	}
	if start != r.bufferPos || int64(len(r.buffer)) < count {
		if start != r.readerPos {
			if err := r.read.SeekToRow(start); err != nil {
				return nil, err
			}
			r.readerPos = start
		}
		readCount := 1024
		if count > int64(readCount) {
			readCount = int(count)
		}
		buffer := make([]sourceRow, readCount)
		n, err := r.read.Read(buffer)
		if n < int(count) || err != nil && err != io.EOF {
			return nil, fmt.Errorf("read source span %s:%d+%d: rows=%d: %w", name, start, count, n, err)
		}
		r.buffer = buffer[:n]
		r.bufferPos = start
		r.readerPos = start + int64(n)
	}
	rows := append([]sourceRow(nil), r.buffer[:count]...)
	r.buffer = r.buffer[count:]
	r.bufferPos += count
	return rows, nil
}

func (c *Client) Export(ctx context.Context, options ExportOptions) (stats ExportStats, err error) {
	started := time.Now()
	if options.BatchDocs <= 0 {
		options.BatchDocs = 1000
	}
	if options.BatchBytes <= 0 {
		options.BatchBytes = 8 << 20
	}
	if options.Retries <= 0 {
		options.Retries = 6
	}
	if options.Workers <= 0 {
		options.Workers = 4
	}
	if options.Workers > 16 {
		return stats, fmt.Errorf("export workers must not exceed 16")
	}
	manifest, err := placeduckdb.Verify(options.Generation)
	if err != nil {
		return stats, err
	}
	entityFiles := []placeduckdb.File{}
	for _, file := range manifest.Files {
		if file.Role == "entities" {
			stats.Expected += file.Rows
			entityFiles = append(entityFiles, file)
		}
	}
	if stats.Expected == 0 {
		return stats, fmt.Errorf("generation has no entities")
	}
	if err = c.CreateIndex(ctx); err != nil {
		return stats, err
	}
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan placeduckdb.File)
	errorsFound := make(chan error, options.Workers)
	var workers sync.WaitGroup
	var statsMu sync.Mutex
	merge := func(local ExportStats) {
		statsMu.Lock()
		defer statsMu.Unlock()
		stats.Read += local.Read
		stats.Accepted += local.Accepted
		stats.Rejected += local.Rejected
		stats.Retried += local.Retried
		stats.Batches += local.Batches
		if stats.FirstRejection == "" {
			stats.FirstRejection = local.FirstRejection
		}
	}
	for range options.Workers {
		workers.Add(1)
		go func() {
			defer workers.Done()
			local := ExportStats{}
			bulk := &bulkBuffer{client: c, options: options, stats: &local}
			var sources sourceReader
			defer sources.close()
			for shard := range jobs {
				if workerErr := c.streamEntityShard(workerCtx, options, shard, &sources, bulk, &local); workerErr != nil {
					errorsFound <- workerErr
					cancel()
					merge(local)
					return
				}
			}
			if workerErr := bulk.flush(workerCtx); workerErr != nil {
				errorsFound <- workerErr
				cancel()
			}
			merge(local)
		}()
	}
	for _, shard := range entityFiles {
		select {
		case jobs <- shard:
		case <-workerCtx.Done():
			break
		}
		if workerCtx.Err() != nil {
			break
		}
	}
	close(jobs)
	workers.Wait()
	close(errorsFound)
	for workerErr := range errorsFound {
		if err == nil {
			err = workerErr
		}
	}
	if err != nil {
		return stats, err
	}
	if stats.Read != stats.Expected || stats.Accepted != stats.Expected || stats.Rejected != 0 {
		return stats, fmt.Errorf("export accounting read=%d accepted=%d rejected=%d expected=%d: %s", stats.Read, stats.Accepted, stats.Rejected, stats.Expected, stats.FirstRejection)
	}
	if _, err = c.request(ctx, http.MethodPost, "/"+url.PathEscape(c.Index)+"/_refresh", "application/json", nil, nil); err != nil {
		return stats, err
	}
	var count struct {
		Count int64 `json:"count"`
	}
	if _, err = c.request(ctx, http.MethodGet, "/"+url.PathEscape(c.Index)+"/_count", "", nil, &count); err != nil {
		return stats, err
	}
	stats.IndexedCount = count.Count
	stats.Elapsed = time.Since(started)
	if count.Count != int64(stats.Expected) {
		return stats, fmt.Errorf("indexed count=%d expected=%d", count.Count, stats.Expected)
	}
	settings := []byte(`{"index":{"refresh_interval":"30s"}}`)
	if _, err = c.request(ctx, http.MethodPut, "/"+url.PathEscape(c.Index)+"/_settings", "application/json", settings, nil); err != nil {
		return stats, err
	}
	return stats, nil
}

func (c *Client) streamEntityShard(ctx context.Context, options ExportOptions, shard placeduckdb.File, sources *sourceReader, bulk *bulkBuffer, stats *ExportStats) error {
	file, err := os.Open(filepath.Join(options.Generation, shard.Name))
	if err != nil {
		return err
	}
	reader := parquet.NewGenericReader[entityRow](file)
	closeAll := func() error { return errors.Join(reader.Close(), file.Close()) }
	rows := make([]entityRow, 1024)
	lastID := ""
	for {
		n, readErr := reader.Read(rows)
		for i := 0; i < n; i++ {
			if ctx.Err() != nil {
				_ = closeAll()
				return ctx.Err()
			}
			row := rows[i]
			if row.ID <= lastID {
				_ = closeAll()
				return fmt.Errorf("entity IDs not strictly ordered in %s: %s after %s", shard.Name, row.ID, lastID)
			}
			lastID = row.ID
			sourceRows, sourceErr := sources.rows(options.Generation, row.SourceFile, row.SourceStart, row.SourceCount)
			if sourceErr != nil {
				_ = closeAll()
				return sourceErr
			}
			doc, projectErr := projectDocument(row, sourceRows)
			if projectErr != nil {
				_ = closeAll()
				return projectErr
			}
			stats.Read++
			if flushErr := bulk.add(ctx, doc); flushErr != nil {
				_ = closeAll()
				return flushErr
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			_ = closeAll()
			return readErr
		}
	}
	return closeAll()
}

func projectDocument(row entityRow, sources []sourceRow) (Document, error) {
	doc := Document{
		ID: row.ID, Kind: row.Kind, Subtype: row.Subtype, Name: row.Name,
		NormalizedName: row.NormalizedName, FormattedAddress: row.Address,
		NormalizedAddress: row.NormalizedAddress, Location: GeoPoint{Lat: row.Lat, Lon: row.Lng},
		Closed: row.Closed,
	}
	if row.NormalizedAliases != "" {
		doc.Aliases = []string{row.NormalizedAliases}
	} else {
		doc.Aliases = []string{}
	}
	sort.SliceStable(sources, func(i, j int) bool {
		if sources[i].Priority != sources[j].Priority {
			return sources[i].Priority > sources[j].Priority
		}
		return sources[i].SourceKey < sources[j].SourceKey
	})
	for _, source := range sources {
		if source.EntityID != row.ID {
			return doc, fmt.Errorf("source locator mismatch: got %s want %s", source.EntityID, row.ID)
		}
		if len(doc.Aliases) == 0 {
			var attrs map[string]json.RawMessage
			if err := json.Unmarshal([]byte(source.Attributes), &attrs); err != nil {
				return doc, err
			}
			var aliases []string
			_ = json.Unmarshal(attrs["aliases"], &aliases)
			if len(aliases) > 0 {
				doc.Aliases = aliases
			}
		}
		if doc.AreaProminence == 0 && doc.SettlementTier == 0 {
			evidence, evidenceErr := importer.AreaRankingEvidence(source.Source, json.RawMessage(source.Raw))
			if evidenceErr != nil {
				return doc, evidenceErr
			}
			doc.AreaProminence, doc.SettlementTier = evidence.Prominence, uint8(evidence.SettlementTier)
		}
		if doc.DestinationClass == 0 && doc.Specificity == 0 && doc.ConfidenceTier == 0 {
			evidence, evidenceErr := importer.PlaceRankingEvidence(source.Source, json.RawMessage(source.Raw))
			if evidenceErr != nil {
				return doc, evidenceErr
			}
			doc.DestinationClass, doc.Specificity = uint8(evidence.DestinationClass), evidence.Specificity
			doc.ConfidenceTier, doc.AreaOverride = uint8(evidence.ConfidenceTier), evidence.AreaOverride
		}
		if doc.Locality == "" && doc.Region == "" && doc.Country == "" {
			projectContext(&doc, source.Source, []byte(source.Raw))
		}
	}
	parts := []string{doc.Locality, doc.Region, doc.RegionCode, doc.Country, doc.PostalCode}
	doc.Hierarchy = strings.Join(nonempty(parts), " ")
	doc.SearchText = strings.Join(nonempty([]string{doc.NormalizedName, strings.Join(doc.Aliases, " "), doc.NormalizedAddress, places.Normalize(doc.Hierarchy)}), " ")
	return doc, nil
}

func projectContext(doc *Document, source string, raw []byte) {
	var feature struct {
		Properties struct {
			Postcode      string `json:"postcode"`
			Country       string `json:"country"`
			AddressLevels []struct {
				Value string `json:"value"`
			} `json:"address_levels"`
			Addresses []struct{ Locality, Region, Country, Postcode string } `json:"addresses"`
		} `json:"properties"`
	}
	if json.Unmarshal(raw, &feature) != nil {
		return
	}
	p := feature.Properties
	if len(p.Addresses) > 0 {
		a := p.Addresses[0]
		doc.Locality, doc.Region, doc.Country, doc.PostalCode = a.Locality, a.Region, a.Country, a.Postcode
		if len(a.Region) == 2 {
			doc.RegionCode = strings.ToUpper(a.Region)
		}
		return
	}
	doc.Country, doc.PostalCode = p.Country, p.Postcode
	if p.Country == "US" && len(p.AddressLevels) == 2 {
		doc.Region, doc.Locality = p.AddressLevels[0].Value, p.AddressLevels[1].Value
		if len(doc.Region) == 2 {
			doc.RegionCode = strings.ToUpper(doc.Region)
		}
	}
}

func nonempty(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

type bulkDocument struct {
	id     string
	source []byte
}
type bulkBuffer struct {
	client  *Client
	options ExportOptions
	stats   *ExportStats
	docs    []bulkDocument
	bytes   int
}

func (b *bulkBuffer) add(ctx context.Context, doc Document) error {
	source, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	b.docs = append(b.docs, bulkDocument{id: doc.ID, source: source})
	b.bytes += len(source) + len(doc.ID) + 32
	if len(b.docs) >= b.options.BatchDocs || b.bytes >= b.options.BatchBytes {
		return b.flush(ctx)
	}
	return nil
}

func (b *bulkBuffer) flush(ctx context.Context) error {
	if len(b.docs) == 0 {
		return nil
	}
	pending := append([]bulkDocument(nil), b.docs...)
	b.docs, b.bytes = b.docs[:0], 0
	for attempt := 0; len(pending) > 0 && attempt <= b.options.Retries; attempt++ {
		if attempt > 0 {
			b.stats.Retried += len(pending)
			delay := time.Duration(1<<min(attempt-1, 5)) * 100 * time.Millisecond
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
		next, err := b.send(ctx, pending)
		if err != nil {
			if !transientError(err) || attempt == b.options.Retries {
				return err
			}
			continue
		}
		pending = next
	}
	if len(pending) != 0 {
		return fmt.Errorf("bulk retries exhausted for %d documents", len(pending))
	}
	b.stats.Batches++
	if b.options.Observe != nil {
		b.options.Observe(*b.stats)
	}
	return nil
}

type statusError struct {
	status int
	err    error
}

func (e statusError) Error() string { return e.err.Error() }
func transientError(err error) bool {
	var status statusError
	return !errors.As(err, &status) || status.status == 429 || status.status == 502 || status.status == 503 || status.status == 504
}

func (b *bulkBuffer) send(ctx context.Context, docs []bulkDocument) ([]bulkDocument, error) {
	var body bytes.Buffer
	w := bufio.NewWriter(&body)
	for _, doc := range docs {
		meta, _ := json.Marshal(map[string]any{"index": map[string]string{"_id": doc.id}})
		w.Write(meta)
		w.WriteByte('\n')
		w.Write(doc.source)
		w.WriteByte('\n')
	}
	w.Flush()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.client.Endpoint+"/"+url.PathEscape(b.client.Index)+"/_bulk", bytes.NewReader(body.Bytes()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-ndjson")
	response, err := b.client.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		return nil, statusError{response.StatusCode, fmt.Errorf("bulk HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))}
	}
	var result struct {
		Items []map[string]struct {
			Status int             `json:"status"`
			Error  json.RawMessage `json:"error"`
		} `json:"items"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 64<<20)).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Items) != len(docs) {
		return nil, fmt.Errorf("bulk item count=%d want=%d", len(result.Items), len(docs))
	}
	retry := []bulkDocument{}
	for i, item := range result.Items {
		operation, ok := item["index"]
		if !ok {
			return nil, fmt.Errorf("bulk item %d has no index result", i)
		}
		switch {
		case operation.Status >= 200 && operation.Status < 300:
			b.stats.Accepted++
		case operation.Status == 429 || operation.Status == 502 || operation.Status == 503 || operation.Status == 504:
			retry = append(retry, docs[i])
		default:
			b.stats.Rejected++
			if b.stats.FirstRejection == "" {
				b.stats.FirstRejection = fmt.Sprintf("id=%s status=%d error=%s", docs[i].id, operation.Status, operation.Error)
			}
		}
	}
	return retry, nil
}
