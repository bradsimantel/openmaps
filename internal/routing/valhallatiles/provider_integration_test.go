//go:build integration

package valhallatiles

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"testing"
)

func TestProviderRestrictedRoutes(t *testing.T) {
	for _, test := range []struct {
		name   string
		path   []ID
		simple bool
	}{
		{"simple-cross-level", []ID{6912238568, 306727658386}, true},
		{"complex-no-u-turn", []ID{ID(3197)<<3 | ID(306)<<25, ID(51668)<<3 | 1 | ID(1188)<<25, ID(3197)<<3 | ID(273)<<25}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := provider(t)
			first, err := s.Reader.Edge(test.path[0])
			if err != nil {
				t.Fatal(err)
			}
			last, err := s.Reader.Edge(test.path[len(test.path)-1])
			if err != nil {
				t.Fatal(err)
			}
			a, err := s.Reader.Shape(first)
			if err != nil {
				t.Fatal(err)
			}
			b, err := s.Reader.Shape(last)
			if err != nil {
				t.Fatal(err)
			}
			p, q := clip(a.Points, .5, .5)[0], clip(b.Points, .5, .5)[0]
			from, to := Snap{Requested: p, Point: p, Edge: first.ID, Fraction: .5}, Snap{Requested: q, Point: q, Edge: last.ID, Fraction: .5}
			legal, err := s.RouteSnaps(context.Background(), from, to, 100000)
			if err != nil {
				t.Fatal(err)
			}
			verifyRoute(t, s, legal)
			// Deliberately remove restrictions ONLY from this disposable in-memory
			// comparator. The pinned provider archive is never changed.
			if test.simple {
				tile, err := s.Reader.get(first.ID)
				if err != nil {
					t.Fatal(err)
				}
				offset := tile.edgeStart + first.ID.Index()*48
				word := u64(tile.b, offset)
				binary.LittleEndian.PutUint64(tile.b[offset:], word & ^(uint64(255)<<46))
				defer binary.LittleEndian.PutUint64(tile.b[offset:], word)
			} else {
				s.prefixes = []prefix{{next: map[ID]int{}}}
			}
			unrestricted, err := s.RouteSnaps(context.Background(), from, to, 100000)
			if err != nil {
				t.Fatal(err)
			}
			if legal.Seconds <= unrestricted.Seconds {
				t.Fatalf("restriction made no difference: legal=%f comparator=%f", legal.Seconds, unrestricted.Seconds)
			}
			ids := []ID{}
			for _, step := range unrestricted.Steps {
				ids = append(ids, step.Edge)
			}
			matches := false
			for i := 0; i+len(test.path) <= len(ids); i++ {
				same := true
				for j := range test.path {
					same = same && ids[i+j] == test.path[j]
				}
				matches = matches || same
			}
			if !matches {
				for _, tid := range s.Reader.TileIDs() {
					rs, _ := s.Reader.Restrictions(tid)
					for _, r := range rs {
						for i := 0; i+len(r.Path) <= len(ids); i++ {
							same := true
							for j := range r.Path {
								same = same && ids[i+j] == r.Path[j]
							}
							if same {
								t.Logf("COMPARATOR VIOLATION %v type=%d", r.Path, r.Type)
							}
						}
					}
				}
				t.Fatalf("comparator did not traverse frozen prohibited path: %v", ids)
			}
			t.Logf("provider path=%v from=%v to=%v names=%v/%v legal %.3f m %.3f s; restriction-disabled comparator %.3f m %.3f s; rejected simple=%d complex=%d", test.path, p, q, a.Names, b.Names, legal.Meters, legal.Seconds, unrestricted.Meters, unrestricted.Seconds, legal.Metrics.SimpleRejected, legal.Metrics.ComplexRejected)
		})
	}
}

func provider(t *testing.T) *Router {
	t.Helper()
	file := os.Getenv("OPENMAPS_VALHALLA_TAR")
	if file == "" {
		t.Skip("set OPENMAPS_VALHALLA_TAR to the pinned downloaded Bremen tar")
	}
	r, err := Open(file, "92e3c58cb1f69b9392b96dc98477b295b491b77617b25f6b5ae5b70c1d3a0b91", 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	s, err := NewRouter(r)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestProviderBoundary(t *testing.T) {
	s := provider(t)
	audit, err := s.Audit()
	if err != nil {
		t.Fatal(err)
	}
	if audit.Nodes != 70224 || audit.Edges != 151586 || audit.CrossTileEdges != 212 || audit.ComplexRestrictions != 140 {
		t.Fatalf("unexpected pinned inventory: %+v", audit)
	}
	a, b := Point{8.745062, 53.083827}, Point{8.765082, 53.085226}
	route, err := s.Route(context.Background(), a, b, 100000)
	if err != nil {
		t.Fatal(err)
	}
	levels := map[int]bool{}
	tiles := map[ID]bool{}
	for _, step := range route.Steps {
		e, err := s.Reader.Edge(step.Edge)
		if err != nil {
			t.Fatal(err)
		}
		if e.Shortcut {
			t.Fatal("shortcut traversed")
		}
		levels[e.ID.Level()] = true
		tiles[e.ID.Base()] = true
	}
	if len(tiles) < 3 || !levels[1] || !levels[2] || route.Meters < 1800 || route.Meters > 3000 {
		t.Fatalf("boundary/transition route: %+v", route)
	}
	verifyRoute(t, s, route)
	// Exercise real eviction and require exactly the same path and cost.
	s.Reader.limit = 8 << 20
	for s.Reader.Stats.Bytes > s.Reader.limit {
		el := s.Reader.lru.Back()
		c := el.Value.(cached)
		s.Reader.Stats.Bytes -= int64(len(c.tile.b))
		delete(s.Reader.cache, c.id)
		s.Reader.lru.Remove(el)
	}
	other, err := s.Route(context.Background(), a, b, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if route.Seconds != other.Seconds {
		t.Fatal("cache changes route cost")
	}
	x, _ := json.Marshal(route.Steps)
	y, _ := json.Marshal(other.Steps)
	if string(x) != string(y) {
		t.Fatal("cache changes path")
	}
	if s.Reader.Stats.Bytes > s.Reader.limit {
		t.Fatal("cache budget exceeded")
	}
}

// Independent output walk: verify adjacency through explicit transitions, mode
// rules, turn masks and whole forbidden subsequences without the search trie.
func verifyRoute(t *testing.T, s *Router, result Result) {
	t.Helper()
	var ids []ID
	meters, seconds := 0.0, 0.0
	for i, step := range result.Steps {
		e, err := s.Reader.Edge(step.Edge)
		if err != nil {
			t.Fatal(err)
		}
		ok, err := s.Allowed(e)
		if err != nil || !ok {
			t.Fatalf("forbidden output edge %s: %v", e.ID, err)
		}
		if i > 0 {
			previous, _ := s.Reader.Edge(result.Steps[i-1].Edge)
			start, err := s.Reader.Start(e.ID)
			if err != nil {
				t.Fatal(err)
			}
			if start.ID != previous.End {
				n, err := s.Reader.Node(previous.End)
				if err != nil {
					t.Fatal(err)
				}
				trans, err := s.Reader.Transitions(n)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, id := range trans {
					found = found || id == start.ID
				}
				if !found {
					t.Fatalf("disconnected route edges: %s %s", previous.ID, e.ID)
				}
			}
			if previous.OppLocalIndex == e.LocalIndex || e.LocalIndex < 8 && previous.Restrictions&(1<<e.LocalIndex) != 0 {
				t.Fatal("prohibited simple maneuver in output")
			}
		}
		ids = append(ids, e.ID)
		meters += e.Length * (step.To - step.From)
		seconds += e.Length * (step.To - step.From) * 3.6 / float64(e.Speed)
	}
	for _, tile := range s.Reader.TileIDs() {
		rs, err := s.Reader.Restrictions(tile)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			for i := 0; i+len(r.Path) <= len(ids); i++ {
				same := true
				for j := range r.Path {
					same = same && ids[i+j] == r.Path[j]
				}
				if same {
					t.Fatal("complex restricted sequence in output")
				}
			}
		}
	}
	if meters != result.Meters || seconds != result.Seconds {
		t.Fatal("cost reconstruction mismatch")
	}
	if Distance(result.Geometry[0], result.Origin.Point) > .1 || Distance(result.Geometry[len(result.Geometry)-1], result.Destination.Point) > .1 {
		t.Fatal("snap/geometry mismatch")
	}
}

func TestProviderRestrictionSeeds(t *testing.T) {
	if os.Getenv("OPENMAPS_VALHALLA_DISCOVER") == "" {
		t.Skip("explicit source seed discovery")
	}
	s := provider(t)
	simpleCount, complexCount := 0, 0
	for _, tile := range s.Reader.TileIDs() {
		tt, _ := s.Reader.get(tile)
		count := tt.edges
		for i := 0; i < count && simpleCount < 8; i++ {
			e, _ := s.Reader.Edge(tile.WithIndex(i))
			ok, err := s.Allowed(e)
			if err != nil {
				t.Fatal(err)
			}
			if !ok || e.Restrictions == 0 {
				continue
			}
			edges, _, err := s.departures(e.End)
			if err != nil {
				t.Fatal(err)
			}
			for _, next := range edges {
				ok, err := s.Allowed(next)
				if err != nil {
					t.Fatal(err)
				}
				if ok && next.LocalIndex < 8 && e.Restrictions&(1<<next.LocalIndex) != 0 && e.OppLocalIndex != next.LocalIndex {
					a, _ := s.Reader.Shape(e)
					b, _ := s.Reader.Shape(next)
					t.Logf("simple %d %d, %v -> %v, %v", e.ID, next.ID, a.Names, b.Names, a.Points[len(a.Points)-1])
					simpleCount++
					break
				}
			}
		}
		rs, err := s.Reader.Restrictions(tile)
		if err != nil {
			t.Fatal(err)
		}
		for _, restriction := range rs {
			if complexCount >= 8 {
				break
			}
			all := true
			for _, id := range restriction.Path {
				e, err := s.Reader.Edge(id)
				if err != nil {
					t.Fatal(err)
				}
				ok, err := s.Allowed(e)
				if err != nil {
					t.Fatal(err)
				}
				all = all && ok
			}
			if all {
				t.Logf("complex %v type=%d", restriction.Path, restriction.Type)
				complexCount++
			}
		}
	}
}
