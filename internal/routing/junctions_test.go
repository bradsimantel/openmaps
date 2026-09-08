package routing

import (
	"context"
	"math"
	"reflect"
	"testing"
)

func TestJunctionShortcutsAndPartialEndpoints(t *testing.T) {
	d := fixture()
	// Add an ordinary third branch at node 2; source topology stays explicit.
	d.Nodes = append(d.Nodes, Node{ID: 99, Point: Point{.004, -.003}})
	d.Segments = append(d.Segments, Segment{ID: "branch", Way: 99, From: 2, To: 99, Forward: true, Backward: true, Snap: true})
	d = uniformCosts(d)
	s := store(t, d)
	selected := 0
	for _, v := range s.junctions {
		if v == 2 {
			selected++
		}
	}
	if selected == 0 {
		t.Fatal("fixture contracted no junctions")
	}
	used := 0
	for _, a := range s.segments {
		for _, b := range s.segments {
			ap, bp := s.point(a.From), s.point(a.To)
			aPoint := Point{ap[0]*.3 + bp[0]*.7, ap[1]*.3 + bp[1]*.7}
			ap, bp = s.point(b.From), s.point(b.To)
			bPoint := Point{ap[0]*.8 + bp[0]*.2, ap[1]*.8 + bp[1]*.2}
			var m SearchMetrics
			x, e := s.routeMeasured(context.Background(), Endpoint{Point: aPoint}, Endpoint{Point: bPoint}, false, true, &m)
			y, f := s.RouteReferenceEndpoints(context.Background(), Endpoint{Point: aPoint}, Endpoint{Point: bPoint})
			if errorText(e) != errorText(f) || math.Abs(x.Duration-y.Duration) > 1e-6 || !reflect.DeepEqual(x.Origin, y.Origin) || !reflect.DeepEqual(x.Destination, y.Destination) {
				t.Fatalf("partial endpoint disagreement: %v %v", e, f)
			}
			used += m.JunctionShortcuts
		}
	}
	if used == 0 {
		t.Fatal("no junction shortcuts exercised")
	}
	// Every node of a via-way ban is a core boundary, including its endpoints.
	// Existing general randomized fixtures exercise multi-edge via history and zones;
	// validate this boundary using the fixture's known contiguous a,b chain.
	d.Bans = []Ban{{Relation: 1, Path: []EdgeRef{{Segment: "a"}, {Segment: "c"}, {Segment: "d"}, {Segment: "e"}}}}
	s = store(t, d)
	for node := range s.restrictedNodes {
		if s.junctions[s.nodeIndex[node]] == 2 {
			t.Fatal("contracted restriction boundary")
		}
	}
}

func TestLargeComponentToDisconnectedIsRejected(t *testing.T) {
	d := fixture()
	d.Nodes = append(d.Nodes, Node{ID: 98, Point: Point{.03, .03}}, Node{ID: 99, Point: Point{.031, .03}})
	d.Segments = append(d.Segments, Segment{ID: "island", Way: 98, From: 98, To: 99, Forward: true, Backward: true, Snap: true})
	s := store(t, d)
	var m SearchMetrics
	a, b := Endpoint{Point: d.Nodes[0].Point}, Endpoint{Point: Point{.0305, .03}}
	fast, e := s.routeMeasured(context.Background(), a, b, false, true, &m)
	ref, f := s.RouteReferenceEndpoints(context.Background(), a, b)
	if errorText(e) != errorText(f) || e == nil || m.Expanded != 0 || !reflect.DeepEqual(fast.Origin, ref.Origin) || !reflect.DeepEqual(fast.Destination, ref.Destination) {
		t.Fatal("connectivity rejection changed endpoints/outcome", e, f, m)
	}
}

func errorText(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
