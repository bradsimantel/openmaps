//go:build integration

package valhallatiles

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"reflect"
	"testing"
)

func scoutProvider(t *testing.T, cache int64) *Router {
	t.Helper()
	dir := os.Getenv("OPENMAPS_SCOUT_DIR")
	if dir == "" {
		t.Skip("set OPENMAPS_SCOUT_DIR to the pinned downloaded packages")
	}
	b, err := os.ReadFile("../../../imports/valhalla-scout-bremen.lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock ScoutLock
	if err := json.Unmarshal(b, &lock); err != nil {
		t.Fatal(err)
	}
	r, err := OpenScout(context.Background(), dir, t.TempDir(), lock, cache)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	s, err := NewRouter(r)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestScoutBoundary(t *testing.T) {
	s := scoutProvider(t, 16<<20)
	a, b := Point{8.745062, 53.083827}, Point{8.765082, 53.085226}
	s.Reader.ClearCache()
	first, err := s.Route(context.Background(), a, b, 100000)
	if err != nil {
		t.Fatal(err)
	}
	verifyRoute(t, s, first)
	tiles := map[ID]bool{}
	levels := map[int]bool{}
	for _, step := range first.Steps {
		tiles[step.Edge.Base()] = true
		levels[step.Edge.Level()] = true
	}
	if len(tiles) < 3 || !levels[1] || !levels[2] || math.Abs(first.Meters-2013.2172798957988) > .01 {
		t.Fatalf("boundary changed: %+v", first)
	}
	for _, budget := range []int64{1 << 20, 8 << 20} {
		s.Reader.ClearCache()
		s.Reader.limit = budget
		other, err := s.Route(context.Background(), a, b, 100000)
		if err != nil {
			t.Fatal(err)
		}
		if other.Seconds != first.Seconds || !reflect.DeepEqual(other.Steps, first.Steps) || !reflect.DeepEqual(other.Geometry, first.Geometry) {
			t.Fatal("cache changes route")
		}
		if s.Reader.Stats.PeakBytes > budget || s.Reader.Stats.Evictions == 0 {
			t.Fatalf("cache not exercised %+v", s.Reader.Stats)
		}
	}
	// A missing upper package must be an incomplete-data error, not no-route.
	delete(s.Reader.tiles, ID(3197)<<3)
	delete(s.Reader.index, ID(3197)<<3)
	_, err = s.Route(context.Background(), a, b, 100000)
	var missing *MissingTileError
	if !errors.As(err, &missing) || missing.Tile != ID(3197)<<3 {
		t.Fatalf("missing package: %v", err)
	}
	t.Logf("tile boundary %.3fm %.3fs, steps=%d", first.Meters, first.Seconds, len(first.Steps))
}

func TestScoutLongerRouteAndIncompleteArea(t *testing.T) {
	s := scoutProvider(t, 32<<20)
	a, b := Point{8.745062, 53.083827}, Point{8.7154925, 53.135412}
	result, err := s.Route(context.Background(), a, b, 200000)
	if err != nil {
		t.Fatal(err)
	}
	verifyRoute(t, s, result)
	packages := map[string]bool{}
	levels := map[int]bool{}
	for _, step := range result.Steps {
		packages[s.Reader.Package(step.Edge)] = true
		levels[step.Edge.Level()] = true
	}
	if result.Meters < 12000 || result.Meters > 16000 || len(packages) < 2 || len(levels) != 3 {
		t.Fatalf("longer route: %fm packages=%v levels=%v", result.Meters, packages, levels)
	}
	// The farther attempted endpoint requires data east of this bounded set.
	_, err = s.Route(context.Background(), a, Point{8.625, 53.18}, 200000)
	var missing *MissingTileError
	if !errors.As(err, &missing) || missing.Tile != ID(824436)<<3|2 {
		t.Fatalf("incomplete-area route: %v", err)
	}
	t.Logf("longer route %.3fm %.3fs steps=%d vertices=%d labels=%d packages=%v", result.Meters, result.Seconds, len(result.Steps), len(result.Geometry), result.Metrics.Labels, packages)
}

func TestScoutSpatialPackageBoundary(t *testing.T) {
	s := scoutProvider(t, 8<<20)
	// Source-selected midpoints on Adelheider Straße, either side of 53° N.
	a, b := Point{8.61671082563697, 52.999802063019516}, Point{8.615838, 53.000541999999996}
	for _, pair := range [][2]Point{{a, b}, {b, a}} {
		result, err := s.Route(context.Background(), pair[0], pair[1], 100000)
		if err != nil {
			t.Fatal(err)
		}
		verifyRoute(t, s, result)
		packages := map[string]bool{}
		crossed := false
		for _, step := range result.Steps {
			packages[s.Reader.Package(step.Edge)] = true
			e, err := s.Reader.Edge(step.Edge)
			if err != nil {
				t.Fatal(err)
			}
			packages[s.Reader.Package(e.End)] = true // opposing/node dependency can live across the boundary
			crossed = crossed || s.Reader.Package(e.ID) != s.Reader.Package(e.End)
		}
		if !packages["1983"] || !packages["1985"] || !crossed || result.Meters < 50 || result.Meters > 500 || result.Origin.GapMeters > .01 || result.Destination.GapMeters > .01 {
			t.Fatalf("spatial package crossing: %+v", result)
		}
		t.Logf("spatial package crossing %.3fm %.3fs steps=%d", result.Meters, result.Seconds, len(result.Steps))
	}
}

func TestScoutInventory(t *testing.T) {
	s := scoutProvider(t, 32<<20)
	report, err := s.AuditSample(true)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Tiles) != 35 || len(report.MissingReferences) == 0 || report.CrossPackageTransitions == 0 || report.TimedTurns == 0 || report.AccessTypes[6] == 0 || report.AccessTypes[7] == 0 {
		t.Fatalf("unexpected sample inventory: %+v", report)
	}
	for _, id := range s.Reader.TileIDs() {
		rs, err := s.Reader.Restrictions(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, rule := range rs {
			if rule.Timed {
				state := 0
				banned := false
				for _, edge := range rule.Path {
					state, banned = s.advance(state, edge)
				}
				if !banned {
					t.Fatal("timed restriction not enforced at all times")
				}
			}
		}
	}
	t.Logf("nodes=%d edges=%d restrictions=%d timed=%d missing tiles=%d cross-package transitions=%d", report.Nodes, report.Edges, report.ComplexRestrictions, report.TimedTurns, len(report.MissingReferences), report.CrossPackageTransitions)
}

func TestProviderPageCache(t *testing.T) {
	s := provider(t)
	a, b := Point{8.745062, 53.083827}, Point{8.765082, 53.085226}
	want, err := s.Route(context.Background(), a, b, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Reader.UsePageCache(); err != nil {
		t.Fatal(err)
	}
	s.Reader.limit = pageSize
	s.Reader.ClearCache()
	got, err := s.Route(context.Background(), a, b, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if got.Seconds != want.Seconds || !reflect.DeepEqual(got.Steps, want.Steps) || !reflect.DeepEqual(got.Geometry, want.Geometry) {
		t.Fatal("page cache changes Librescoot reference")
	}
	verifyRoute(t, s, got)
}

func TestScoutRestrictedRoutes(t *testing.T) {
	for _, test := range []struct {
		name   string
		path   []ID
		simple bool
	}{
		{"simple-package-boundary", []ID{4756844078056, 174379146}, true},
		{"complex-package-boundary", []ID{4869586969576, 380071464609, 4818181579752}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := scoutProvider(t, 16<<20)
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
			legal, err := s.RouteSnaps(context.Background(), from, to, 200000)
			if err != nil {
				t.Fatal(err)
			}
			verifyRoute(t, s, legal)
			packages := map[string]bool{}
			for _, step := range legal.Steps {
				packages[s.Reader.Package(step.Edge)] = true
			}
			if len(packages) != 2 {
				t.Fatal("legal route did not cross packages")
			}
			if test.simple {
				// Mutate only this disposable spool, never the downloaded package.
				tile, _ := s.Reader.get(first.ID)
				offset := tile.offset + int64(tile.edgeStart+first.ID.Index()*48)
				var word [8]byte
				if _, err := s.Reader.f.ReadAt(word[:], offset); err != nil {
					t.Fatal(err)
				}
				binary.LittleEndian.PutUint64(word[:], u64(word[:], 0)&^(uint64(255)<<46))
				if _, err := s.Reader.f.WriteAt(word[:], offset); err != nil {
					t.Fatal(err)
				}
				s.Reader.ClearCache()
			} else {
				s.prefixes = []prefix{{next: map[ID]int{}}}
			}
			other, err := s.RouteSnaps(context.Background(), from, to, 200000)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for i := 0; i+len(test.path) <= len(other.Steps); i++ {
				match := true
				for j, id := range test.path {
					match = match && other.Steps[i+j].Edge == id
				}
				found = found || match
			}
			if !found || legal.Seconds <= other.Seconds {
				t.Fatalf("frozen restriction did not change route: legal=%f comparator=%f steps=%v", legal.Seconds, other.Seconds, other.Steps)
			}
			t.Logf("path=%v from=%v to=%v names=%v/%v legal %.3fm %.3fs; comparator %.3fm %.3fs; steps=%d rejected simple=%d complex=%d", test.path, p, q, a.Names, b.Names, legal.Meters, legal.Seconds, other.Meters, other.Seconds, len(legal.Steps), legal.Metrics.SimpleRejected, legal.Metrics.ComplexRejected)
		})
	}
}

func TestScoutDiscover(t *testing.T) {
	if os.Getenv("OPENMAPS_SCOUT_DISCOVER") == "" {
		t.Skip("explicit source seed discovery")
	}
	s := scoutProvider(t, 16<<20)
	simple, complex := 0, 0
	inside := func(p Point) bool { return p[0] > 8.4 && p[0] < 8.95 && p[1] > 53.03 && p[1] < 53.4 }
	for _, id := range s.Reader.TileIDs() {
		tile, _ := s.Reader.get(id)
		for i := 0; i < tile.edges && simple < 12; i++ {
			e, err := s.Reader.Edge(id.WithIndex(i))
			if err != nil {
				t.Fatal(err)
			}
			if e.Shortcut || e.Restrictions == 0 {
				continue
			}
			n, err := s.Reader.Node(e.End)
			if err != nil || !inside(n.Point) {
				continue
			}
			ok, err := s.Allowed(e)
			if err != nil || !ok {
				continue
			}
			departures, _, err := s.departures(e.End)
			if err != nil {
				continue
			}
			for _, next := range departures {
				ok, err := s.Allowed(next)
				if err == nil && ok && next.LocalIndex < 8 && e.Restrictions&(1<<next.LocalIndex) != 0 && e.OppLocalIndex != next.LocalIndex {
					a, _ := s.Reader.Shape(e)
					b, _ := s.Reader.Shape(next)
					t.Logf("simple %d %d ids=%v/%v names=%v/%v at=%v", e.ID, next.ID, e.ID, next.ID, a.Names, b.Names, n.Point)
					simple++
					break
				}
			}
		}
		rs, err := s.Reader.Restrictions(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, rule := range rs {
			if complex >= 12 {
				break
			}
			all := true
			for _, id := range rule.Path {
				e, err := s.Reader.Edge(id)
				if err != nil {
					all = false
					break
				}
				n, err := s.Reader.Start(id)
				if err != nil || !inside(n.Point) {
					all = false
					break
				}
				ok, err := s.Allowed(e)
				if err != nil || !ok {
					all = false
					break
				}
			}
			if all {
				ids := []uint64{}
				for _, id := range rule.Path {
					ids = append(ids, uint64(id))
				}
				t.Logf("complex %v numeric=%v type=%d timed=%v", rule.Path, ids, rule.Type, rule.Timed)
				complex++
			}
		}
	}
}

func TestScoutSpatialPackageSeeds(t *testing.T) {
	if os.Getenv("OPENMAPS_SCOUT_DISCOVER") == "" {
		t.Skip("explicit seed discovery")
	}
	s := scoutProvider(t, 32<<20)
	count := 0
	for _, id := range s.Reader.TileIDs() {
		tile, _ := s.Reader.get(id)
		for i := 0; i < tile.edges && count < 6; i++ {
			e, err := s.Reader.Edge(id.WithIndex(i))
			if err != nil {
				t.Fatal(err)
			}
			if e.Shortcut || e.Class < 2 || e.Bridge || e.Tunnel {
				continue
			}
			if _, ok := s.Reader.index[e.End.Base()]; !ok || s.Reader.Package(e.ID) == s.Reader.Package(e.End) {
				continue
			}
			n, err := s.Reader.Node(e.End)
			if err != nil {
				t.Fatal(err)
			}
			if n.Point[0] < 8.5 || n.Point[0] > 8.85 {
				continue
			}
			ok, err := s.Allowed(e)
			if err != nil || !ok {
				continue
			}
			a, err := s.Reader.Shape(e)
			if err != nil {
				t.Fatal(err)
			}
			for j := 0; j < n.EdgeCount; j++ {
				next, err := s.Reader.Edge(n.ID.WithIndex(n.EdgeIndex + j))
				if err != nil {
					t.Fatal(err)
				}
				ok, err := s.Allowed(next)
				if err != nil || !ok || next.Shortcut || next.Class < 2 || next.Bridge || next.Tunnel || next.LocalIndex == e.OppLocalIndex {
					continue
				}
				b, err := s.Reader.Shape(next)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("spatial package %v %v from=%v to=%v names=%v/%v", e.ID, next.ID, clip(a.Points, .5, .5)[0], clip(b.Points, .5, .5)[0], a.Names, b.Names)
				count++
				break
			}
		}
	}
}
