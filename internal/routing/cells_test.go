package routing

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"runtime"
	"testing"
)

func cellFixture() Data {
	d := Data{Metadata: fixture().Metadata}
	const width, height = 20, 5
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			d.Nodes = append(d.Nodes, Node{ID: int64(y*width + x + 1), Point: Point{float64(x) * .002, float64(y) * .002}})
		}
	}
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			for _, step := range []int{1, width} {
				if step == 1 && x == width-1 || step == width && y == height-1 {
					continue
				}
				from, to := int64(y*width+x+1), int64(y*width+x+step+1)
				id := len(d.Segments) + 1
				d.Segments = append(d.Segments, Segment{ID: fmt.Sprintf("%04d", id), Way: int64(id), From: from, To: to, Forward: true, Backward: true, Snap: true})
			}
		}
	}
	// Via-way history, destination phases, and directional costs share this graph.
	d.Bans = []Ban{{Relation: 1, Path: []EdgeRef{{Segment: "0001"}, {Segment: "0003"}, {Segment: "0005"}}}}
	d.Segments[40].DestinationForward = true
	d.Segments[40].DestinationBackward = true
	d = uniformCosts(d)
	for i := range d.Costs {
		if i%7 == 0 {
			d.Costs[i].Backward.KPH = 17
		}
	}
	return d
}

func TestRecursiveCellPathsAndBoundaries(t *testing.T) {
	s := store(t, cellFixture())
	depth := 0
	for _, entry := range s.cellEntries {
		incoming := int(entry.edge)
		if s.restrictedNodes[s.edges[incoming].to] {
			t.Fatal("restriction node inside cell")
		}
		for _, transfer := range s.cellTransfers[entry.offset : entry.offset+entry.count] {
			parts := []cellPath{}
			for id := transfer.path; id >= 0; id = s.cellPaths[id].parent {
				p := s.cellPaths[id]
				if p.parent >= id {
					t.Fatal("cyclic recursive path")
				}
				parts = append(parts, p)
			}
			depth = max(depth, len(parts))
			prev := incoming
			cost := 0.0
			for i := len(parts) - 1; i >= 0; i-- {
				p := parts[i]
				for id := p.first; ; id = s.continuation[id] {
					e := s.edges[id]
					if e.from != s.edges[prev].to || e.segment == s.edges[prev].segment {
						t.Fatal("broken source adjacency/reversal")
					}
					if s.zones[e.segment] != 0 || s.restrictedNodes[e.from] {
						t.Fatal("transfer crossed protected state boundary")
					}
					for history := range s.trie {
						next, ok := s.advance(history, int(id))
						if !ok || next != 0 {
							t.Fatal("transfer does not safely reset history")
						}
					}
					if !s.cellBounds[entry.cell].contains(s.point(e.to)) {
						t.Fatal("endpoint escape bounds incomplete")
					}
					cost += e.length * e.seconds / e.length
					prev = int(id)
					if id == p.last {
						break
					}
				}
			}
			if prev != int(transfer.last) || math.Abs(cost-transfer.cost) > math.Max(1e-6, math.Abs(cost)*1e-10) {
				t.Fatal("transfer cost/path mismatch")
			}
		}
	}
	if depth < 3 {
		t.Fatal("no recursive composition", depth)
	}
}

func TestCellEndpointsTiesAndCancellation(t *testing.T) {
	d := cellFixture()
	s := store(t, d)
	if (runtime.GOOS == "darwin" || runtime.GOOS == "linux") && (runtime.GOARCH == "arm64" || runtime.GOARCH == "amd64") {
		if err := s.UseMappedQueryData(context.Background(), t.TempDir()); err != nil {
			t.Fatal(err)
		}
	}
	defer s.Close()
	points := []Point{}
	for i := 0; i < len(d.Segments); i += 13 {
		e := d.Segments[i]
		a, b := s.point(e.From), s.point(e.To)
		for _, fraction := range []float64{0, .37, 1} {
			points = append(points, Point{a[0] + fraction*(b[0]-a[0]), a[1] + fraction*(b[1]-a[1])})
		}
	}
	used := 0
	for i, a := range points {
		for j, b := range points {
			if (i+j)%7 != 0 {
				continue
			}
			var m SearchMetrics
			fast, e := s.RouteEndpoints(WithSearchMetrics(context.Background(), &m), Endpoint{Point: a}, Endpoint{Point: b})
			ref, f := s.RouteReferenceEndpoints(context.Background(), Endpoint{Point: a}, Endpoint{Point: b})
			if errorText(e) != errorText(f) || math.Abs(fast.Duration-ref.Duration) > math.Max(1e-6, math.Abs(ref.Duration)*1e-10) || !reflect.DeepEqual(fast.Origin, ref.Origin) || !reflect.DeepEqual(fast.Destination, ref.Destination) {
				t.Fatal("cell endpoint/reference mismatch", i, j, e, f)
			}
			again, g := s.Route(context.Background(), a, b)
			if errorText(e) != errorText(g) || !reflect.DeepEqual(fast, again) {
				t.Fatal("unstable tie")
			}
			used += m.CellShortcuts
		}
	}
	if used == 0 {
		t.Fatal("no cell queries exercised")
	}
	var m SearchMetrics
	ctx := &cancelAfterCell{Context: context.Background(), metrics: &m}
	result, err := s.RouteEndpoints(WithSearchMetrics(ctx, &m), Endpoint{Point: points[0]}, Endpoint{Point: points[len(points)-1]})
	if err != context.Canceled || len(result.Geometry) > 0 || m.CellShortcuts == 0 {
		t.Fatal("cell cancellation not observed", err, m)
	}
	if _, err = s.Route(context.Background(), points[0], points[len(points)-1]); err != nil {
		t.Fatal("cancellation damaged store", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := newStoreContext(canceled, d, false); err != context.Canceled {
		t.Fatal("construction ignored cancellation", err)
	}
}

type cancelAfterCell struct {
	context.Context
	metrics *SearchMetrics
}

func (c *cancelAfterCell) Err() error {
	if c.metrics.CellShortcuts > 0 {
		return context.Canceled
	}
	return nil
}

func TestCellTerminalApproachesAndPartialTargets(t *testing.T) {
	d := Data{Metadata: fixture().Metadata}
	for i := 0; i < 70; i++ {
		main, tooth := int64(i*2+1), int64(i*2+2)
		d.Nodes = append(d.Nodes, Node{ID: main, Point: Point{float64(i) * .002, 0}}, Node{ID: tooth, Point: Point{float64(i) * .002, .002}})
		d.Segments = append(d.Segments, Segment{ID: fmt.Sprintf("tooth-%03d", i), Way: tooth, From: main, To: tooth, Forward: true, Backward: true, Snap: true})
		if i > 0 {
			d.Segments = append(d.Segments, Segment{ID: fmt.Sprintf("main-%03d", i), Way: main, From: main - 2, To: main, Forward: true, Backward: true, Snap: true})
		}
	}
	s := store(t, d)
	terminals := 0
	for _, e := range s.edges {
		if e.cellEntry == terminalCellEntry {
			terminals++
		}
	}
	if terminals != 70 {
		t.Fatal("terminal approaches missing", terminals)
	}
	for _, i := range []int{0, 1, 16, 31, 32, 50, 69} {
		for _, j := range []int{0, 15, 32, 50, 69} {
			for _, fraction := range []float64{0, .37, 1} {
				a, b := Point{float64(i) * .002, .002}, Point{float64(j) * .002, .002 * fraction}
				fast, e := s.Route(context.Background(), a, b)
				ref, f := s.RouteReferenceEndpoints(context.Background(), Endpoint{Point: a}, Endpoint{Point: b})
				if errorText(e) != errorText(f) || math.Abs(fast.Duration-ref.Duration) > 1e-6 || !reflect.DeepEqual(fast.Segments, ref.Segments) {
					t.Fatal("terminal/partial endpoint lost", i, j, fraction, e, f)
				}
			}
		}
	}
}

func TestCellOverlayLegacyDistanceModels(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		d := cellFixture()
		d.Metadata.Version = version
		d.Metadata.Profile = fmt.Sprintf("driving-distance-v%d", version)
		d.Metadata.CostModel = ""
		d.Costs = nil
		s := store(t, d)
		var metrics SearchMetrics
		a, b := Point{0, 0}, Point{.038, .008}
		fast, e := s.RouteEndpoints(WithSearchMetrics(context.Background(), &metrics), Endpoint{Point: a}, Endpoint{Point: b})
		ref, f := s.RouteReferenceEndpoints(context.Background(), Endpoint{Point: a}, Endpoint{Point: b})
		if errorText(e) != errorText(f) || math.Abs(fast.Distance-ref.Distance) > 1e-6 || fast.Duration != 0 || metrics.CellShortcuts == 0 {
			t.Fatal("legacy objective changed", version, e, f, metrics)
		}
	}
}
