package importer

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/format"
)

// rangeFile reads immutable remote Parquet assets without downloading the
// planet. A bounded block cache coalesces small column reads. If-Match prevents
// mixing versions, and a server ignoring Range is rejected before reading data.
type rangeFile struct {
	ctx       context.Context
	url, etag string
	size      int64
	mu        sync.Mutex
	blocks    map[int64][]byte
}

const rangeBlock int64 = 1 << 20

func openRange(ctx context.Context, source string) (*rangeFile, error) {
	req, e := http.NewRequestWithContext(ctx, "HEAD", source, nil)
	if e != nil {
		return nil, e
	}
	resp, e := downloadClient.Do(req)
	if e != nil {
		return nil, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.ContentLength <= 0 || resp.Header.Get("ETag") == "" {
		return nil, fmt.Errorf("remote asset requires length and ETag: %s (HTTP %d)", source, resp.StatusCode)
	}
	return &rangeFile{ctx: ctx, url: source, etag: resp.Header.Get("ETag"), size: resp.ContentLength, blocks: map[int64][]byte{}}, nil
}
func (r *rangeFile) ReadAt(p []byte, offset int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if offset < 0 {
		return 0, fmt.Errorf("negative offset")
	}
	n := 0
	for n < len(p) {
		if offset >= r.size {
			return n, io.EOF
		}
		start := offset / rangeBlock * rangeBlock
		block, ok := r.blocks[start]
		if !ok {
			end := min(start+rangeBlock, r.size) - 1
			req, e := http.NewRequestWithContext(r.ctx, "GET", r.url, nil)
			if e != nil {
				return n, e
			}
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
			req.Header.Set("If-Match", r.etag)
			req.Header.Set("Accept-Encoding", "identity")
			resp, e := downloadClient.Do(req)
			if e != nil {
				return n, e
			}
			expectedRange := fmt.Sprintf("bytes %d-%d/%d", start, end, r.size)
			if resp.StatusCode != 206 || resp.Header.Get("Content-Range") != expectedRange {
				resp.Body.Close()
				return n, fmt.Errorf("invalid range response: HTTP %d %q", resp.StatusCode, resp.Header.Get("Content-Range"))
			}
			block, e = io.ReadAll(io.LimitReader(resp.Body, end-start+2))
			resp.Body.Close()
			if e != nil {
				return n, e
			}
			if int64(len(block)) != end-start+1 {
				return n, io.ErrUnexpectedEOF
			}
			if len(r.blocks) >= 64 {
				clear(r.blocks)
			}
			r.blocks[start] = block
		}
		count := copy(p[n:], block[offset-start:])
		n += count
		offset += int64(count)
	}
	return n, nil
}

// FetchSources downloads missing locked artifacts. rewriteExports is only for
// deliberate maintainer migrations: it never changes release URLs automatically.
func FetchSources(ctx context.Context, m Manifest, dir string, rewriteExports bool) (Manifest, error) {
	if e := os.MkdirAll(dir, 0755); e != nil {
		return m, e
	}
	if m.Catalog == nil {
		return m, fmt.Errorf("Overture catalog must be pinned in the manifest")
	}
	if e := download(ctx, m.Catalog.URL, filepath.Join(dir, m.Catalog.File), m.Catalog.SHA256); e != nil {
		return m, e
	}
	for index, input := range m.Inputs {
		path := filepath.Join(dir, input.File)
		if strings.HasSuffix(input.File, ".osm.pbf") {
			if e := download(ctx, input.URL, path, input.SHA256); e != nil {
				return m, e
			}
			continue
		}
		if _, e := os.Stat(path); e == nil && !rewriteExports {
			if e = Verify(path, input.SHA256); e != nil {
				return m, e
			}
			continue
		} else if e != nil && !os.IsNotExist(e) {
			return m, e
		}
		kind := sourceKind(input.URL)
		if kind == "" || len(input.BBox) != 4 {
			return m, fmt.Errorf("invalid Overture input %s", input.File)
		}
		log.Printf("Extracting Overture %s release %s", kind, input.Release)
		features, e := extractOverture(ctx, filepath.Join(dir, m.Catalog.File), input, [4]float64(input.BBox))
		if e != nil {
			return m, e
		}
		doc := map[string]any{"type": "FeatureCollection", "features": features}
		data, e := json.Marshal(doc)
		if e != nil {
			return m, e
		}
		data = append(data, '\n')
		temp, e := os.CreateTemp(dir, ".overture-*")
		if e != nil {
			return m, e
		}
		name := temp.Name()
		temp.Close()
		if e = writeAtomic(name, data); e == nil && !rewriteExports {
			e = Verify(name, input.SHA256)
		}
		if e == nil {
			if rewriteExports {
				e = os.Rename(name, path)
			} else {
				e = os.Link(name, path)
			}
		}
		os.Remove(name)
		if e != nil {
			return m, e
		}
		if rewriteExports {
			digest, e := Checksum(path)
			if e != nil {
				return m, e
			}
			m.Inputs[index].SHA256 = digest
		}
		log.Printf("Extracted %d %s records", len(features), kind)
	}
	return m, nil
}
func extractOverture(ctx context.Context, catalogPath string, input Input, bounds [4]float64) ([]any, error) {
	assets, e := catalogAssets(catalogPath, sourceKind(input.URL), bounds)
	if e != nil {
		return nil, e
	}
	features := []any{}
	seen := map[string]bool{}
	for _, asset := range assets {
		if e = validateAsset(asset, input); e != nil {
			return nil, e
		}
		remote, e := openRange(ctx, asset)
		if e != nil {
			return nil, e
		}
		file, e := parquet.OpenFile(remote, remote.size, parquet.SkipPageIndex(true), parquet.SkipBloomFilters(true))
		if e != nil {
			return nil, e
		}
		for i, group := range file.RowGroups() {
			if !overlapsStats(file.Metadata().RowGroups[i], bounds) {
				continue
			}
			log.Printf("Reading %s row group %d", sourceKind(input.URL), i)
			reader := parquet.NewGenericRowGroupReader[any](group)
			rows := make([]any, 128)
			for {
				n, readErr := reader.Read(rows)
				for _, v := range rows[:n] {
					row := v.(map[string]any)
					if !overlapsMap(row["bbox"], bounds) {
						continue
					}
					f, e := parquetFeature(row, file.Schema())
					if e != nil {
						reader.Close()
						return nil, e
					}
					props := f["properties"].(map[string]any)
					id, ok := props["id"].(string)
					if !ok || id == "" || seen[id] {
						reader.Close()
						return nil, fmt.Errorf("missing or duplicate Overture ID: %v", props["id"])
					}
					seen[id] = true
					features = append(features, f)
				}
				if readErr != nil {
					reader.Close()
					if readErr != io.EOF {
						return nil, readErr
					}
					break
				}
				if e = ctx.Err(); e != nil {
					reader.Close()
					return nil, e
				}
			}
		}
	}
	if len(features) == 0 {
		return nil, fmt.Errorf("no Overture records found for %s", input.File)
	}
	sort.Slice(features, func(i, j int) bool {
		return features[i].(map[string]any)["properties"].(map[string]any)["id"].(string) < features[j].(map[string]any)["properties"].(map[string]any)["id"].(string)
	})
	return features, nil
}
func validateAsset(asset string, input Input) error {
	source, e := url.Parse(input.URL)
	if e != nil {
		return e
	}
	u, e := url.Parse(asset)
	if e != nil {
		return e
	}
	// The manifest selects both provider and release. Catalog entries cannot send
	// acquisition to arbitrary hosts or substitute a different release/theme.
	if source.Scheme != "s3" || u.Scheme != "https" || u.Host != source.Host+".s3.us-west-2.amazonaws.com" || !strings.HasPrefix(u.Path, source.Path) || !strings.HasSuffix(u.Path, ".parquet") {
		return fmt.Errorf("asset outside pinned source: %s", asset)
	}
	return nil
}
func catalogAssets(path, kind string, bounds [4]float64) ([]string, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	stat, e := f.Stat()
	if e != nil {
		return nil, e
	}
	pf, e := parquet.OpenFile(f, stat.Size())
	if e != nil {
		return nil, e
	}
	r := parquet.NewGenericReader[any](pf)
	defer r.Close()
	rows := make([]any, 128)
	out := []string{}
	for {
		n, e := r.Read(rows)
		for _, v := range rows[:n] {
			m := v.(map[string]any)
			if m["type"] != "Feature" || m["collection"] != kind || !overlapsMap(m["bbox"], bounds) {
				continue
			}
			assets, ok := m["assets"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("catalog has no assets")
			}
			aws, ok := assets["aws"].(map[string]any)
			if !ok {
				return nil, fmt.Errorf("catalog has no AWS HTTPS asset")
			}
			href, ok := aws["href"].(string)
			if !ok {
				return nil, fmt.Errorf("catalog asset has no href")
			}
			out = append(out, href)
		}
		if e != nil {
			if e == io.EOF {
				break
			}
			return nil, e
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no catalog assets for %s", kind)
	}
	sort.Strings(out)
	return unique(out), nil
}
func number(v any) float64 {
	switch x := v.(type) {
	case float32:
		return float64(x)
	case float64:
		return x
	case int64:
		return float64(x)
	default:
		return math.NaN()
	}
}
func overlapsMap(value any, b [4]float64) bool {
	m, ok := value.(map[string]any)
	return ok && number(m["xmin"]) < b[2] && number(m["xmax"]) > b[0] && number(m["ymin"]) < b[3] && number(m["ymax"]) > b[1]
}
func overlapsStats(group format.RowGroup, b [4]float64) bool {
	for _, c := range group.Columns {
		md := c.MetaData
		path := strings.Join(md.PathInSchema, ".")
		var value []byte
		switch path {
		case "bbox.xmin", "bbox.ymin":
			value = md.Statistics.MinValue
		case "bbox.xmax", "bbox.ymax":
			value = md.Statistics.MaxValue
		default:
			continue
		}
		v := math.NaN()
		if len(value) == 4 {
			v = float64(math.Float32frombits(binary.LittleEndian.Uint32(value)))
		} else if len(value) == 8 {
			v = math.Float64frombits(binary.LittleEndian.Uint64(value))
		}
		if (path == "bbox.xmin" && v >= b[2]) || (path == "bbox.xmax" && v <= b[0]) || (path == "bbox.ymin" && v >= b[3]) || (path == "bbox.ymax" && v <= b[1]) {
			return false
		}
	}
	return true
}
func parquetFeature(row map[string]any, schema *parquet.Schema) (map[string]any, error) {
	geometry, ok := row["geometry"].(string)
	if !ok {
		return nil, fmt.Errorf("expected binary WKB geometry")
	}
	point, e := pointWKB([]byte(geometry))
	if e != nil {
		return nil, e
	}
	props := map[string]any{}
	for _, field := range schema.Fields() {
		name := field.Name()
		if name == "geometry" || name == "bbox" || row[name] == nil {
			continue
		}
		props[name] = parquetJSON(row[name], field)
	}
	return map[string]any{"type": "Feature", "geometry": map[string]any{"type": "Point", "coordinates": point}, "properties": props}, nil
}
func pointWKB(b []byte) ([2]float64, error) {
	var point [2]float64
	if len(b) != 21 || (b[0] != 0 && b[0] != 1) {
		return point, fmt.Errorf("expected 2D WKB Point")
	}
	var order binary.ByteOrder = binary.LittleEndian
	if b[0] == 0 {
		order = binary.BigEndian
	}
	if order.Uint32(b[1:5]) != 1 {
		return point, fmt.Errorf("expected WKB Point type")
	}
	point[0] = math.Float64frombits(order.Uint64(b[5:13]))
	point[1] = math.Float64frombits(order.Uint64(b[13:21]))
	return point, nil
}

// Keep Arrow's map-as-pairs representation used in existing source records.
// Struct fields retain nulls; top-level nulls were omitted by the initial export.
func parquetJSON(value any, node parquet.Node) any {
	if value == nil {
		return nil
	}
	v := reflect.ValueOf(value)
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return nil
		}
		return parquetJSON(v.Elem().Interface(), node)
	}
	if text, ok := value.([]byte); ok {
		return string(text)
	}
	if x, ok := value.(float32); ok {
		return float64(x)
	}
	fields := node.Fields()
	if len(fields) == 1 && fields[0].Name() == "key_value" {
		pairs := []any{}
		m := value.(map[string]any)
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			pairs = append(pairs, []any{k, parquetJSON(m[k], fields[0].Fields()[1])})
		}
		return pairs
	}
	if len(fields) == 1 && fields[0].Name() == "list" {
		values := value.([]any)
		out := make([]any, 0, len(values))
		for _, v := range values {
			out = append(out, parquetJSON(v, fields[0].Fields()[0]))
		}
		return out
	}
	if m, ok := value.(map[string]any); ok {
		out := map[string]any{}
		for _, f := range fields {
			out[f.Name()] = parquetJSON(m[f.Name()], f)
		}
		return out
	}
	return value
}
