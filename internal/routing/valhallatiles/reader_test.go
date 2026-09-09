package valhallatiles

import (
	"archive/tar"
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This hand-encoded synthetic graph is NOT provider compatibility evidence.
// A--B--C--D, with a B--E--C detour, split across two real geographic tiles.
// The opt-in integration test separately reads the downloaded provider archive.
func fixture(t *testing.T, simple, complex bool) (*Router, []ID) {
	t.Helper()
	points := []Point{{8.749, 53.08}, {8.7502, 53.08}, {8.751, 53.08}, {8.752, 53.08}, {8.7505, 53.081}}
	bases := []ID{ID(824434)<<3 | 2, ID(824435)<<3 | 2}
	nodeIDs := []ID{bases[0], bases[1], bases[1].WithIndex(1), bases[1].WithIndex(2), bases[1].WithIndex(3)}
	pairs := [][2]int{{0, 1}, {1, 2}, {2, 3}, {1, 4}, {4, 2}}
	type fe struct {
		from, to, pair int
		id             ID
		local          int
	}
	var edges []fe
	for n := range points {
		idx := 0
		if n > 0 {
			for _, e := range edges {
				if e.from > 0 {
					idx++
				}
			}
		}
		local := 0
		for p, pair := range pairs {
			to := -1
			if pair[0] == n {
				to = pair[1]
			}
			if pair[1] == n {
				to = pair[0]
			}
			if to >= 0 {
				edges = append(edges, fe{n, to, p, nodeIDs[n].WithIndex(idx), local})
				idx++
				local++
			}
		}
	}
	lookup := func(from, to int) fe {
		for _, e := range edges {
			if e.from == from && e.to == to {
				return e
			}
		}
		panic("missing fixture edge")
	}
	path := []ID{lookup(0, 1).id, lookup(1, 2).id, lookup(2, 3).id}
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	put32 := func(b []byte, o int, v uint32) { binary.LittleEndian.PutUint32(b[o:], v) }
	put64 := func(b []byte, o int, v uint64) { binary.LittleEndian.PutUint64(b[o:], v) }
	for _, base := range bases {
		var ns []int
		var es []fe
		for n, id := range nodeIDs {
			if id.Base() == base {
				ns = append(ns, n)
			}
		}
		for _, e := range edges {
			if e.id.Base() == base {
				es = append(es, e)
			}
		}
		shapeBytes := []byte{}
		offsets := map[ID]int{}
		for _, e := range es {
			offsets[e.id] = len(shapeBytes)
			encoded := []byte{}
			var last [2]int64
			for _, p := range []Point{points[e.from], points[e.to]} {
				for axis := 0; axis < 2; axis++ {
					v := int64(math.Round(p[1-axis] * 1e6))
					d := v - last[axis]
					last[axis] = v
					encoded = binary.AppendUvarint(encoded, uint64(d<<1)^uint64(d>>63))
				}
			}
			info := make([]byte, 12)
			put32(info, 0, uint32(e.pair+100))
			put32(info, 8, uint32(len(encoded))<<4)
			shapeBytes = append(shapeBytes, info...)
			shapeBytes = append(shapeBytes, encoded...)
		}
		bins := 272 + 32*len(ns) + 48*len(es)
		cf := bins + 8*len(es)
		cr := cf
		var restriction []byte
		if complex && base == bases[1] {
			restriction = make([]byte, 32)
			put64(restriction, 0, uint64(path[0]))
			put64(restriction, 8, uint64(path[2]))
			put64(restriction, 16, 1<<4|1<<16)
			put64(restriction, 24, uint64(path[1]))
			cr += len(restriction)
		}
		info := cr
		text := info + len(shapeBytes)
		b := make([]byte, text+8)
		put64(b, 0, uint64(base))
		put32(b, 8, math.Float32bits(float32(8.5+float64(base.Tile()-824434)*.25)))
		put32(b, 12, math.Float32bits(53))
		copy(b[16:], "3.6.3")
		put64(b, 40, uint64(len(ns))|uint64(len(es))<<21)
		put32(b, 96, uint32(cf))
		put32(b, 100, uint32(cr))
		put32(b, 104, uint32(info))
		put32(b, 108, uint32(text))
		put32(b, 216, uint32(len(b)))
		put32(b, 224, uint32(len(b)))
		for i, n := range ns {
			lon := uint64(math.Round((points[n][0] - float64(math.Float32frombits(u32(b, 8)))) * 1e6))
			lat := uint64(math.Round((points[n][1] - 53) * 1e6))
			o := 272 + i*32
			put64(b, o, lat|lon<<26|1<<52)
			first, count := 0, 0
			for _, e := range es {
				if e.from == n {
					if count == 0 {
						first = e.id.Index()
					}
					count++
				}
			}
			put64(b, o+8, uint64(first)|uint64(count)<<21)
		}
		for i, e := range es {
			o := 272 + 32*len(ns) + i*48
			opp := lookup(e.to, e.from)
			mask := uint64(0)
			if simple && e.id == path[0] {
				mask = 1 << lookup(1, 2).local
			}
			put64(b, o, uint64(nodeIDs[e.to])|mask<<46|uint64(opp.local)<<54|1<<61)
			put64(b, o+8, uint64(offsets[e.id]))
			put64(b, o+16, 30|uint64(6)<<54)
			put64(b, o+24, 1|1<<12)
			put64(b, o+32, uint64(math.Round(Distance(points[e.from], points[e.to])))<<32)
			put64(b, o+40, uint64(e.local)<<32|uint64(opp.local)<<39)
			put64(b, bins+i*8, uint64(e.id))
		}
		// All fixture geometry is in row 1, column 0 (right) or 4 (left).
		bin := 5
		if base == bases[0] {
			bin = 9
		}
		for i := bin; i < 25; i++ {
			put32(b, 116+i*4, uint32(len(es)))
		}
		copy(b[cf:], restriction)
		copy(b[info:], shapeBytes)
		if err := tw.WriteHeader(&tar.Header{Name: fmt.Sprintf("%d.gph", base), Mode: 0600, Size: int64(len(b))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "fixture.tar")
	if err := os.WriteFile(file, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	sum := fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
	r, err := Open(file, sum, 4096)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	s, err := NewRouter(r)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestFixtureRestrictionsAndBoundary(t *testing.T) {
	for _, test := range []struct {
		name            string
		simple, complex bool
		steps           int
	}{{"ordinary", false, false, 3}, {"simple", true, false, 4}, {"complex", false, true, 4}} {
		t.Run(test.name, func(t *testing.T) {
			s, path := fixture(t, test.simple, test.complex)
			a, _ := s.Reader.Edge(path[0])
			b, _ := s.Reader.Edge(path[2])
			sa, _ := s.Reader.Shape(a)
			sb, _ := s.Reader.Shape(b)
			from := clip(sa.Points, .5, .5)[0]
			to := clip(sb.Points, .5, .5)[0]
			result, err := s.Route(context.Background(), from, to, 1000)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Steps) != test.steps {
				t.Fatalf("unexpected path: %+v", result.Steps)
			}
			if Distance(result.Geometry[0], from) > .01 || Distance(result.Geometry[len(result.Geometry)-1], to) > .01 {
				t.Fatal("partial geometry endpoints")
			}
			if result.Steps[0].Edge.Base() == result.Steps[len(result.Steps)-1].Edge.Base() {
				t.Fatal("did not cross boundary")
			}
			if test.simple && result.Metrics.SimpleRejected == 0 {
				t.Fatal("simple restriction not evaluated")
			}
			if test.complex && result.Metrics.ComplexRejected == 0 {
				t.Fatal("complex restriction not evaluated")
			}
			if _, err := s.Route(context.Background(), from, to, 1); err == nil {
				t.Fatal("label budget not enforced")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := s.Route(ctx, from, to, 1000); err == nil {
				t.Fatal("cancellation ignored")
			}
		})
	}
}
func TestReaderRejectsCorruptionAndMissingTiles(t *testing.T) {
	s, path := fixture(t, false, false)
	r := s.Reader
	if _, err := Open(r.f.Name(), strings.Repeat("0", 64), 4096); err == nil {
		t.Fatal("wrong checksum accepted")
	}
	if _, err := r.Edge(path[0].Base().WithIndex(999)); err == nil {
		t.Fatal("bad edge accepted")
	}
	if _, err := r.Node(ID(999)<<3 | 2); err == nil {
		t.Fatal("missing tile accepted")
	}
	for _, mutate := range []func([]byte){func(b []byte) { b[16] = '9' }, func(b []byte) { binary.LittleEndian.PutUint32(b[104:], math.MaxUint32) }, func(b []byte) { binary.LittleEndian.PutUint32(b[116:], math.MaxUint32) }} {
		tile, err := r.get(path[0])
		if err != nil {
			t.Fatal(err)
		}
		b := append([]byte(nil), tile.b...)
		mutate(b)
		if _, err := parseTile(b, tile.id); err == nil {
			t.Fatal("corrupt tile accepted")
		}
	}
}
func TestCacheEviction(t *testing.T) {
	s, path := fixture(t, false, false)
	r := s.Reader
	t0, _ := r.get(path[0])
	t1, _ := r.get(path[2])
	limit := max(len(t0.b), len(t1.b))
	r.cache = map[ID]*list.Element{}
	r.lru.Init()
	r.Stats = CacheStats{}
	r.limit = int64(limit)
	for range 5 {
		for _, id := range path {
			if _, err := r.Edge(id); err != nil {
				t.Fatal(err)
			}
		}
	}
	if r.Stats.Evictions == 0 || r.Stats.PeakBytes > int64(limit) {
		t.Fatalf("cache budget: %+v", r.Stats)
	}
}

func TestOneWayPartialAndZeroRoutes(t *testing.T) {
	s, path := fixture(t, false, false)
	e, _ := s.Reader.Edge(path[0])
	opp, err := s.opposite(e)
	if err != nil {
		t.Fatal(err)
	}
	tile, err := s.Reader.get(opp.ID)
	if err != nil {
		t.Fatal(err)
	}
	o := tile.edgeStart + opp.ID.Index()*48 + 24
	binary.LittleEndian.PutUint64(tile.b[o:], u64(tile.b, o)&^1)
	shape, _ := s.Reader.Shape(e)
	a := clip(shape.Points, .3, .3)[0]
	b := clip(shape.Points, .7, .7)[0]
	out, err := s.Route(context.Background(), a, b, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Steps) != 1 || math.Abs(out.Meters-e.Length*.4) > 1e-5 {
		t.Fatalf("partial one-way route %+v", out.Steps)
	}
	if _, err := s.Route(context.Background(), b, a, 1000); err == nil {
		t.Fatal("backwards one-way route accepted")
	}
	zero, err := s.Route(context.Background(), a, a, 1000)
	if err != nil || zero.Meters != 0 || zero.Seconds != 0 || len(zero.Geometry) != 2 {
		t.Fatalf("zero route: %+v %v", zero, err)
	}
}

func TestEncodedAccessLimits(t *testing.T) {
	for _, test := range []struct {
		name    string
		typ     uint8
		value   uint64
		allowed bool
	}{{"height-equality", 1, 190, true}, {"low-height", 1, 189, false}, {"weight-equality", 4, 180, true}, {"low-weight", 4, 179, false}, {"conditional-denied", 7, 0, false}, {"conditional-allowed", 6, 0, false}, {"destination-permission", 8, 0, false}, {"unknown", 15, 0, false}} {
		t.Run(test.name, func(t *testing.T) {
			s, path := fixture(t, false, false)
			e, _ := s.Reader.Edge(path[0])
			tile, _ := s.Reader.get(e.ID)
			at := tile.accessStart
			b := append([]byte(nil), tile.b[:at]...)
			b = append(b, make([]byte, 16)...)
			b = append(b, tile.b[at:]...)
			binary.LittleEndian.PutUint64(b[72:], 1)
			for _, offset := range []int{96, 100, 104, 108, 216, 224} {
				binary.LittleEndian.PutUint32(b[offset:], u32(b, offset)+16)
			}
			binary.LittleEndian.PutUint64(b[at:], uint64(e.ID.Index())|uint64(test.typ)<<22|1<<28)
			binary.LittleEndian.PutUint64(b[at+8:], test.value)
			updated, err := parseTile(b, tile.id)
			if err != nil {
				t.Fatal(err)
			}
			s.Reader.cache[tile.id].Value = cached{tile.id, updated}
			ok, err := s.Allowed(e)
			if err != nil || ok != test.allowed {
				t.Fatalf("allowed=%v error=%v", ok, err)
			}
		})
	}
}

func TestMalformedGeometry(t *testing.T) {
	s, path := fixture(t, false, false)
	e, _ := s.Reader.Edge(path[0])
	tile, _ := s.Reader.get(e.ID)
	o := tile.info + e.Info
	binary.LittleEndian.PutUint32(tile.b[o+8:], 1<<4)
	tile.b[o+12] = 128
	if _, err := s.Reader.Shape(e); err == nil {
		t.Fatal("truncated varint accepted")
	}
}
