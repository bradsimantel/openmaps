package valhallatiles

import (
	"bytes"
	"container/list"
	"errors"
	"fmt"
	"io"
)

const pageSize = 64 << 10

type MissingTileError struct{ Tile ID }

func (e *MissingTileError) Error() string {
	return fmt.Sprintf("missing tile %s: incomplete package dependencies", e.Tile)
}

type page struct {
	offset int64
	b      []byte
}

type CacheReport struct {
	Graph                            CacheStats
	Turns, ReverseTurns              *CacheStats  `json:",omitempty"`
	Landmarks                        []CacheStats `json:",omitempty"`
	RetainedBytes, PayloadLimitBytes int64
}

// CacheReport includes all persistent page caches owned by this single-reader
// lease. Per-cache peak counters are not a simultaneous process RSS measurement.
func (r *Reader) CacheReport() CacheReport {
	out := CacheReport{Graph: r.Stats, RetainedBytes: r.Stats.Bytes, PayloadLimitBytes: r.limit}
	add := func(child *Reader) { out.RetainedBytes += child.Stats.Bytes; out.PayloadLimitBytes += child.limit }
	if r.turnsReader != nil {
		v := r.turnsReader.Stats
		out.Turns = &v
		add(r.turnsReader)
	}
	for _, child := range r.landmarkReaders {
		out.Landmarks = append(out.Landmarks, child.Stats)
		add(child)
	}
	return out
}

// ClearCache makes the next lookup application-cache cold, not OS-cache cold.
func (r *Reader) ClearCache() {
	if r.records != nil {
		r.records = &recordCache{}
	}
	for _, reader := range r.landmarkReaders {
		reader.ClearCache()
	}
	if r.turnsReader != nil {
		r.turnsReader.ClearCache()
	}
	if r.pages != nil {
		r.pages = map[int64]*list.Element{}
	}
	r.lru.Init()
	r.Stats = CacheStats{}
}

func (t *tile) span(offset, size int) ([]byte, error) {
	if offset < 0 || size < 0 || offset > t.size || size > t.size-offset {
		return nil, errors.New("tile read outside extent")
	}
	if t.reader == nil {
		return t.b[offset : offset+size], nil
	}
	if size == 0 {
		return nil, nil
	}
	r := t.reader
	absolute := t.offset + int64(offset)
	var out []byte
	for left := size; left > 0; {
		base := absolute / pageSize * pageSize
		el := r.pages[base]
		if el == nil {
			var recycled []byte
			for r.Stats.Bytes+pageSize > r.limit {
				last := r.lru.Back()
				p := last.Value.(page)
				if r.recordPageReuse {
					recycled = p.b[:cap(p.b)]
				}
				delete(r.pages, p.offset)
				r.lru.Remove(last)
				r.Stats.Bytes -= pageSize
				r.Stats.Evictions++
			}
			b := recycled
			if b == nil {
				b = make([]byte, pageSize)
			}
			n, err := r.f.ReadAt(b, base)
			if err != nil && err != io.EOF {
				return nil, err
			}
			b = b[:n]
			el = r.lru.PushFront(page{base, b})
			r.pages[base] = el
			r.Stats.Bytes += pageSize
			r.Stats.PeakBytes = max(r.Stats.PeakBytes, r.Stats.Bytes)
			r.Stats.Loads++
			r.Stats.ReadBytes += int64(n)
		} else {
			r.lru.MoveToFront(el)
			r.Stats.Hits++
		}
		b := el.Value.(page).b
		at := int(absolute - base)
		n := min(left, pageSize-at)
		if at+n > len(b) {
			return nil, io.ErrUnexpectedEOF
		}
		if n == size {
			// Internal borrowed view. Decoders return copied domain records.
			// Default readers never recycle buffers. The restricted landmark
			// loop completes each fixed-record decode before any subsequent read.
			return b[at : at+n], nil
		}
		if out == nil {
			out = make([]byte, 0, size)
		}
		out = append(out, b[at:at+n]...)
		absolute += int64(n)
		left -= n
	}
	return out, nil
}

func (t *tile) name(offset int) (string, error) {
	var out []byte
	for offset < t.textEnd && len(out) < pageSize {
		n := min(256, t.textEnd-offset, pageSize-len(out))
		b, err := t.span(offset, n)
		if err != nil {
			return "", err
		}
		if end := bytes.IndexByte(b, 0); end >= 0 {
			return string(append(out, b[:end]...)), nil
		}
		out = append(out, b...)
		offset += n
	}
	return "", errors.New("unterminated or oversized name")
}

func (t *tile) basePoint() Point {
	width := [...]int{90, 360, 1440}[t.id.Level()]
	step := [...]float64{4, 1, .25}[t.id.Level()]
	return Point{float64(t.id.Tile()%width)*step - 180, float64(t.id.Tile()/width)*step - 90}
}

func (r *Reader) Package(id ID) string { return r.packages[id.Base()] }
