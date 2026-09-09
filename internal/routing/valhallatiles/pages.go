package valhallatiles

import (
	"bytes"
	"container/list"
	"errors"
	"fmt"
	"io"
	"math"
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

// ClearCache makes the next lookup application-cache cold, not OS-cache cold.
func (r *Reader) ClearCache() {
	r.cache = map[ID]*list.Element{}
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
			for r.Stats.Bytes+pageSize > r.limit {
				last := r.lru.Back()
				p := last.Value.(page)
				delete(r.pages, p.offset)
				r.lru.Remove(last)
				r.Stats.Bytes -= pageSize
				r.Stats.Evictions++
			}
			b := make([]byte, pageSize)
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
			// Buffers are never recycled while a decoder may still hold a view.
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
	if string(bytes.TrimRight(t.b[16:32], "\x00")) == "3.4.0" {
		// 3.4.0 GraphTileHeader::base_ll derives this from the GraphId.
		width := [...]int{90, 360, 1440}[t.id.Level()]
		step := [...]float64{4, 1, .25}[t.id.Level()]
		return Point{float64(t.id.Tile()%width)*step - 180, float64(t.id.Tile()/width)*step - 90}
	}
	return Point{float64(math.Float32frombits(u32(t.b, 8))), float64(math.Float32frombits(u32(t.b, 12)))}
}

func (r *Reader) Package(id ID) string { return r.packages[id.Base()] }

// UsePageCache enables a matched page-vs-tile experiment on the original
// uncompressed archive. It neither changes the archive nor acquires any data.
func (r *Reader) UsePageCache() error {
	if r.closed {
		return errors.New("reader closed")
	}
	if r.limit < pageSize {
		return errors.New("page cache needs at least 64 KiB")
	}
	if r.tiles != nil {
		return nil
	}
	tiles := map[ID]*tile{}
	for id, e := range r.index {
		b := make([]byte, 272)
		if _, err := r.f.ReadAt(b, e.offset); err != nil {
			return err
		}
		t, err := parseTileHeader(b, id, int(e.size))
		if err != nil {
			return err
		}
		t.reader, t.offset = r, e.offset
		tiles[id] = t
	}
	r.tiles = tiles
	r.pages = map[int64]*list.Element{}
	r.ClearCache()
	return nil
}
