package valhallatiles

import (
	"bytes"
	"container/list"
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestManyToOneOpposingIndexesPreserveIncomingEdges(t *testing.T) {
	s, path := fixtureWithParallel(t, false, false, true)
	e, err := s.Reader.Edge(path[1])
	if err != nil {
		t.Fatal(err)
	}
	start, _ := s.Reader.Start(e.ID)
	end, _ := s.Reader.Node(e.End)
	var forwards, backwards []Edge
	for _, n := range []Node{start, end} {
		for i := 0; i < n.EdgeCount; i++ {
			e, err := s.Reader.Edge(n.ID.WithIndex(n.EdgeIndex + i))
			if err != nil {
				t.Fatal(err)
			}
			if n.ID == start.ID && e.End == end.ID {
				forwards = append(forwards, e)
			}
			if n.ID == end.ID && e.End == start.ID {
				backwards = append(backwards, e)
			}
		}
	}
	if len(forwards) != 2 || len(backwards) != 2 {
		t.Fatal("fixture needs parallel edges")
	}
	// Both opposing pointers select the slower parallel record. The omitted
	// incoming record remains valid and gives a strictly shorter reverse cost.
	for _, pair := range [][]Edge{forwards, backwards} {
		opposite := backwards[1]
		owner := end
		if pair[0].ID == backwards[0].ID {
			opposite = forwards[1]
			owner = start
		}
		for _, e := range pair {
			tile, _ := s.Reader.get(e.ID)
			at := tile.edgeStart + e.ID.Index()*48
			word := u64(tile.b, at)
			word = word&^(uint64(127)<<54) | uint64(opposite.ID.Index()-owner.EdgeIndex)<<54
			binary.LittleEndian.PutUint64(tile.b[at:], word)
		}
	}
	tile, _ := s.Reader.get(backwards[0].ID)
	at := tile.edgeStart + backwards[0].ID.Index()*48 + 16
	word := u64(tile.b, at)
	binary.LittleEndian.PutUint64(tile.b[at:], word&^255|100)
	got := map[ID]bool{}
	if _, err := s.incoming(start, false, func(e Edge, source ID) error {
		if source == end.ID {
			got[e.ID] = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !got[backwards[0].ID] || !got[backwards[1].ID] {
		t.Fatal("lost duplicate incoming identity")
	}
	support, meta, err := scanReverseSupport(context.Background(), s.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Exceptions == 0 {
		t.Fatal("source certificate missed nonreciprocal incoming records")
	}
	s.reverseSupport = support
	got = map[ID]bool{}
	if _, err := s.incoming(start, false, func(e Edge, source ID) error {
		if source == end.ID {
			got[e.ID] = true
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !got[backwards[0].ID] || !got[backwards[1].ID] {
		t.Fatal("certified reverse path lost exceptional incoming identity")
	}
	d, err := newDenseNodes(s.Reader)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if err := diskReserve(out, 1<<20, 32<<30); err != nil {
		t.Skip("declared disk reserve unavailable")
	}
	pin, err := prepareLandmarkVector(context.Background(), s, d, out, "reverse.bin", start.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(out, pin.File))
	if err != nil {
		t.Fatal(err)
	}
	index, _ := d.index(end.ID)
	distance := float64(math.Float32frombits(binary.LittleEndian.Uint32(b[64+int(index)*4:])))
	fast, _ := s.Reader.Edge(backwards[0].ID)
	want := fast.Length * 3.6 / float64(fast.Speed)
	if distance > want || want-distance > 1e-5 {
		t.Fatalf("reverse distance lost fast incoming edge: got %g want %g", distance, want)
	}
	s.turns = testTurnTable(s)
	reverse, err := buildRouterOrder(context.Background(), s.Reader, 4096, 65536, true)
	if err != nil {
		t.Fatal(err)
	}
	s.reverseTurns = testTurnTable(reverse)
	s.secondsPerMeter = .001
	last, _ := s.Reader.Edge(path[2])
	fromEdge, _ := s.opposite(last)
	first, _ := s.Reader.Edge(path[0])
	toEdge, _ := s.opposite(first)
	sa, _ := s.Reader.Shape(fromEdge)
	sb, _ := s.Reader.Shape(toEdge)
	pa, pb := clip(sa.Points, .5, .5)[0], clip(sb.Points, .5, .5)[0]
	a, bSnap := Snap{pa, pa, 0, fromEdge.ID, .5}, Snap{pb, pb, 0, toEdge.ID, .5}
	ref, re := s.RouteSnaps(context.Background(), a, bSnap, 1000)
	bi, be := s.routeBidirectionalSnaps(context.Background(), a, bSnap, 1000)
	if re != nil || be != nil || math.Abs(ref.Seconds-bi.Seconds) > 1e-8 {
		t.Fatalf("nonbijective reverse route differs: %g %v vs %g %v", bi.Seconds, be, ref.Seconds, re)
	}
}

func TestLandmarkReusedPagesPreserveCrossPageRecords(t *testing.T) {
	data := make([]byte, 3*pageSize)
	for i := range data {
		data[i] = byte((i*31 + i/pageSize) % 251)
	}
	path := filepath.Join(t.TempDir(), "pages.bin")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	r := &Reader{f: f, pages: map[int64]*list.Element{}, limit: pageSize, recordPageReuse: true}
	defer r.Close()
	tile := tile{reader: r, size: len(data)}
	for repeat := 0; repeat < 10; repeat++ {
		for _, offset := range []int{pageSize - 7, 2*pageSize + 9, 3, pageSize + 13, 2*pageSize - 17} {
			got, err := tile.span(offset, 48)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, data[offset:offset+48]) {
				t.Fatal("recycled page corrupted a fixed record")
			}
		}
	}
	if _, err := r.Shape(Edge{}); err == nil {
		t.Fatal("shape decoder can retain a recycled borrowed record")
	}
}
