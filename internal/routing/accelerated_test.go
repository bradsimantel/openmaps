package routing

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"math/rand"
	"sort"
	"testing"
)

func testTurnTable(s *Router) *preparedTurns {
	count := 0
	for _, p := range s.prefixes {
		count += len(p.next)
	}
	data := make([]byte, 64+16*len(s.prefixes)+16*count)
	first := 0
	for i, p := range s.prefixes {
		b := data[64+i*16:]
		binary.LittleEndian.PutUint32(b, uint32(first))
		binary.LittleEndian.PutUint32(b[4:], uint32(len(p.next)))
		binary.LittleEndian.PutUint32(b[8:], uint32(p.fail))
		if p.banned {
			b[13] = 1
		}
		keys := make([]ID, 0, len(p.next))
		for id := range p.next {
			keys = append(keys, id)
		}
		sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
		for _, id := range keys {
			b := data[64+16*len(s.prefixes)+16*first:]
			binary.LittleEndian.PutUint64(b, uint64(id))
			binary.LittleEndian.PutUint32(b[8:], uint32(p.next[id]))
			first++
		}
	}
	return &preparedTurns{t: &tile{b: data, size: len(data)}, states: len(s.prefixes), transitions: count}
}
func TestAcceleratedAdversarialPartials(t *testing.T) {
	for _, simple := range []bool{false, true} {
		for _, complex := range []bool{false, true} {
			s, _ := fixture(t, simple, complex)
			s.turns = testTurnTable(s)
			var edges []Edge
			s.secondsPerMeter = math.Inf(1)
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
					a, err := s.Reader.Start(e.ID)
					if err != nil {
						t.Fatal(err)
					}
					b, err := s.Reader.Node(e.End)
					if err != nil {
						t.Fatal(err)
					}
					s.secondsPerMeter = math.Min(s.secondsPerMeter, e.Length*3.6/float64(e.Speed)/Distance(a.Point, b.Point))
				}
			}
			s.secondsPerMeter *= 1 - 1e-10
			rng := rand.New(rand.NewSource(44))
			fractions := []float64{0, .01, .25, .5, .99, 1}
			for iteration := 0; iteration < 300; iteration++ {
				a, b := edges[rng.Intn(len(edges))], edges[rng.Intn(len(edges))]
				fa, fb := fractions[rng.Intn(len(fractions))], fractions[rng.Intn(len(fractions))]
				sa, err := s.Reader.Shape(a)
				if err != nil {
					t.Fatal(err)
				}
				sb, err := s.Reader.Shape(b)
				if err != nil {
					t.Fatal(err)
				}
				pa, pb := clip(sa.Points, fa, fa)[0], clip(sb.Points, fb, fb)[0]
				from, to := Snap{pa, pa, 0, a.ID, fa}, Snap{pb, pb, 0, b.ID, fb}
				want, we := s.RouteSnaps(context.Background(), from, to, 1000)
				accelerated, ae := s.routeSnaps(context.Background(), from, to, 1000, true)
				if !(errors.Is(we, ErrUnreachable) && errors.Is(ae, ErrUnreachable)) && (we != nil || ae != nil || math.Abs(accelerated.Seconds-want.Seconds) > 1e-8) {
					t.Fatalf("forced A* differs: %g %v vs %g %v", accelerated.Seconds, ae, want.Seconds, we)
				}
			}
		}
	}
}
