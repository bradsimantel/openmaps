package routing

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"unsafe"
)

// FlatVersion fixes field widths, little-endian encoding, zero padding and order.
// Only pointer-free arrays are mapped. SQLite remains the authoritative snapshot.
const FlatVersion = "routing-hot-le64-v2"
const flatHeaderBytes = 4096

type flatSection struct {
	Name                 string
	Offset, Count, Width int64
}
type flatHeader struct {
	Version, Preprocessing, GraphSHA256, PayloadSHA256 string
	Sections                                           []flatSection
}
type queryMapping struct {
	once sync.Once
	data []byte
	err  error
}

func (m *queryMapping) close() error {
	if m == nil {
		return nil
	}
	m.once.Do(func() { m.err = unmapQuery(m.data); m.data = nil })
	return m.err
}

// OpenMapped validates the complete SQLite graph, then moves its large numeric
// arrays to a verified read-only file in cacheDir. This bounds their Go heap use,
// not OS residency or construction memory. Call Close after the final request.
func OpenMapped(ctx context.Context, path, cacheDir string) (*Store, error) {
	s, err := Open(ctx, path)
	if err != nil || s == nil {
		return s, err
	}
	if err = s.UseMappedQueryData(ctx, cacheDir); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// UseMappedQueryData runs before publishing a Store. Existing cache files must
// match newly reconstructed canonical arrays, including preprocessing, exactly.
// It never repairs or overwrites a corrupt cache or modifies the SQLite snapshot.
func (s *Store) UseMappedQueryData(ctx context.Context, cacheDir string) error {
	if s == nil || cacheDir == "" {
		return nil
	}
	s.life.Lock()
	defer s.life.Unlock()
	if s.closed {
		return fmt.Errorf("routing store closed")
	}
	if s.mapping != nil {
		return fmt.Errorf("routing store already mapped")
	}
	if err := flatABI(); err != nil {
		return err
	}
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return err
	}
	h := s.flatHeader()
	// Hash canonical field encoding, never native padding or pointer bytes.
	hash := sha256.New()
	if err := s.writeFlat(ctx, hash); err != nil {
		return err
	}
	h.PayloadSHA256 = hex.EncodeToString(hash.Sum(nil))
	key := h.GraphSHA256
	if key == "" {
		key = h.PayloadSHA256
	}
	path := filepath.Join(cacheDir, key+"-"+FlatVersion+"-"+JunctionPreprocessingVersion+".bin")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if err = s.createFlat(ctx, path, h); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	last := h.Sections[len(h.Sections)-1]
	size := last.Offset + last.Count*last.Width
	if info.Size() != size {
		return fmt.Errorf("routing flat size mismatch")
	}
	raw := make([]byte, flatHeaderBytes)
	if _, err = io.ReadFull(f, raw); err != nil {
		return err
	}
	n := binary.LittleEndian.Uint32(raw)
	if n == 0 || n > flatHeaderBytes-4 {
		return fmt.Errorf("invalid routing flat header length")
	}
	var got flatHeader
	if err = json.Unmarshal(raw[4:4+n], &got); err != nil {
		return err
	}
	if got.Version != FlatVersion || got.Preprocessing != JunctionPreprocessingVersion {
		return fmt.Errorf("unsupported routing flat/preprocessing version %q/%q", got.Version, got.Preprocessing)
	}
	if !reflect.DeepEqual(h, got) {
		return fmt.Errorf("routing flat manifest differs from validated graph")
	}
	for _, v := range raw[4+n:] {
		if v != 0 {
			return fmt.Errorf("nonzero routing flat header padding")
		}
	}
	mapped, err := mapQuery(f, int(size))
	if err != nil {
		return err
	}
	success := false
	defer func() {
		if !success {
			unmapQuery(mapped)
		}
	}()
	hash.Reset()
	// Chunking bounds cancellation latency while validating a large mapping.
	for lo := flatHeaderBytes; lo < len(mapped); lo += 1 << 20 {
		if err := ctx.Err(); err != nil {
			return err
		}
		hash.Write(mapped[lo:min(len(mapped), lo+(1<<20))])
	}
	if hex.EncodeToString(hash.Sum(nil)) != h.PayloadSHA256 {
		return fmt.Errorf("routing flat payload checksum mismatch")
	}
	section := func(i int) []byte { x := h.Sections[i]; return mapped[x.Offset : x.Offset+x.Count*x.Width] }
	s.points = flatSlice[Point](section(0))
	s.edges = flatSlice[edge](section(1))
	s.offsets = flatSlice[uint32](section(2))
	s.adjacency = flatSlice[int](section(3))
	s.directions = flatSlice[[2]int](section(4))
	s.continuation = flatSlice[int32](section(5))
	s.zones = flatSlice[int](section(6))
	s.components = flatSlice[int32](section(7))
	s.junctions = section(8)
	s.cellBounds = flatSlice[cellBounds](section(9))
	s.cellEntries = flatSlice[cellEntry](section(10))
	s.cellTransfers = flatSlice[cellTransfer](section(11))
	s.cellPaths = flatSlice[cellPath](section(12))
	s.mapping = &queryMapping{data: mapped}
	s.mappingCleanup = runtime.AddCleanup(s, func(m *queryMapping) { m.close() }, s.mapping)
	success = true
	return nil
}

// Close waits for route calls and releases mapped pages. Dataset publication also
// holds the existing HTTP response lease, so encoding finishes before retirement.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.life.Lock()
	defer s.life.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.mappingCleanup.Stop()
	return s.mapping.close()
}
func (s *Store) MappedBytes() int {
	if s == nil {
		return 0
	}
	s.life.RLock()
	defer s.life.RUnlock()
	if s.mapping == nil {
		return 0
	}
	return len(s.mapping.data)
}

// These checked, pointer-free overlays are the only unsafe operations. Encoding
// is explicit and portable; this reader deliberately supports little-endian
// 64-bit ABIs with the verified offsets rather than guessing another ABI.
func flatABI() error {
	var e edge
	var one uint64 = 1
	if unsafe.Sizeof(int(0)) != 8 || *(*byte)(unsafe.Pointer(&one)) != 1 ||
		unsafe.Sizeof(e) != 48 || unsafe.Offsetof(e.from) != 0 || unsafe.Offsetof(e.to) != 8 ||
		unsafe.Offsetof(e.segment) != 16 || unsafe.Offsetof(e.reverse) != 24 || unsafe.Offsetof(e.cellEntry) != 28 ||
		unsafe.Offsetof(e.length) != 32 || unsafe.Offsetof(e.seconds) != 40 || unsafe.Sizeof(Point{}) != 16 {
		return fmt.Errorf("unsupported routing flat memory ABI")
	}
	if unsafe.Sizeof(denseEdge{}) != 32 || unsafe.Offsetof(denseEdge{}.from) != 0 || unsafe.Offsetof(denseEdge{}.to) != 4 || unsafe.Offsetof(denseEdge{}.segmentReverse) != 8 || unsafe.Offsetof(denseEdge{}.cellEntry) != 12 || unsafe.Offsetof(denseEdge{}.length) != 16 || unsafe.Offsetof(denseEdge{}.seconds) != 24 {
		return fmt.Errorf("unsupported routing dense edge ABI")
	}
	if unsafe.Sizeof(cellBounds{}) != 32 || unsafe.Offsetof(cellBounds{}.minX) != 0 ||
		unsafe.Offsetof(cellBounds{}.minY) != 8 || unsafe.Offsetof(cellBounds{}.maxX) != 16 || unsafe.Offsetof(cellBounds{}.maxY) != 24 ||
		unsafe.Sizeof(cellEntry{}) != 16 || unsafe.Offsetof(cellEntry{}.edge) != 0 || unsafe.Offsetof(cellEntry{}.cell) != 4 ||
		unsafe.Offsetof(cellEntry{}.offset) != 8 || unsafe.Offsetof(cellEntry{}.count) != 12 ||
		unsafe.Sizeof(cellTransfer{}) != 16 || unsafe.Offsetof(cellTransfer{}.cost) != 0 || unsafe.Offsetof(cellTransfer{}.path) != 8 || unsafe.Offsetof(cellTransfer{}.last) != 12 ||
		unsafe.Sizeof(cellPath{}) != 12 || unsafe.Offsetof(cellPath{}.parent) != 0 || unsafe.Offsetof(cellPath{}.first) != 4 || unsafe.Offsetof(cellPath{}.last) != 8 {
		return fmt.Errorf("unsupported routing cell flat memory ABI")
	}
	return nil
}
func flatSlice[T any](b []byte) []T {
	if len(b) == 0 {
		return nil
	}
	var v T
	return unsafe.Slice((*T)(unsafe.Pointer(&b[0])), len(b)/int(unsafe.Sizeof(v)))
}
func (s *Store) flatHeader() flatHeader { return s.numericHeader(false) }
func (s *Store) numericHeader(dense bool) flatHeader {
	h := flatHeader{Version: FlatVersion, Preprocessing: JunctionPreprocessingVersion, GraphSHA256: s.graphSHA}
	offset := int64(flatHeaderBytes)
	for _, v := range []struct {
		name  string
		count int
		width int64
	}{
		{"points", len(s.points), 16}, {"edges", len(s.edges), 48}, {"offsets", len(s.offsets), 4}, {"adjacency", len(s.adjacency), 8}, {"directions", len(s.directions), 16}, {"continuation", len(s.continuation), 4}, {"zones", len(s.zones), 8}, {"components", len(s.components), 4}, {"junctions", len(s.junctions), 1},
		{"cell_bounds", len(s.cellBounds), 32}, {"cell_entries", len(s.cellEntries), 16}, {"cell_transfers", len(s.cellTransfers), 16}, {"cell_paths", len(s.cellPaths), 12},
	} {
		if dense && v.name == "edges" {
			v.width = 32
		}
		offset = (offset + 7) &^ 7
		h.Sections = append(h.Sections, flatSection{v.name, offset, int64(v.count), v.width})
		offset += int64(v.count) * v.width
	}
	return h
}
func (s *Store) writeFlat(ctx context.Context, output io.Writer) error {
	return s.writeNumeric(ctx, output, false)
}
func (s *Store) writeNumeric(ctx context.Context, output io.Writer, dense bool) error {
	w := bufio.NewWriterSize(output, 64<<10)
	var buf [48]byte
	var count int64 = flatHeaderBytes
	write := func(b []byte) error { _, err := w.Write(b); count += int64(len(b)); return err }
	for i, section := range s.numericHeader(dense).Sections {
		if err := ctx.Err(); err != nil {
			return err
		}
		clear(buf[:])
		if err := write(buf[:section.Offset-count]); err != nil {
			return err
		}
		for j := 0; j < int(section.Count); j++ {
			if j%4096 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			clear(buf[:])
			put := func(offset int, v uint64) { binary.LittleEndian.PutUint64(buf[offset:], v) }
			switch i {
			case 0:
				put(0, math.Float64bits(s.points[j][0]))
				put(8, math.Float64bits(s.points[j][1]))
			case 1:
				e := s.edges[j]
				if dense {
					from, fromOK := s.findNode(e.from)
					to, toOK := s.findNode(e.to)
					if !fromOK || !toOK || from < 0 || to < 0 || int(from) >= len(s.points) || int(to) >= len(s.points) || e.segment < 0 || e.segment > math.MaxInt32 {
						return fmt.Errorf("dense edge overflow or missing endpoint")
					}
					segment := uint32(e.segment)
					if e.reverse {
						segment |= 1 << 31
					}
					binary.LittleEndian.PutUint32(buf[0:], uint32(from))
					binary.LittleEndian.PutUint32(buf[4:], uint32(to))
					binary.LittleEndian.PutUint32(buf[8:], segment)
					binary.LittleEndian.PutUint32(buf[12:], uint32(e.cellEntry))
					put(16, math.Float64bits(e.length))
					put(24, math.Float64bits(e.seconds))
					break
				}
				put(0, uint64(e.from))
				put(8, uint64(e.to))
				put(16, uint64(e.segment))
				if e.reverse {
					buf[24] = 1
				}
				binary.LittleEndian.PutUint32(buf[28:], uint32(e.cellEntry))
				put(32, math.Float64bits(e.length))
				put(40, math.Float64bits(e.seconds))
			case 2:
				binary.LittleEndian.PutUint32(buf[:], s.offsets[j])
			case 3:
				put(0, uint64(s.adjacency[j]))
			case 4:
				put(0, uint64(s.directions[j][0]))
				put(8, uint64(s.directions[j][1]))
			case 5:
				binary.LittleEndian.PutUint32(buf[:], uint32(s.continuation[j]))
			case 6:
				put(0, uint64(s.zones[j]))
			case 7:
				binary.LittleEndian.PutUint32(buf[:], uint32(s.components[j]))
			case 8:
				buf[0] = s.junctions[j]
			case 9:
				b := s.cellBounds[j]
				put(0, math.Float64bits(b.minX))
				put(8, math.Float64bits(b.minY))
				put(16, math.Float64bits(b.maxX))
				put(24, math.Float64bits(b.maxY))
			case 10:
				e := s.cellEntries[j]
				for k, v := range []int32{e.edge, e.cell, e.offset, e.count} {
					binary.LittleEndian.PutUint32(buf[k*4:], uint32(v))
				}
			case 11:
				t := s.cellTransfers[j]
				put(0, math.Float64bits(t.cost))
				binary.LittleEndian.PutUint32(buf[8:], uint32(t.path))
				binary.LittleEndian.PutUint32(buf[12:], uint32(t.last))
			case 12:
				p := s.cellPaths[j]
				for k, v := range []int32{p.parent, p.first, p.last} {
					binary.LittleEndian.PutUint32(buf[k*4:], uint32(v))
				}
			}
			if err := write(buf[:section.Width]); err != nil {
				return err
			}
		}
	}
	return w.Flush()
}
func (s *Store) createFlat(ctx context.Context, path string, h flatHeader) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".routing-hot-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	raw := make([]byte, flatHeaderBytes)
	header, err := json.Marshal(h)
	if err != nil {
		return err
	}
	if len(header) > len(raw)-4 {
		return fmt.Errorf("routing flat header too large")
	}
	binary.LittleEndian.PutUint32(raw, uint32(len(header)))
	copy(raw[4:], header)
	if _, err = f.Write(raw); err != nil {
		return err
	}
	if err = s.writeFlat(ctx, f); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Chmod(0400); err != nil {
		return err
	}
	// Atomic publication without replacing an existing file. A concurrent publisher
	// is accepted only after the caller verifies its complete artifact.
	if err = os.Link(f.Name(), path); err != nil && !os.IsExist(err) {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}
