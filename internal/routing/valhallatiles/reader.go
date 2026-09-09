// Package valhallatiles is an isolated feasibility reader for pinned Valhalla
// 3.6.3 little-endian road tiles. It is not used by the production router.
// Layout references: upstream e2f017b16080f49203de245a211b09efab09cf72,
// valhalla/baldr/{graphtileheader,nodeinfo,directededge,edgeinfo}.h.
package valhallatiles

import (
	"archive/tar"
	"bytes"
	"container/list"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strings"
)

type ID uint64

const idMask = uint64(1<<46 - 1)
const invalid ID = ID(idMask)

func (id ID) Base() ID           { return id & (1<<25 - 1) }
func (id ID) Level() int         { return int(id & 7) }
func (id ID) Tile() int          { return int(id>>3) & (1<<22 - 1) }
func (id ID) Index() int         { return int(id >> 25) }
func (id ID) WithIndex(i int) ID { return id.Base() | ID(i)<<25 }
func (id ID) String() string     { return fmt.Sprintf("%d/%d/%d", id.Level(), id.Tile(), id.Index()) }

type Point [2]float64 // WGS84 longitude, latitude, degrees.
func (p Point) valid() bool {
	return !math.IsNaN(p[0]) && !math.IsNaN(p[1]) && math.Abs(p[0]) <= 180 && math.Abs(p[1]) <= 90
}
func Distance(a, b Point) float64 {
	r := math.Pi / 180
	h := math.Pow(math.Sin((b[1]-a[1])*r/2), 2) + math.Cos(a[1]*r)*math.Cos(b[1]*r)*math.Pow(math.Sin((b[0]-a[0])*r/2), 2)
	return 6371008.8 * 2 * math.Asin(math.Min(1, math.Sqrt(h)))
}

type Node struct {
	ID                                                     ID
	Point                                                  Point
	EdgeIndex, EdgeCount, TransitionIndex, TransitionCount int
	Access                                                 uint16
}
type Edge struct {
	ID, End                                                                       ID
	Info                                                                          int
	Restrictions, OppIndex, LocalIndex, OppLocalIndex, Speed, Use, Class, Surface uint8
	Access, ReverseAccess, AccessRestriction                                      uint16
	Length                                                                        float64
	Forward, Shortcut, Destination, Bridge, Tunnel, Roundabout                    bool
}
type AccessRestriction struct {
	Edge              int
	Type              uint8
	Modes             uint16
	Value             uint64
	ExceptDestination bool
}
type Restriction struct {
	Path  []ID
	Type  uint8
	Timed bool
}
type Shape struct {
	Way        uint64
	Names      []string
	Points     []Point
	SpeedLimit uint8
	TagTypes   []uint8 // Presence only; tagged payloads are not interpreted.
}
type tile struct {
	b                                                                                                          []byte
	id                                                                                                         ID
	nodes, edges, transitions, access, edgeStart, accessStart, binStart, forward, reverse, info, text, textEnd int
}
type entry struct{ offset, size int64 }
type cached struct {
	id   ID
	tile *tile
}
type CacheStats struct{ Bytes, PeakBytes, Loads, Hits, Evictions int64 }

// Reader is single-owner, not concurrent. Methods return copied records, never
// byte views into cached tiles. CacheBytes bounds retained tile payload; Go heap,
// transient allocations, archive index, query labels and OS file cache are separate.
type Reader struct {
	f     *os.File
	index map[ID]entry
	cache map[ID]*list.Element
	lru   list.List
	limit int64
	Stats CacheStats
}

// Open verifies a caller-pinned digest before indexing a seekable, uncompressed
// tar. It never extracts files, constructs graph arrays, or contacts the network.
// Explicit small-experiment caps: 64 MiB archive, 128 tiles, 16 MiB per tile.
func Open(path, digest string, cacheBytes int64) (*Reader, error) {
	if len(digest) != 64 {
		return nil, errors.New("a pinned SHA-256 is required")
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return nil, err
	}
	if cacheBytes < 272 || cacheBytes > 64<<20 {
		return nil, errors.New("cache must be between 272 bytes and 64 MiB")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
		}
	}()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() > 64<<20 {
		return nil, errors.New("archive exceeds small-prototype 64 MiB cap")
	}
	h := sha256.New()
	if _, err = io.Copy(h, f); err != nil {
		return nil, err
	}
	if hex.EncodeToString(h.Sum(nil)) != strings.ToLower(digest) {
		return nil, errors.New("archive SHA-256 mismatch")
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	r := &Reader{f: f, index: map[ID]entry{}, cache: map[ID]*list.Element{}, limit: cacheBytes}
	tr := tar.NewReader(f)
	var dataset, checksum uint64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if !strings.HasSuffix(hdr.Name, ".gph") {
			continue
		}
		if hdr.Typeflag != tar.TypeReg || hdr.Size < 272 || hdr.Size > 16<<20 || hdr.Size > cacheBytes {
			return nil, fmt.Errorf("unsupported tile size/type: %s", hdr.Name)
		}
		off, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			return nil, err
		}
		var header [272]byte
		if _, err = io.ReadFull(tr, header[:]); err != nil {
			return nil, err
		}
		id := ID(binary.LittleEndian.Uint64(header[:]) & idMask)
		if len(r.index) == 0 {
			dataset, checksum = u64(header[:], 32), u64(header[:], 88)
		} else if dataset != u64(header[:], 32) || checksum != u64(header[:], 88) {
			return nil, errors.New("mixed tile dataset/checksum headers")
		}
		if id.Index() != 0 || id.Level() > 2 {
			return nil, errors.New("only road tile bases supported")
		}
		if _, exists := r.index[id]; exists {
			return nil, errors.New("duplicate tile")
		}
		r.index[id] = entry{off, hdr.Size}
		if len(r.index) > 128 {
			return nil, errors.New("tile count exceeds prototype cap")
		}
	}
	if len(r.index) == 0 {
		return nil, errors.New("no road tiles")
	}
	ok = true
	return r, nil
}
func (r *Reader) Close() error { r.cache = nil; r.lru.Init(); r.Stats.Bytes = 0; return r.f.Close() }
func (r *Reader) TileIDs() []ID {
	ids := make([]ID, 0, len(r.index))
	for id := range r.index {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
func (r *Reader) get(id ID) (*tile, error) {
	id = id.Base()
	if el := r.cache[id]; el != nil {
		r.lru.MoveToFront(el)
		r.Stats.Hits++
		return el.Value.(cached).tile, nil
	}
	e, ok := r.index[id]
	if !ok {
		return nil, fmt.Errorf("missing tile %s (incomplete dataset)", id)
	}
	for r.Stats.Bytes+e.size > r.limit {
		el := r.lru.Back()
		c := el.Value.(cached)
		r.Stats.Bytes -= int64(len(c.tile.b))
		delete(r.cache, c.id)
		r.lru.Remove(el)
		r.Stats.Evictions++
	}
	b := make([]byte, e.size)
	if _, err := r.f.ReadAt(b, e.offset); err != nil {
		return nil, err
	}
	t, err := parseTile(b, id)
	if err != nil {
		return nil, fmt.Errorf("tile %s: %w", id, err)
	}
	r.cache[id] = r.lru.PushFront(cached{id, t})
	r.Stats.Bytes += e.size
	r.Stats.Loads++
	r.Stats.PeakBytes = max(r.Stats.PeakBytes, r.Stats.Bytes)
	return t, nil
}
func u32(b []byte, o int) uint32            { return binary.LittleEndian.Uint32(b[o : o+4]) }
func u64(b []byte, o int) uint64            { return binary.LittleEndian.Uint64(b[o : o+8]) }
func field(w uint64, start, width uint) int { return int(w >> start & (1<<width - 1)) }
func parseTile(b []byte, id ID) (*tile, error) {
	if len(b) < 272 {
		return nil, errors.New("short header")
	}
	if string(bytes.TrimRight(b[16:32], "\x00")) != "3.6.3" {
		return nil, errors.New("unsupported tile version (only pinned 3.6.3 layout)")
	}
	if ID(u64(b, 0)&idMask) != id || int(u32(b, 224)) != len(b) {
		return nil, errors.New("tile identity/size mismatch")
	}
	// Transit records are deliberately unsupported, even inside a road tile.
	if u64(b, 56) != 0 || field(u64(b, 64), 0, 24) != 0 {
		return nil, errors.New("transit records unsupported")
	}
	t := &tile{b: b, id: id, nodes: field(u64(b, 40), 0, 21), edges: field(u64(b, 40), 21, 21), transitions: field(u64(b, 48), 0, 22), access: field(u64(b, 72), 0, 24), forward: int(u32(b, 96)), reverse: int(u32(b, 100)), info: int(u32(b, 104)), text: int(u32(b, 108)), textEnd: int(u32(b, 216))}
	t.edgeStart = 272 + t.nodes*32 + t.transitions*8
	t.accessStart = t.edgeStart + t.edges*48
	if u64(b, 0)>>63 != 0 {
		t.accessStart += t.edges * 8
	}
	t.binStart = t.accessStart + t.access*16 + field(u64(b, 64), 24, 24)*8 + field(u64(b, 48), 32, 21)*8 + field(u64(b, 72), 24, 16)*16
	if t.edgeStart > len(b) || t.accessStart > len(b) || t.binStart > t.forward || t.forward > t.reverse || t.reverse > t.info || t.info > t.text || t.text > t.textEnd || t.textEnd > len(b) {
		return nil, errors.New("invalid section extents")
	}
	prev := 0
	for i := 0; i < 25; i++ {
		v := int(u32(b, 116+i*4))
		if v < prev || v > (t.forward-t.binStart)/8 {
			return nil, errors.New("invalid spatial bin extent")
		}
		prev = v
	}
	return t, nil
}
func (t *tile) node(i int) (Node, error) {
	if i < 0 || i >= t.nodes {
		return Node{}, errors.New("node index out of range")
	}
	o := 272 + i*32
	a, b, c := u64(t.b, o), u64(t.b, o+8), u64(t.b, o+16)
	n := Node{ID: t.id.WithIndex(i), Point: Point{float64(math.Float32frombits(u32(t.b, 8))) + float64(field(a, 26, 22))*1e-6 + float64(field(a, 48, 4))*1e-7, float64(math.Float32frombits(u32(t.b, 12))) + float64(field(a, 0, 22))*1e-6 + float64(field(a, 22, 4))*1e-7}, Access: uint16(a >> 52), EdgeIndex: field(b, 0, 21), EdgeCount: field(b, 21, 7), TransitionIndex: field(c, 0, 21), TransitionCount: field(c, 21, 3)}
	if !n.Point.valid() || n.EdgeIndex+n.EdgeCount > t.edges || n.TransitionIndex+n.TransitionCount > t.transitions {
		return Node{}, errors.New("invalid node fields")
	}
	return n, nil
}
func (r *Reader) Node(id ID) (Node, error) {
	t, err := r.get(id)
	if err != nil {
		return Node{}, err
	}
	return t.node(id.Index())
}
func (t *tile) edge(i int) (Edge, error) {
	if i < 0 || i >= t.edges {
		return Edge{}, errors.New("edge index out of range")
	}
	o := t.edgeStart + i*48
	a, b, c, d, e, f := u64(t.b, o), u64(t.b, o+8), u64(t.b, o+16), u64(t.b, o+24), u64(t.b, o+32), u64(t.b, o+40)
	x := Edge{ID: t.id.WithIndex(i), End: ID(a & idMask), Info: field(b, 0, 25), Restrictions: uint8(a >> 46), OppIndex: uint8(field(a, 54, 7)), Forward: a&(1<<61) != 0, AccessRestriction: uint16(field(b, 25, 12)), Destination: b&(1<<62) != 0, Speed: uint8(c), Use: uint8(field(c, 40, 6)), Class: uint8(field(c, 54, 3)), Surface: uint8(field(c, 57, 3)), Roundabout: c&(1<<61) != 0, Access: uint16(field(d, 0, 12)), ReverseAccess: uint16(field(d, 12, 12)), Tunnel: d&(1<<49) != 0, Bridge: d&(1<<50) != 0, Length: float64(field(e, 32, 24)), LocalIndex: uint8(field(f, 32, 7)), OppLocalIndex: uint8(field(f, 39, 7)), Shortcut: f&(1<<60) != 0}
	if x.End.Level() > 2 || x.Info+12 > t.text-t.info {
		return Edge{}, errors.New("invalid edge fields")
	}
	return x, nil
}
func (r *Reader) Edge(id ID) (Edge, error) {
	t, err := r.get(id)
	if err != nil {
		return Edge{}, err
	}
	return t.edge(id.Index())
}
func (r *Reader) Start(id ID) (Node, error) {
	t, err := r.get(id)
	if err != nil {
		return Node{}, err
	}
	if id.Index() >= t.edges {
		return Node{}, errors.New("edge index out of range")
	}
	i := sort.Search(t.nodes, func(i int) bool { return field(u64(t.b, 272+i*32+8), 0, 21) > id.Index() }) - 1
	n, err := t.node(i)
	if err != nil {
		return Node{}, err
	}
	if id.Index() >= n.EdgeIndex+n.EdgeCount {
		return Node{}, errors.New("edge has no owning node")
	}
	return n, nil
}
func (r *Reader) Transitions(n Node) ([]ID, error) {
	t, err := r.get(n.ID)
	if err != nil {
		return nil, err
	}
	if n.TransitionIndex < 0 || n.TransitionCount < 0 || n.TransitionCount > 3 || n.TransitionIndex+n.TransitionCount > t.transitions {
		return nil, errors.New("transition range out of bounds")
	}
	out := make([]ID, n.TransitionCount)
	for i := range out {
		out[i] = ID(u64(t.b, 272+t.nodes*32+(n.TransitionIndex+i)*8) & idMask)
	}
	return out, nil
}
func (r *Reader) AccessRules(e Edge) ([]AccessRestriction, error) {
	t, err := r.get(e.ID)
	if err != nil {
		return nil, err
	}
	i := sort.Search(t.access, func(i int) bool { return field(u64(t.b, t.accessStart+i*16), 0, 22) >= e.ID.Index() })
	var out []AccessRestriction
	for ; i < t.access; i++ {
		o := t.accessStart + i*16
		w := u64(t.b, o)
		if field(w, 0, 22) != e.ID.Index() {
			break
		}
		out = append(out, AccessRestriction{Edge: e.ID.Index(), Type: uint8(field(w, 22, 6)), Modes: uint16(field(w, 28, 12)), Value: u64(t.b, o+8), ExceptDestination: w&(1<<40) != 0})
	}
	return out, nil
}
func (r *Reader) Restrictions(id ID) ([]Restriction, error) {
	t, err := r.get(id)
	if err != nil {
		return nil, err
	}
	var out []Restriction
	for o := t.forward; o < t.reverse; {
		if t.reverse-o < 24 {
			return nil, errors.New("truncated complex restriction")
		}
		a, b, c := u64(t.b, o), u64(t.b, o+8), u64(t.b, o+16)
		n := field(c, 16, 5)
		if o+24+n*8 > t.reverse {
			return nil, errors.New("truncated vias")
		}
		if field(c, 4, 12)&1 != 0 {
			typ := uint8(c & 15)
			if typ > 9 {
				return nil, errors.New("probable/unknown turn restriction unsupported")
			}
			p := []ID{ID(a & idMask)}
			for i := n - 1; i >= 0; i-- {
				p = append(p, ID(u64(t.b, o+24+i*8)&idMask))
			}
			p = append(p, ID(b&idMask))
			out = append(out, Restriction{p, typ, a&(1<<46) != 0})
			if len(out) > 4096 {
				return nil, errors.New("complex restriction cap exceeded")
			}
		}
		o += 24 + n*8
	}
	return out, nil
}
func (r *Reader) Shape(e Edge) (Shape, error) {
	t, err := r.get(e.ID)
	if err != nil {
		return Shape{}, err
	}
	o := t.info + e.Info
	if o < t.info || o+12 > t.text {
		return Shape{}, errors.New("invalid edge info")
	}
	a, b := u32(t.b, o+4), u32(t.b, o+8)
	nc := int(b & 15)
	size := int(b >> 4 & 65535)
	extra := int(b >> 28 & 3)
	start := o + 12 + nc*4
	end := start + size
	if extra > 2 || end+extra > t.text {
		return Shape{}, errors.New("invalid shape extent")
	}
	s := Shape{Way: uint64(u32(t.b, o)) | uint64(a>>24)<<32 | uint64(b>>20&255)<<40, SpeedLimit: uint8(a >> 16)}
	for i := 0; i < extra; i++ {
		s.Way |= uint64(t.b[end+i]) << uint(48+8*i)
	}
	for i := 0; i < nc; i++ {
		w := u32(t.b, o+12+i*4)
		off := t.text + int(w&0xffffff)
		if off >= t.textEnd {
			return Shape{}, errors.New("name outside text section")
		}
		if w&(1<<29) != 0 {
			s.TagTypes = append(s.TagTypes, t.b[off])
			continue
		}
		z := bytes.IndexByte(t.b[off:t.textEnd], 0)
		if z < 0 {
			return Shape{}, errors.New("unterminated name")
		}
		s.Names = append(s.Names, string(t.b[off:off+z]))
	}
	lat, lon := int64(0), int64(0)
	for p := start; p < end; {
		read := func() (int64, error) {
			v, n := binary.Uvarint(t.b[p:end])
			if n <= 0 || n > 5 || v > math.MaxUint32 {
				return 0, errors.New("invalid shape varint")
			}
			p += n
			return int64(v>>1) ^ -int64(v&1), nil
		}
		dy, err := read()
		if err != nil {
			return Shape{}, err
		}
		dx, err := read()
		if err != nil {
			return Shape{}, err
		}
		lat += dy
		lon += dx
		point := Point{float64(lon) * 1e-6, float64(lat) * 1e-6}
		if !point.valid() {
			return Shape{}, errors.New("invalid shape coordinate")
		}
		s.Points = append(s.Points, point)
	}
	if len(s.Points) < 2 {
		return Shape{}, errors.New("shape needs two vertices")
	}
	if !e.Forward {
		for i, j := 0, len(s.Points)-1; i < j; i, j = i+1, j-1 {
			s.Points[i], s.Points[j] = s.Points[j], s.Points[i]
		}
	}
	return s, nil
}

// Candidates uses the provider's level-2 5x5 bins, including references to
// roads on higher levels. Missing bins at a sample's edge are not fabricated.
// This bounded experiment deliberately rejects polar/dateline requests.
func (r *Reader) Candidates(p Point, radius float64) ([]ID, error) {
	if !p.valid() || math.Abs(p[1]) > 80 || radius <= 0 || radius > 1000 || math.IsNaN(radius) {
		return nil, errors.New("unsupported snap coordinates/radius")
	}
	dy := radius / 110000
	dx := dy / math.Cos(p[1]*math.Pi/180)
	if p[0]-dx < -180 || p[0]+dx > 180 {
		return nil, errors.New("dateline snapping not implemented")
	}
	seen := map[ID]bool{}
	for y := int(math.Floor((p[1] - dy + 90) / .25)); y <= int(math.Floor((p[1]+dy+90)/.25)); y++ {
		for x := int(math.Floor((p[0] - dx + 180) / .25)); x <= int(math.Floor((p[0]+dx+180)/.25)); x++ {
			id := ID(y*1440+x)<<3 | 2
			if _, ok := r.index[id]; !ok {
				continue
			}
			t, err := r.get(id)
			if err != nil {
				return nil, err
			}
			baseX, baseY := float64(x)*.25-180, float64(y)*.25-90
			for by := 0; by < 5; by++ {
				for bx := 0; bx < 5; bx++ {
					if baseX+float64(bx+1)*.05 < p[0]-dx || baseX+float64(bx)*.05 > p[0]+dx || baseY+float64(by+1)*.05 < p[1]-dy || baseY+float64(by)*.05 > p[1]+dy {
						continue
					}
					bin := by*5 + bx
					lo := 0
					if bin > 0 {
						lo = int(u32(t.b, 116+(bin-1)*4))
					}
					hi := int(u32(t.b, 116+bin*4))
					for i := lo; i < hi; i++ {
						edge := ID(u64(t.b, t.binStart+i*8))
						if uint64(edge) > idMask {
							return nil, errors.New("invalid bin graph ID")
						}
						seen[edge] = true
						if len(seen) > 100000 {
							return nil, errors.New("snap candidate cap exceeded")
						}
					}
				}
			}
		}
	}
	out := make([]ID, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}
