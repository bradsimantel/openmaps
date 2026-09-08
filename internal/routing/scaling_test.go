package routing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"math/rand/v2"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestAcceleratedReferenceAgreement(t *testing.T) {
	ctx := context.Background()
	for seed := uint64(1); seed <= 12; seed++ {
		random := rand.New(rand.NewPCG(seed, 17))
		d := Data{Metadata: fixture().Metadata}
		for y := 0; y < 6; y++ {
			for x := 0; x < 6; x++ {
				d.Nodes = append(d.Nodes, Node{ID: int64(y*6 + x + 1), Point: Point{float64(x) * .002, float64(y) * .002}})
			}
		}
		for y := 0; y < 6; y++ {
			for x := 0; x < 6; x++ {
				for _, step := range []int{1, 6} {
					if step == 1 && x == 5 || step == 6 && y == 5 {
						continue
					}
					id := int64(len(d.Segments) + 1)
					s := Segment{ID: fmt.Sprint(id), Way: id, From: int64(y*6 + x + 1), To: int64(y*6 + x + 1 + step), Forward: true, Backward: random.IntN(3) != 0, Snap: true}
					if random.IntN(9) == 0 {
						s.DestinationForward = true
						s.DestinationBackward = s.Backward
					}
					d.Segments = append(d.Segments, s)
				}
			}
		}
		// A three-edge via-way prohibition on the first row, with side entrances.
		d.Bans = []Ban{{Relation: 1, Path: []EdgeRef{{Segment: "1"}, {Segment: "3"}, {Segment: "5"}}}}
		d = uniformCosts(d)
		for i := range d.Costs {
			d.Costs[i].Forward.KPH = float64(5 + random.IntN(90))
			d.Costs[i].Backward.KPH = float64(5 + random.IntN(90))
		}
		s := store(t, d)
		if seed%2 == 0 && (runtime.GOOS == "darwin" || runtime.GOOS == "linux") && (runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64") {
			if err := s.UseMappedQueryData(ctx, t.TempDir()); err != nil {
				t.Fatal(err)
			}
		}
		if seed%3 == 0 && (runtime.GOOS == "darwin" || runtime.GOOS == "linux") {
			_, path, receipt := preparedFixture(t, d)
			s.Close()
			var err error
			s, err = openPreparedArtifact(ctx, path, receipt)
			if err != nil {
				t.Fatal(err)
			}
		}
		t.Cleanup(func() { s.Close() })
		shortcuts := 0
		for n := 0; n < 45; n++ {
			point := func() Point {
				seg := d.Segments[random.IntN(len(d.Segments))]
				a, b := s.point(seg.From), s.point(seg.To)
				f := []float64{0, .25, .7, 1}[random.IntN(4)]
				return Point{a[0] + f*(b[0]-a[0]), a[1] + f*(b[1]-a[1])}
			}
			a, b := Endpoint{Point: point()}, Endpoint{Point: point()}
			var metrics SearchMetrics
			fast, fe := s.RouteEndpoints(WithSearchMetrics(ctx, &metrics), a, b)
			shortcuts += metrics.JunctionShortcuts
			ref, re := s.RouteReferenceEndpoints(ctx, a, b)
			if fmt.Sprint(fe) != fmt.Sprint(re) || math.Abs(fast.Duration-ref.Duration) > 1e-6 || !reflect.DeepEqual(fast.Origin, ref.Origin) || !reflect.DeepEqual(fast.Destination, ref.Destination) {
				t.Fatalf("seed %d request %d: accelerated=%+v %v reference=%+v %v", seed, n, fast, fe, ref, re)
			}
			again, ae := s.RouteEndpoints(ctx, a, b)
			if !reflect.DeepEqual(again, fast) || fmt.Sprint(ae) != fmt.Sprint(fe) {
				t.Fatal("nondeterministic tie")
			}
		}
		if shortcuts == 0 {
			t.Fatalf("seed %d did not exercise junction shortcuts alongside via-way and destination restrictions", seed)
		}
	}
}
func TestForcedChainInteriorAndRing(t *testing.T) {
	d := Data{Metadata: fixture().Metadata}
	for i := 0; i < 80; i++ {
		angle := float64(i) * 2 * math.Pi / 80
		d.Nodes = append(d.Nodes, Node{ID: int64(i + 1), Point: Point{.01 * math.Cos(angle), .01 * math.Sin(angle)}})
	}
	for i := 0; i < 80; i++ {
		d.Segments = append(d.Segments, Segment{ID: fmt.Sprint(i + 1), Way: int64(i + 1), From: int64(i + 1), To: int64((i+1)%80 + 1), Forward: true, Snap: true})
	}
	s := store(t, d)
	for _, i := range []int{0, 1, 20, 79} {
		for _, j := range []int{0, 2, 43, 78} {
			a, b := Endpoint{Point: d.Nodes[i].Point}, Endpoint{Point: d.Nodes[j].Point}
			x, e := s.RouteEndpoints(context.Background(), a, b)
			y, f := s.RouteReferenceEndpoints(context.Background(), a, b)
			if fmt.Sprint(e) != fmt.Sprint(f) || math.Abs(x.Duration-y.Duration) > 1e-6 {
				t.Fatal(i, j, x, y, e, f)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Route(ctx, d.Nodes[0].Point, d.Nodes[0].Point); err != context.Canceled {
		t.Fatal("zero-route cancellation", err)
	}
}

// Cancel at a deterministic context observation after endpoint selection. This
// exercises an in-progress search without a scheduler-dependent timer.
type searchCancellationContext struct {
	context.Context
	checks, cancelAt int
	cancel           context.CancelFunc
}

func (c *searchCancellationContext) Err() error {
	c.checks++
	if c.checks == c.cancelAt {
		c.cancel()
	}
	return c.Context.Err()
}

func TestCancellationDuringSearch(t *testing.T) {
	d := Data{Metadata: fixture().Metadata}
	for i := 0; i <= 4096; i++ {
		d.Nodes = append(d.Nodes, Node{ID: int64(i + 1), Point: Point{float64(i) * .0001, 0}})
		if i > 0 {
			d.Segments = append(d.Segments, Segment{ID: fmt.Sprint(i), Way: int64(i), From: int64(i), To: int64(i + 1), Forward: true, Snap: true})
		}
	}
	s := store(t, d)
	a, b := Endpoint{Point: d.Nodes[0].Point}, Endpoint{Point: d.Nodes[len(d.Nodes)-1].Point}
	selection := &searchCancellationContext{Context: context.Background()}
	for _, endpoint := range []Endpoint{a, b} {
		if _, err := s.endpointSnap(selection, endpoint, "origin"); err != nil {
			t.Fatal(err)
		}
	}
	for _, accelerated := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		probe := &searchCancellationContext{Context: ctx, cancel: cancel, cancelAt: selection.checks + 2}
		result, err := s.routeEndpoints(probe, a, b, false, accelerated)
		cancel()
		if err != context.Canceled || len(result.Geometry) != 0 || probe.checks < probe.cancelAt {
			t.Fatalf("accelerated=%v checks=%d result=%+v error=%v", accelerated, probe.checks, result, err)
		}
	}
	if _, err := s.RouteEndpoints(context.Background(), a, b); err != nil {
		t.Fatalf("canceled request damaged subsequent routing: %v", err)
	}
}

func scanIndex(n int) spatialIndex {
	ids := make([]int32, n)
	for i := range ids {
		ids[i] = int32(i)
	}
	return spatialIndex{ids: ids, nodes: []spatialNode{{bounds: box{-180, -90, 180, 90}, left: -1, end: int32(n)}}}
}
func TestSpatialSelectionMatchesFullScan(t *testing.T) {
	d := fixture()
	d.Guards = []Guard{{Segment: "guard", Way: 99, From: Point{.002, .0003}, To: Point{.005, .0003}}}
	s := store(t, d)
	ref := store(t, d)
	ref.segmentIndex = scanIndex(len(s.segments))
	ref.guardIndex = scanIndex(len(s.guards))
	ref.areaIndex = scanIndex(len(s.access.Areas))
	ref.accessIndex = scanIndex(len(s.access.Ways))
	random := rand.New(rand.NewPCG(9, 3))
	for i := 0; i < 800; i++ {
		p := Point{random.Float64()*.013 - .002, random.Float64()*.009 - .002}
		a, e := s.snap(context.Background(), p, "origin")
		b, f := ref.snap(context.Background(), p, "origin")
		if !reflect.DeepEqual(a, b) || fmt.Sprint(e) != fmt.Sprint(f) {
			t.Fatal(p, a, b, e, f)
		}
	}
	// A long segment crossing the query box must not be dropped by a midpoint index.
	idx := buildSpatial([]box{{-150, 45, -70, 45}, {0, 0, 1, 1}})
	if !reflect.DeepEqual(idx.query(box{-121, 44, -120, 46}), []int{0, 1}) {
		t.Fatal("expected conservative leaf candidates")
	}
}
func persistedFixture() Data {
	d := uniformCosts(fixture())
	d.Metadata.SourceSHA256 = Digest([]byte("fixture"))
	d.Metadata.Release = "fixture"
	d.Metadata.URL = "https://example.org/fixture"
	d.Metadata.Attribution = "fixture"
	for _, seg := range d.Segments {
		d.Sources = append(d.Sources, Source{Kind: "way", ID: seg.Way, Version: 1, Raw: json.RawMessage(`{"tags":{}}`), Decision: "included"})
	}
	return d
}
func writeFixture(t *testing.T, d Data) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "graph.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := WriteGraph(context.Background(), tx, d); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return db
}
func TestChunkStorageValidation(t *testing.T) {
	for _, kind := range []string{"roundtrip", "corrupt topology", "corrupt source", "missing", "layout", "preprocessing", "cost model"} {
		t.Run(kind, func(t *testing.T) {
			db := writeFixture(t, persistedFixture())
			switch kind {
			case "corrupt topology":
				db.Exec("UPDATE routing_chunks SET data=x'00' WHERE kind='nodes'")
			case "corrupt source":
				db.Exec("UPDATE routing_chunks SET data=x'00' WHERE kind='sources'")
			case "missing":
				db.Exec("DELETE FROM routing_chunks WHERE kind='costs'")
			case "layout", "preprocessing", "cost model":
				m, _, _, err := readManifest(context.Background(), db)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "layout" {
					m.Layout = "future"
				} else if kind == "preprocessing" {
					m.Preprocessing = "future"
				} else {
					m.Metadata.CostModel = "future"
				}
				raw, _ := json.Marshal(m)
				db.Exec("UPDATE routing_graph SET data=?,sha256=?", raw, Digest(raw))
			}
			s, _, err := Load(context.Background(), db)
			if kind != "roundtrip" {
				if err == nil {
					t.Fatal("accepted damaged/unsupported graph")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			a := Point{.001, 0}
			b := Point{.008, .004}
			r, e := s.Route(context.Background(), a, b)
			ref, f := store(t, persistedFixture()).Route(context.Background(), a, b)
			if !reflect.DeepEqual(r, ref) || fmt.Sprint(e) != fmt.Sprint(f) {
				t.Fatal("storage changed route")
			}
		})
	}
}

// Retained preprocessing manifests describe their source-era build. Runtime
// regenerates the current overlay without rewriting those authoritative records.
func TestLegacyChunkPreprocessing(t *testing.T) {
	for _, version := range []string{"forced-chain-v1", "independent-junction-v1"} {
		t.Run(version, func(t *testing.T) {
			db := writeFixture(t, persistedFixture())
			m, _, _, err := readManifest(context.Background(), db)
			if err != nil {
				t.Fatal(err)
			}
			m.Preprocessing = version
			raw, _ := json.Marshal(m)
			if _, err = db.Exec("UPDATE routing_graph SET data=?,sha256=?", raw, Digest(raw)); err != nil {
				t.Fatal(err)
			}
			s, summary, err := Load(context.Background(), db)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if summary.Preprocessing != version {
				t.Fatal("legacy summary rewritten")
			}
			fast, e := s.Route(context.Background(), Point{.001, 0}, Point{.008, .004})
			ref, f := s.RouteReferenceEndpoints(context.Background(), Endpoint{Point: Point{.001, 0}}, Endpoint{Point: Point{.008, .004}})
			if errorText(e) != errorText(f) || math.Abs(fast.Duration-ref.Duration) > 1e-6 {
				t.Fatal("legacy source changed", e, f)
			}
		})
	}
}

func TestSpatialAddressSelectionMatchesFullScan(t *testing.T) {
	d, e := associationFixture()
	s := store(t, d)
	reference := store(t, d)
	reference.segmentIndex = scanIndex(len(s.segments))
	reference.guardIndex = scanIndex(len(s.guards))
	reference.areaIndex = scanIndex(len(s.access.Areas))
	reference.accessIndex = scanIndex(len(s.access.Ways))
	rng := rand.New(rand.NewPCG(77, 91))
	for i := 0; i < 300; i++ {
		input := e
		input.Point = Point{e.Point[0] + (rng.Float64()-.5)*.001, e.Point[1] + (rng.Float64()-.5)*.001}
		a, ae := s.endpointSnap(context.Background(), input, "origin")
		b, be := reference.endpointSnap(context.Background(), input, "origin")
		if !reflect.DeepEqual(a, b) || fmt.Sprint(ae) != fmt.Sprint(be) {
			t.Fatal("indexed address differs", input, a, b, ae, be)
		}
	}
}
func TestRejectOrphanRoutingChunks(t *testing.T) {
	db := writeFixture(t, persistedFixture())
	if _, err := db.Exec("DROP TABLE routing_graph"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(context.Background(), db); err == nil {
		t.Fatal("orphan chunks treated as absent graph")
	}
}
