package routing

import (
	"context"
	"errors"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestIndexedNodeHeap(t *testing.T) {
	rng := rand.New(rand.NewSource(812))
	h := nodeHeap{cost: make([]float64, 1000), positions: make([]int32, 1000)}
	for i := range h.cost {
		h.cost[i] = rng.Float64() * 1000
		h.push(uint32(i))
	}
	for i := 0; i < 3000; i++ {
		n := uint32(rng.Intn(len(h.cost)))
		h.cost[n] *= .75
		h.push(n)
	}
	last := -1.0
	seen := map[uint32]bool{}
	for len(h.nodes) > 0 {
		n := h.pop()
		if seen[n] || h.cost[n] < last || h.positions[n] != -1 {
			t.Fatal("decrease-key heap lost order or uniqueness")
		}
		seen[n], last = true, h.cost[n]
	}
	if len(seen) != len(h.cost) {
		t.Fatal("lost nodes")
	}
}

func TestLandmarkPreparedPartialsAndCorruption(t *testing.T) {
	ctx := context.Background()
	dir := syntheticPrepared(t)
	s, err := OpenPreparedRouter(ctx, dir, pageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Reader.Close()
	var seeds []LandmarkSeed
	var edges []Edge
	for _, id := range s.Reader.TileIDs() {
		tile, err := s.Reader.get(id)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < tile.edges; i++ {
			e, err := s.Reader.Edge(id.WithIndex(i))
			if err != nil {
				t.Fatal(err)
			}
			edges = append(edges, e)
		}
	}
	for _, i := range []int{0, len(edges) - 1} {
		shape, err := s.Reader.Shape(edges[i])
		if err != nil {
			t.Fatal(err)
		}
		seeds = append(seeds, LandmarkSeed{Name: edges[i].ID.String(), Point: clip(shape.Points, .5, .5)[0]})
	}
	out := filepath.Join(t.TempDir(), "landmarks")
	if err := PrepareLandmarks(ctx, dir, out, seeds); err != nil {
		t.Fatal(err)
	}
	prefix := filepath.Join(t.TempDir(), "prefix")
	if err := PublishLandmarkPrefix(ctx, dir, out, prefix, 1); err != nil {
		t.Fatal(err)
	}
	p, err := OpenPreparedRouter(ctx, dir, pageSize)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.EnableLandmarks(ctx, dir, prefix); err != nil {
		t.Fatal(err)
	}
	p.Reader.Close()
	if err := PublishLandmarkPrefix(ctx, dir, out, prefix, 1); err == nil {
		t.Fatal("existing immutable publication overwritten")
	}
	if err := PublishLandmarkPrefix(ctx, dir, out, filepath.Join(t.TempDir(), "incomplete"), len(seeds)+1); err == nil {
		t.Fatal("incomplete pair publication accepted")
	}
	if err := s.EnablePotential(dir); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableLandmarks(ctx, dir, out); err != nil {
		t.Fatal(err)
	}
	for _, a := range edges {
		for _, b := range edges {
			for _, fraction := range []float64{0, .01, .5, .99, 1} {
				sa, _ := s.Reader.Shape(a)
				sb, _ := s.Reader.Shape(b)
				pa, pb := clip(sa.Points, fraction, fraction)[0], clip(sb.Points, 1-fraction, 1-fraction)[0]
				from, to := Snap{pa, pa, 0, a.ID, fraction}, Snap{pb, pb, 0, b.ID, 1 - fraction}
				want, we := s.RouteSnaps(ctx, from, to, 1000)
				got, ge := s.routeSnaps(ctx, from, to, 1000, true)
				if errors.Is(we, ErrUnreachable) && errors.Is(ge, ErrUnreachable) {
					continue
				}
				if we != nil || ge != nil || math.Abs(want.Seconds-got.Seconds) > 1e-8 {
					t.Fatalf("landmark partial %s to %s: %g %v vs reference %g %v", a.ID, b.ID, got.Seconds, ge, want.Seconds, we)
				}
			}
		}
	}
	// A second reader reopens persisted vectors; changing a byte must fail before use.
	r, err := OpenPreparedRouter(ctx, dir, pageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Reader.Close()
	path := filepath.Join(out, "landmark-00-false.bin")
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{127}, 65); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := r.EnableLandmarks(ctx, dir, out); err == nil {
		t.Fatal("corrupt landmark vector accepted")
	}
	if err := PublishLandmarkPrefix(ctx, dir, out, filepath.Join(t.TempDir(), "corrupt"), 1); err == nil {
		t.Fatal("corrupt vector published")
	}
}

func TestLandmarkQuantizationAndDisconnectedBounds(t *testing.T) {
	l := landmarkTables{vectors: make([][2]*tile, 1)}
	var a, b landmarkValues
	a[0] = [2]uint32{math.Float32bits(100000.125), math.Float32bits(40)}
	b[0] = [2]uint32{math.Float32bits(100000.5), math.Float32bits(39.8)}
	if got := l.bound(a, b); got < .3 || got > .375 {
		t.Fatalf("nonconservative bound %g", got)
	}
	a[0] = [2]uint32{0x7f800000, 0x7f800000}
	if got := l.bound(a, b); got != 0 {
		t.Fatalf("infinite component produced %g", got)
	}
}
