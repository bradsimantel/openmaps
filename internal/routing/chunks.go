package routing

import (
	"bytes"
	"compress/flate"
	"context"
	"database/sql"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io"
)

// LayoutVersion is independent of the car profile and cost model. Each ordered
// chunk is a bounded gob slice of the named domain record type, DEFLATE encoded.
// There are no maps in binary records. The JSON manifest commits to every chunk.
const LayoutVersion = "routing-chunks-v1"
const PreprocessingVersion = "forced-chain-v1"
const chunkRecords = 4096
const maxChunkBytes = 32 << 20

type chunkRef struct {
	Kind    string `json:"kind"`
	Ordinal int    `json:"ordinal"`
	Count   int    `json:"count"`
	SHA256  string `json:"sha256"`
}
type graphManifest struct {
	Layout        string     `json:"layout"`
	Preprocessing string     `json:"preprocessing"`
	Metadata      Metadata   `json:"metadata"`
	Chunks        []chunkRef `json:"chunks"`
}

// WriteGraph writes only inside the caller's unpublished snapshot transaction.
func WriteGraph(ctx context.Context, tx *sql.Tx, d Data) error {
	if err := validateProvenance(d, func(f func(Source) error) error {
		for _, s := range d.Sources {
			if err := f(s); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TABLE routing_graph(id INTEGER PRIMARY KEY CHECK(id=1),data BLOB NOT NULL,sha256 TEXT NOT NULL); CREATE TABLE routing_chunks(kind TEXT NOT NULL,ordinal INTEGER NOT NULL,data BLOB NOT NULL,PRIMARY KEY(kind,ordinal)) WITHOUT ROWID`); err != nil {
		return err
	}
	m := graphManifest{Layout: LayoutVersion, Preprocessing: PreprocessingVersion, Metadata: d.Metadata}
	write := func(kind string, n int, slice func(int, int) any) error {
		for lo, ordinal := 0, 0; lo < n; lo, ordinal = lo+chunkRecords, ordinal+1 {
			hi := min(n, lo+chunkRecords)
			var raw bytes.Buffer
			w, _ := flate.NewWriter(&raw, flate.BestSpeed)
			if err := gob.NewEncoder(&boundedChunkWriter{writer: w}).Encode(slice(lo, hi)); err != nil {
				w.Close()
				return err
			}
			if err := w.Close(); err != nil {
				return err
			}
			if raw.Len() > maxChunkBytes {
				return fmt.Errorf("routing chunk exceeds size bound")
			}
			sum := Digest(raw.Bytes())
			if _, err := tx.ExecContext(ctx, "INSERT INTO routing_chunks VALUES(?,?,?)", kind, ordinal, raw.Bytes()); err != nil {
				return err
			}
			m.Chunks = append(m.Chunks, chunkRef{kind, ordinal, hi - lo, sum})
		}
		return nil
	}
	for _, err := range []error{
		write("nodes", len(d.Nodes), func(a, b int) any { return d.Nodes[a:b] }),
		write("segments", len(d.Segments), func(a, b int) any { return d.Segments[a:b] }),
		write("costs", len(d.Costs), func(a, b int) any { return d.Costs[a:b] }),
		write("bans", len(d.Bans), func(a, b int) any { return d.Bans[a:b] }),
		write("guards", len(d.Guards), func(a, b int) any { return d.Guards[a:b] }),
		write("access_ways", len(d.Access.Ways), func(a, b int) any { return d.Access.Ways[a:b] }),
		write("access_areas", len(d.Access.Areas), func(a, b int) any { return d.Access.Areas[a:b] }),
		write("access_entrances", len(d.Access.Entrances), func(a, b int) any { return d.Access.Entrances[a:b] }),
		write("sources", len(d.Sources), func(a, b int) any { return d.Sources[a:b] }),
	} {
		if err != nil {
			return err
		}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO routing_graph VALUES(1,?,?)", raw, Digest(raw))
	return err
}
func readManifest(ctx context.Context, db *sql.DB) (graphManifest, []byte, string, error) {
	var m graphManifest
	var raw []byte
	var sum string
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM routing_graph").Scan(&count); err != nil {
		return m, nil, "", err
	}
	if count != 1 {
		return m, nil, "", fmt.Errorf("routing graph must have one payload")
	}
	if err := db.QueryRowContext(ctx, "SELECT data,sha256 FROM routing_graph WHERE id=1").Scan(&raw, &sum); err != nil {
		return m, nil, "", err
	}
	if Digest(raw) != sum {
		return m, nil, "", fmt.Errorf("routing graph checksum mismatch")
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return m, nil, "", err
	}
	if m.Layout == "" {
		var chunks int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='routing_chunks'").Scan(&chunks); err != nil {
			return m, nil, "", err
		}
		if chunks != 0 {
			return m, nil, "", fmt.Errorf("legacy graph with unexpected chunk table")
		}
		return m, raw, sum, nil
	}
	if m.Layout != LayoutVersion || m.Preprocessing != PreprocessingVersion {
		return m, nil, "", fmt.Errorf("unsupported routing layout/preprocessing %q/%q", m.Layout, m.Preprocessing)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM routing_chunks").Scan(&count); err != nil {
		return m, nil, "", err
	}
	if count != len(m.Chunks) {
		return m, nil, "", fmt.Errorf("routing chunk count mismatch")
	}
	ord := map[string]int{}
	for _, c := range m.Chunks {
		switch c.Kind {
		case "nodes", "segments", "costs", "bans", "guards", "access_ways", "access_areas", "access_entrances", "sources":
		default:
			return m, nil, "", fmt.Errorf("unsupported routing chunk %q", c.Kind)
		}
		if c.Ordinal != ord[c.Kind] || c.Count < 1 || c.Count > chunkRecords || len(c.SHA256) != 64 {
			return m, nil, "", fmt.Errorf("invalid routing chunk manifest")
		}
		ord[c.Kind]++
	}
	return m, nil, sum, nil
}
func readChunk[T any](ctx context.Context, db *sql.DB, c chunkRef) ([]T, error) {
	var raw []byte
	if err := db.QueryRowContext(ctx, "SELECT data FROM routing_chunks WHERE kind=? AND ordinal=?", c.Kind, c.Ordinal).Scan(&raw); err != nil {
		return nil, err
	}
	if len(raw) > maxChunkBytes || Digest(raw) != c.SHA256 {
		return nil, fmt.Errorf("routing chunk checksum/size mismatch: %s/%d", c.Kind, c.Ordinal)
	}
	r := flate.NewReader(bytes.NewReader(raw))
	defer r.Close()
	decoded, err := io.ReadAll(io.LimitReader(r, maxChunkBytes+1))
	if err != nil {
		return nil, err
	}
	if len(decoded) > maxChunkBytes {
		return nil, fmt.Errorf("routing chunk decoded size exceeds bound")
	}
	var values []T
	dec := gob.NewDecoder(bytes.NewReader(decoded))
	if err := dec.Decode(&values); err != nil {
		return nil, err
	}
	if len(values) != c.Count {
		return nil, fmt.Errorf("routing chunk record count mismatch")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("trailing routing chunk data")
	}
	return values, nil
}
func appendChunk[T any](ctx context.Context, db *sql.DB, c chunkRef, to *[]T) error {
	v, err := readChunk[T](ctx, db, c)
	if err == nil {
		*to = append(*to, v...)
	}
	return err
}
func readGraphData(ctx context.Context, db *sql.DB, withSources bool) (Data, graphManifest, string, error) {
	m, raw, sum, err := readManifest(ctx, db)
	var d Data
	if err != nil {
		return d, m, sum, err
	}
	if m.Layout == "" {
		err = json.Unmarshal(raw, &d)
		return d, m, sum, err
	}
	d.Metadata = m.Metadata
	counts := map[string]int{}
	for _, c := range m.Chunks {
		counts[c.Kind] += c.Count
	}
	d.Nodes = make([]Node, 0, counts["nodes"])
	d.Segments = make([]Segment, 0, counts["segments"])
	d.Bans = make([]Ban, 0, counts["bans"])
	d.Guards = make([]Guard, 0, counts["guards"])
	d.Costs = make([]WayCost, 0, counts["costs"])
	d.Sources = []Source{}
	if withSources {
		d.Sources = make([]Source, 0, counts["sources"])
	}
	notesPresent := []string{"original cost assumptions retained in checked source chunks"}
	for _, c := range m.Chunks {
		switch c.Kind {
		case "nodes":
			err = appendChunk(ctx, db, c, &d.Nodes)
		case "segments":
			err = appendChunk(ctx, db, c, &d.Segments)
		case "costs":
			var values []WayCost
			values, err = readChunk[WayCost](ctx, db, c)
			if !withSources {
				for i := range values {
					if len(values[i].Forward.Notes) > 0 {
						values[i].Forward.Notes = notesPresent
					}
					if len(values[i].Backward.Notes) > 0 {
						values[i].Backward.Notes = notesPresent
					}
				}
			}
			d.Costs = append(d.Costs, values...)
		case "bans":
			err = appendChunk(ctx, db, c, &d.Bans)
		case "guards":
			err = appendChunk(ctx, db, c, &d.Guards)
		case "access_ways":
			err = appendChunk(ctx, db, c, &d.Access.Ways)
		case "access_areas":
			err = appendChunk(ctx, db, c, &d.Access.Areas)
		case "access_entrances":
			err = appendChunk(ctx, db, c, &d.Access.Entrances)
		case "sources":
			if withSources {
				err = appendChunk(ctx, db, c, &d.Sources)
			}
		}
		if err != nil {
			return d, m, sum, err
		}
	}
	return d, m, sum, nil
}

// ReadData is the offline inspection/rebuild interface. Runtime loading streams
// provenance independently; callers opting into ReadData retain full raw records.
func ReadData(ctx context.Context, db *sql.DB) (Data, error) {
	d, _, _, err := readGraphData(ctx, db, true)
	return d, err
}

// Reject an oversized logical chunk during writing, before it can produce an
// apparently valid compressed record which the bounded loader cannot read.
type boundedChunkWriter struct {
	writer  io.Writer
	written int
}

func (w *boundedChunkWriter) Write(p []byte) (int, error) {
	if len(p) > maxChunkBytes-w.written {
		return 0, fmt.Errorf("routing decoded chunk exceeds size bound")
	}
	n, err := w.writer.Write(p)
	w.written += n
	return n, err
}
