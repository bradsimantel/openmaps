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
	"runtime"
	"sort"
)

// PreparedVersion is independent of source graph and numeric preprocessing versions.
const PreparedVersion = "routing-prepared-le64-v1"

// Prepared v1 fixes the numeric v2 prefix; a numeric migration must explicitly
// migrate this format too, rather than silently changing its reader.
const preparedNumericVersion = "routing-hot-le64-v2"
const preparedAuxLimit = 128 << 20

type PreparedReceipt struct {
	Version        string
	SnapshotSHA256 string
	ArtifactSHA256 string
	GraphSHA256    string
	Artifact       string
	Summary        *Summary
}
type preparedTrie struct {
	Next   map[int]int
	Fail   int
	Banned bool
}
type preparedLink struct {
	To     int64
	Length float64
}
type preparedAux struct {
	Metadata           Metadata
	Access             AccessData
	Trie               []preparedTrie
	Driveways          map[int64][]preparedLink
	MaxMetersPerSecond float64
}
type preparedViews struct {
	segments [][6]uint64
	guards   [][7]uint64
	nodes    [][2]uint64
	strings  []byte
}

func nodeHash(v uint64) uint64 {
	v ^= v >> 30
	v *= 0xbf58476d1ce4e5b9
	v ^= v >> 27
	v *= 0x94d049bb133111eb
	return v ^ (v >> 31)
}
func (s *Store) findNode(id int64) (int32, bool) {
	if s.prepared == nil {
		i, ok := s.nodeIndex[id]
		return i, ok
	}
	if id <= 0 {
		return 0, false
	}
	slots := s.prepared.nodes
	mask := uint64(len(slots) - 1)
	for i := nodeHash(uint64(id)) & mask; ; i = (i + 1) & mask {
		v := slots[i]
		if v[0] == 0 {
			return 0, false
		}
		if v[0] == uint64(id) {
			return int32(v[1]), true
		}
	}
}
func (s *Store) nodeOrdinal(id int64) int32 { i, _ := s.findNode(id); return i }
func (s *Store) nodeFlag(id int64, bit uint) bool {
	if s.prepared == nil {
		if bit == 32 {
			return s.publicNodes[id]
		}
		return s.restrictedNodes[id]
	}
	if id <= 0 {
		return false
	}
	slots := s.prepared.nodes
	mask := uint64(len(slots) - 1)
	for i := nodeHash(uint64(id)) & mask; ; i = (i + 1) & mask {
		v := slots[i]
		if v[0] == 0 {
			return false
		}
		if v[0] == uint64(id) {
			return v[1]&(uint64(1)<<bit) != 0
		}
	}
}
func (s *Store) isPublic(id int64) bool     { return s.nodeFlag(id, 32) }
func (s *Store) isRestricted(id int64) bool { return s.nodeFlag(id, 33) }
func (s *Store) segmentCount() int {
	if s.prepared != nil {
		return len(s.prepared.segments)
	}
	return len(s.segments)
}
func (s *Store) segment(i int) Segment {
	v := s.segmentFields(i)
	if s.prepared != nil {
		r := s.prepared.segments[i]
		v.ID = string(s.prepared.strings[r[3] : r[3]+r[4]])
	}
	return v
}
func (s *Store) segmentFields(i int) Segment {
	if s.prepared == nil {
		return s.segments[i]
	}
	v := s.prepared.segments[i]
	f := v[5]
	return Segment{Way: int64(v[0]), From: int64(v[1]), To: int64(v[2]), Forward: f&1 != 0, Backward: f&2 != 0, Snap: f&4 != 0, DestinationForward: f&8 != 0, DestinationBackward: f&16 != 0, Service: f&32 != 0, Elevated: f&64 != 0}
}
func (s *Store) guard(i int) Guard {
	if s.prepared == nil {
		return s.guards[i]
	}
	v := s.prepared.guards[i]
	return Guard{Way: int64(v[0]), From: Point{math.Float64frombits(v[1]), math.Float64frombits(v[2])}, To: Point{math.Float64frombits(v[3]), math.Float64frombits(v[4])}, Segment: string(s.prepared.strings[v[5] : v[5]+v[6]])}
}
func hashFile(ctx context.Context, path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	b := make([]byte, 1<<20)
	for {
		if e = ctx.Err(); e != nil {
			return "", e
		}
		n, e := f.Read(b)
		h.Write(b[:n])
		if e == io.EOF {
			break
		}
		if e != nil {
			return "", e
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// PublishPrepared publishes canonical arrays from a source-validated store. The
// caller must validate the entire snapshot (including lookup), and supply its
// digest taken before validation. This is an offline publication authority, not
// a way to approve a caller-supplied artifact.
func (s *Store) PublishPrepared(ctx context.Context, snapshot, before, dir string) (*PreparedReceipt, error) {
	if s == nil || s.graphSHA == "" || s.prepared != nil {
		return nil, fmt.Errorf("publication requires a source-validated legacy store")
	}
	after, e := hashFile(ctx, snapshot)
	if e != nil {
		return nil, e
	}
	if before != after {
		return nil, fmt.Errorf("snapshot changed during preparation")
	}
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	receiptPath := filepath.Join(dir, before+".json")
	if _, e = os.Stat(receiptPath); !os.IsNotExist(e) {
		return nil, fmt.Errorf("prepared receipt already exists or cannot be inspected")
	}
	name := before + "-" + PreparedVersion + ".bin"
	h, e := s.writePrepared(ctx, filepath.Join(dir, name))
	if e != nil {
		return nil, e
	}
	digest, e := hashFile(ctx, filepath.Join(dir, name))
	if e != nil {
		return nil, e
	}
	r := &PreparedReceipt{Version: PreparedVersion, SnapshotSHA256: before, ArtifactSHA256: digest, GraphSHA256: h.GraphSHA256, Artifact: name, Summary: &Summary{Metadata: s.meta, SHA256: s.graphSHA, Layout: s.sourceLayout, Preprocessing: s.sourcePreprocessing}}
	raw, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return nil, e
	}
	if e = ctx.Err(); e != nil {
		return nil, e
	}
	if e = publishBytes(receiptPath, raw); e != nil {
		return nil, e
	}
	return r, nil
}
func publishBytes(path string, raw []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".prepared-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(raw); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Chmod(0400); e != nil {
		return e
	}
	if e = os.Link(f.Name(), path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func (s *Store) writePrepared(ctx context.Context, path string) (flatHeader, error) {
	h := s.flatHeader()
	if FlatVersion != preparedNumericVersion {
		return h, fmt.Errorf("prepared numeric format requires migration")
	}
	h.Version = PreparedVersion
	f, e := os.CreateTemp(filepath.Dir(path), ".prepared-*")
	if e != nil {
		return h, e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(make([]byte, flatHeaderBytes)); e != nil {
		return h, e
	}
	if e = s.writeFlat(ctx, f); e != nil {
		return h, e
	}
	w := bufio.NewWriterSize(f, 1<<20)
	last := h.Sections[len(h.Sections)-1]
	offset := last.Offset + last.Count*last.Width
	add := func(name string, count, width int64, write func() error) error {
		pad := (-offset) & 7
		if _, e := w.Write(make([]byte, int(pad))); e != nil {
			return e
		}
		offset += pad
		h.Sections = append(h.Sections, flatSection{name, offset, count, width})
		if e := write(); e != nil {
			return e
		}
		offset += count * width
		return ctx.Err()
	}
	put := func(v any) error { return binary.Write(w, binary.LittleEndian, v) }
	strings := []byte{}
	if e = add("segments", int64(len(s.segments)), 48, func() error {
		for i, v := range s.segments {
			if i%4096 == 0 {
				if e := ctx.Err(); e != nil {
					return e
				}
			}
			flags := uint64(0)
			for i, b := range []bool{v.Forward, v.Backward, v.Snap, v.DestinationForward, v.DestinationBackward, v.Service, v.Elevated} {
				if b {
					flags |= 1 << i
				}
			}
			r := [6]uint64{uint64(v.Way), uint64(v.From), uint64(v.To), uint64(len(strings)), uint64(len(v.ID)), flags}
			strings = append(strings, v.ID...)
			if e := put(r); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		return h, e
	}
	if e = add("guards", int64(len(s.guards)), 56, func() error {
		for i, v := range s.guards {
			if i%4096 == 0 {
				if e := ctx.Err(); e != nil {
					return e
				}
			}
			r := [7]uint64{uint64(v.Way), math.Float64bits(v.From[0]), math.Float64bits(v.From[1]), math.Float64bits(v.To[0]), math.Float64bits(v.To[1]), uint64(len(strings)), uint64(len(v.Segment))}
			strings = append(strings, v.Segment...)
			if e := put(r); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		return h, e
	}
	// Insert in source-ID order to make hash collisions reproducible across map seeds.
	ids := make([]int64, 0, len(s.nodeIndex))
	for id := range s.nodeIndex {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	n := 1
	for n < len(ids)*2 {
		n *= 2
	}
	slots := make([][2]uint64, n)
	for ordinal, id := range ids {
		if ordinal%4096 == 0 {
			if e := ctx.Err(); e != nil {
				return h, e
			}
		}
		i := int(nodeHash(uint64(id))) & (n - 1)
		for slots[i][0] != 0 {
			i = (i + 1) & (n - 1)
		}
		flags := uint64(uint32(s.nodeIndex[id]))
		if s.publicNodes[id] {
			flags |= 1 << 32
		}
		if s.restrictedNodes[id] {
			flags |= 1 << 33
		}
		slots[i] = [2]uint64{uint64(id), flags}
	}
	if e = add("node_lookup", int64(n), 16, func() error { return put(slots) }); e != nil {
		return h, e
	}
	if e = add("strings", int64(len(strings)), 1, func() error { _, e := w.Write(strings); return e }); e != nil {
		return h, e
	}
	for i, index := range []spatialIndex{s.segmentIndex, s.guardIndex, s.areaIndex, s.accessIndex} {
		if e = add(fmt.Sprintf("spatial_%d_nodes", i), int64(len(index.nodes)), 48, func() error {
			for _, v := range index.nodes {
				if e := put(v.bounds); e != nil {
					return e
				}
				if e := put([4]int32{v.left, v.right, v.start, v.end}); e != nil {
					return e
				}
			}
			return nil
		}); e != nil {
			return h, e
		}
		if e = add(fmt.Sprintf("spatial_%d_ids", i), int64(len(index.ids)), 4, func() error { return put(index.ids) }); e != nil {
			return h, e
		}
	}
	aux := preparedAux{Metadata: s.meta, Access: s.access, MaxMetersPerSecond: s.maxMetersPerSecond, Driveways: map[int64][]preparedLink{}}
	for _, v := range s.trie {
		aux.Trie = append(aux.Trie, preparedTrie{v.next, v.fail, v.banned})
	}
	for id, links := range s.driveways {
		for _, v := range links {
			aux.Driveways[id] = append(aux.Driveways[id], preparedLink{v.to, v.length})
		}
	}
	raw, e := json.Marshal(aux)
	if e != nil {
		return h, e
	}
	if len(raw) > preparedAuxLimit {
		return h, fmt.Errorf("prepared ancillary data exceeds %d bytes; requires a new bounded format", preparedAuxLimit)
	}
	if e = add("ancillary", int64(len(raw)), 1, func() error { _, e := w.Write(raw); return e }); e != nil {
		return h, e
	}
	if e = w.Flush(); e != nil {
		return h, e
	}
	if _, e = f.Seek(flatHeaderBytes, io.SeekStart); e != nil {
		return h, e
	}
	hash := sha256.New()
	if _, e = io.Copy(hash, f); e != nil {
		return h, e
	}
	h.PayloadSHA256 = hex.EncodeToString(hash.Sum(nil))
	raw, e = json.Marshal(h)
	if e != nil {
		return h, e
	}
	if len(raw) > flatHeaderBytes-4 {
		return h, fmt.Errorf("prepared header too large")
	}
	header := make([]byte, flatHeaderBytes)
	binary.LittleEndian.PutUint32(header, uint32(len(raw)))
	copy(header[4:], raw)
	if _, e = f.WriteAt(header, 0); e != nil {
		return h, e
	}
	if e = f.Sync(); e != nil {
		return h, e
	}
	if e = f.Chmod(0400); e != nil {
		return h, e
	}
	if e = os.Link(f.Name(), path); e != nil {
		return h, e
	}
	return h, nil
}

// OpenPrepared requires a trusted offline receipt for this exact SQLite file.
// Missing/invalid prepared data is an error, never a request to reconstruct it.
// Integrity verification touches all artifact pages; mmap does not bound RSS.
func OpenPrepared(ctx context.Context, snapshot, dir string) (*Store, error) {
	done := loadPhase(ctx, "source snapshot integrity")
	digest, e := hashFile(ctx, snapshot)
	done()
	if e != nil {
		return nil, e
	}
	r, e := readPreparedReceipt(dir, digest)
	if e != nil {
		return nil, e
	}
	return openPreparedArtifact(ctx, filepath.Join(dir, r.Artifact), r)
}

func readPreparedReceipt(dir, digest string) (PreparedReceipt, error) {
	var r PreparedReceipt
	raw, e := readLimited(filepath.Join(dir, digest+".json"), 1<<20)
	if e != nil {
		return r, fmt.Errorf("prepared receipt required (run cmd/routing-prepare offline): %w", e)
	}
	if e = json.Unmarshal(raw, &r); e != nil {
		return r, e
	}
	if r.Version != PreparedVersion || r.SnapshotSHA256 != digest || r.Artifact != filepath.Base(r.Artifact) || r.Artifact == "" || len(r.ArtifactSHA256) != 64 || len(r.GraphSHA256) != 64 {
		return r, fmt.Errorf("invalid prepared publication receipt")
	}
	if r.Summary == nil || r.Summary.SHA256 != r.GraphSHA256 {
		return r, fmt.Errorf("missing prepared source summary")
	}
	if e = validatePreparedMetadata(r.Summary.Metadata); e != nil {
		return r, e
	}
	return r, nil
}

// VerifyPreparedPublication verifies a selection against the trusted offline
// publication without creating query mappings or reconstructing the graph.
// It returns false for lookup-only snapshots, whose caller must still validate
// lookup semantics. expected is the selected whole-file source SHA-256.
// Complete source/artifact hashes establish equality to the canonical publication;
// query structure is checked by OpenPrepared before any runtime publication.
// Sources, artifacts and receipts must remain immutable, as for OpenPrepared.
func VerifyPreparedPublication(ctx context.Context, snapshot, expected, dir string) (bool, error) {
	if !filepath.IsAbs(snapshot) || len(expected) != 64 {
		return false, fmt.Errorf("invalid snapshot reference")
	}
	done := loadPhase(ctx, "publication source integrity")
	digest, e := hashFile(ctx, snapshot)
	done()
	if e != nil {
		return false, e
	}
	if digest != expected {
		return false, fmt.Errorf("snapshot differs from selected digest")
	}
	has, e := snapshotHasRouting(ctx, snapshot)
	if e != nil || !has {
		return false, e
	}
	r, e := readPreparedReceipt(dir, digest)
	if e != nil {
		return false, e
	}
	done = loadPhase(ctx, "publication artifact integrity")
	digest, e = hashFile(ctx, filepath.Join(dir, r.Artifact))
	done()
	if e != nil {
		return false, e
	}
	if digest != r.ArtifactSHA256 {
		return false, fmt.Errorf("prepared artifact differs from trusted publication digest")
	}
	return true, ctx.Err()
}

func readLimited(path string, limit int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if st.Size() > limit {
		return nil, fmt.Errorf("prepared metadata too large")
	}
	return io.ReadAll(io.LimitReader(f, limit+1))
}
func openPreparedArtifact(ctx context.Context, path string, r PreparedReceipt) (*Store, error) {
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	if FlatVersion != preparedNumericVersion {
		return nil, fmt.Errorf("prepared numeric format requires migration")
	}
	if e := flatABI(); e != nil {
		return nil, e
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return nil, e
	}
	if info.Size() < flatHeaderBytes || info.Size() > int64(int(^uint(0)>>1)) {
		return nil, fmt.Errorf("invalid prepared file size")
	}
	done := loadPhase(ctx, "mapping")
	mapped, e := mapQuery(f, int(info.Size()))
	done()
	if e != nil {
		return nil, e
	}
	ok := false
	defer func() {
		if !ok {
			unmapQuery(mapped)
		}
	}()
	done = loadPhase(ctx, "artifact integrity")
	hash := sha256.New()
	for lo := 0; lo < len(mapped); lo += 1 << 20 {
		if e = ctx.Err(); e != nil {
			return nil, e
		}
		hash.Write(mapped[lo:min(len(mapped), lo+(1<<20))])
	}
	done()
	if hex.EncodeToString(hash.Sum(nil)) != r.ArtifactSHA256 {
		return nil, fmt.Errorf("prepared artifact differs from trusted publication digest")
	}
	n := int(binary.LittleEndian.Uint32(mapped))
	if n <= 0 || n > flatHeaderBytes-4 {
		return nil, fmt.Errorf("invalid prepared header length")
	}
	var h flatHeader
	if e = json.Unmarshal(mapped[4:4+n], &h); e != nil {
		return nil, e
	}
	for _, b := range mapped[4+n : flatHeaderBytes] {
		if b != 0 {
			return nil, fmt.Errorf("nonzero prepared padding")
		}
	}
	if h.Version != PreparedVersion || h.Preprocessing != JunctionPreprocessingVersion || h.GraphSHA256 != r.GraphSHA256 {
		return nil, fmt.Errorf("unsupported or foreign prepared graph")
	}
	names := []string{"points", "edges", "offsets", "adjacency", "directions", "continuation", "zones", "components", "junctions", "cell_bounds", "cell_entries", "cell_transfers", "cell_paths", "segments", "guards", "node_lookup", "strings", "spatial_0_nodes", "spatial_0_ids", "spatial_1_nodes", "spatial_1_ids", "spatial_2_nodes", "spatial_2_ids", "spatial_3_nodes", "spatial_3_ids", "ancillary"}
	widths := []int64{16, 48, 4, 8, 16, 4, 8, 4, 1, 32, 16, 16, 12, 48, 56, 16, 1, 48, 4, 48, 4, 48, 4, 48, 4, 1}
	if len(h.Sections) != len(names) {
		return nil, fmt.Errorf("invalid prepared section count")
	}
	offset := int64(flatHeaderBytes)
	for i, v := range h.Sections {
		aligned := (offset + 7) &^ 7
		if v.Name != names[i] || v.Width != widths[i] || v.Count < 0 || v.Offset != aligned || v.Offset > info.Size() || v.Count > (info.Size()-v.Offset)/v.Width {
			return nil, fmt.Errorf("invalid prepared section %d dimensions", i)
		}
		for _, b := range mapped[offset:aligned] {
			if b != 0 {
				return nil, fmt.Errorf("nonzero section padding")
			}
		}
		offset = v.Offset + v.Count*v.Width
	}
	if offset != info.Size() || h.Sections[25].Count > preparedAuxLimit {
		return nil, fmt.Errorf("invalid prepared extent or ancillary budget")
	}
	section := func(i int) []byte { v := h.Sections[i]; return mapped[v.Offset : v.Offset+v.Count*v.Width] }
	s := &Store{graphSHA: h.GraphSHA256, prepared: &preparedViews{segments: flatSlice[[6]uint64](section(13)), guards: flatSlice[[7]uint64](section(14)), nodes: flatSlice[[2]uint64](section(15)), strings: section(16)}}
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
	for i, index := range []*spatialIndex{&s.segmentIndex, &s.guardIndex, &s.areaIndex, &s.accessIndex} {
		index.nodes = flatSlice[spatialNode](section(17 + 2*i))
		index.ids = flatSlice[int32](section(18 + 2*i))
	}
	done = loadPhase(ctx, "ancillary decoding")
	var aux preparedAux
	if e = json.Unmarshal(section(25), &aux); e != nil {
		return nil, e
	}
	s.meta = aux.Metadata
	s.access = aux.Access
	s.maxMetersPerSecond = aux.MaxMetersPerSecond
	s.driveways = map[int64][]drivewayLink{}
	for _, v := range aux.Trie {
		s.trie = append(s.trie, trieNode{v.Next, v.Fail, v.Banned})
	}
	for id, links := range aux.Driveways {
		for _, v := range links {
			s.driveways[id] = append(s.driveways[id], drivewayLink{v.To, v.Length})
		}
	}
	done()
	done = loadPhase(ctx, "runtime structural validation")
	e = s.validatePrepared(ctx)
	done()
	if e != nil {
		return nil, e
	}
	if r.Summary == nil || r.Summary.SHA256 != s.graphSHA {
		return nil, fmt.Errorf("missing prepared source summary")
	}
	a, _ := json.Marshal(r.Summary.Metadata)
	b, _ := json.Marshal(s.meta)
	if string(a) != string(b) {
		return nil, fmt.Errorf("prepared metadata differs from publication")
	}
	s.mapping = &queryMapping{data: mapped}
	s.mappingCleanup = runtime.AddCleanup(s, func(m *queryMapping) { m.close() }, s.mapping)
	ok = true
	return s, nil
}
