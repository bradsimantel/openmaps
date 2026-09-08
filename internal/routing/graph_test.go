package routing

import (
	"context"
	"errors"
	"math"
	"testing"
)

func fixture() Data {
	return Data{Metadata: Metadata{Version: GraphVersion, Profile: Profile, EndpointBounds: [4]float64{-1, -1, 1, 1}}, Nodes: []Node{{ID: 1, Point: Point{0, 0}}, {ID: 2, Point: Point{.004, 0}}, {ID: 3, Point: Point{.008, 0}}, {ID: 4, Point: Point{.004, .004}}, {ID: 5, Point: Point{.008, .004}}}, Segments: []Segment{{ID: "a", Way: 1, From: 1, To: 2, Forward: true, Backward: true, Snap: true}, {ID: "b", Way: 2, From: 2, To: 3, Forward: true, Backward: true, Snap: true}, {ID: "c", Way: 3, From: 2, To: 4, Forward: true, Backward: true, Snap: true}, {ID: "d", Way: 4, From: 4, To: 5, Forward: true, Backward: true, Snap: true}, {ID: "e", Way: 5, From: 5, To: 3, Forward: true, Backward: true, Snap: true}}}
}
func uniformCosts(d Data) Data {
	if d.Metadata.Version == GraphVersion && d.Metadata.CostModel == "" {
		d.Metadata.CostModel = CostModel
		seen := map[int64]bool{}
		for _, seg := range d.Segments {
			if !seen[seg.Way] {
				d.Costs = append(d.Costs, WayCost{Way: seg.Way, Forward: Speed{KPH: 36, Notes: []string{"fixture"}}, Backward: Speed{KPH: 36, Notes: []string{"fixture"}}})
				seen[seg.Way] = true
			}
		}
	}
	return d
}
func store(t *testing.T, d Data) *Store {
	t.Helper()
	s, e := New(uniformCosts(d))
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func outcome(t *testing.T, e error, want string) {
	t.Helper()
	var v *Error
	if !errors.As(e, &v) || v.Outcome != want {
		t.Fatalf("want %s, got %v", want, e)
	}
}
func TestOneWayAndPartialSnapping(t *testing.T) {
	d := fixture()
	d.Segments = d.Segments[:1]
	d.Segments[0].Backward = false
	s := store(t, d)
	a, b := Point{.001, .0001}, Point{.003, 0}
	r, e := s.Route(context.Background(), a, b)
	if e != nil {
		t.Fatal(e)
	}
	if math.Abs(r.Distance-Distance(Point{.001, 0}, b)) > .001 || math.Abs(r.Origin.Distance-11.1195) > .001 || r.Geometry[0] != (Point{.001, 0}) {
		t.Fatalf("unexpected partial snap: %+v", r)
	}
	_, e = s.Route(context.Background(), b, a)
	outcome(t, e, "unreachable")
}
func TestProhibitedTurnAndViaWayHistory(t *testing.T) {
	d := fixture()
	d.Bans = []Ban{{Relation: 9, Path: []EdgeRef{{Segment: "a"}, {Segment: "b"}}}}
	s := store(t, d)
	r, e := s.Route(context.Background(), Point{0, 0}, Point{.008, 0})
	if e != nil || len(r.Segments) != 4 || r.Segments[1] != "c" {
		t.Fatalf("illegal turn or no detour: %+v %v", r, e)
	}
	// A restriction spans multiple segments; taking a side entrance does not trigger it.
	d = fixture()
	d.Bans = []Ban{{Relation: 10, Path: []EdgeRef{{Segment: "a"}, {Segment: "c"}, {Segment: "d"}, {Segment: "e"}}}, {Relation: 11, Path: []EdgeRef{{Segment: "a"}, {Segment: "b"}}}}
	s = store(t, d)
	_, e = s.Route(context.Background(), Point{0, 0}, Point{.008, 0})
	outcome(t, e, "unreachable")
	r, e = s.Route(context.Background(), Point{.004, .004}, Point{.008, 0})
	if e != nil || r.Distance < 800 {
		t.Fatalf("via history applied without approach: %+v %v", r, e)
	}
}
func TestDisconnectedAndCrossingWithoutJunction(t *testing.T) {
	d := fixture()
	d.Nodes = append(d.Nodes, Node{ID: 6, Point: Point{.002, -.003}}, Node{ID: 7, Point: Point{.002, .003}})
	d.Segments = []Segment{d.Segments[0], {ID: "crossing", Way: 8, From: 6, To: 7, Forward: true, Backward: true, Snap: true}}
	s := store(t, d)
	_, e := s.Route(context.Background(), Point{0, 0}, Point{.002, .003})
	outcome(t, e, "unreachable")
}
func TestSnapLimitsCoverageAndMotorway(t *testing.T) {
	d := fixture()
	s := store(t, d)
	_, e := s.Route(context.Background(), Point{.002, -.0009}, Point{.008, 0})
	outcome(t, e, "unsnappable")
	if _, e = s.Route(context.Background(), Point{.002, -.00089}, Point{.008, 0}); e != nil {
		t.Fatal(e)
	}
	_, e = s.Route(context.Background(), Point{2, 0}, Point{0, 0})
	outcome(t, e, "outside_coverage")
	_, e = s.Route(context.Background(), Point{math.NaN(), 0}, Point{0, 0})
	outcome(t, e, "invalid_coordinate")
	d.Segments = d.Segments[:1]
	d.Segments[0].Snap = false
	_, e = store(t, d).Route(context.Background(), Point{0, 0}, Point{.004, 0})
	outcome(t, e, "unsnappable")
}
func TestJunctionEndpointsZeroDistanceAndCancellation(t *testing.T) {
	s := store(t, fixture())
	r, e := s.Route(context.Background(), Point{.004, 0}, Point{.004, .004})
	if e != nil || len(r.Segments) != 1 || r.Segments[0] != "c" {
		t.Fatalf("junction should permit any initial heading: %+v %v", r, e)
	}
	r, e = s.Route(context.Background(), Point{.004, 0}, Point{.004, 0})
	if e != nil || r.Distance != 0 || len(r.Geometry) != 2 {
		t.Fatal(r, e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, e = s.Route(ctx, Point{0, 0}, Point{.004, 0})
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func TestCorruptGraphReferences(t *testing.T) {
	d := fixture()
	d.Segments[0].From = 999
	if _, e := New(uniformCosts(d)); e == nil {
		t.Fatal("accepted dangling node")
	}
	d = fixture()
	d.Bans = []Ban{{Path: []EdgeRef{{Segment: "a"}, {Segment: "e"}}}}
	if _, e := New(uniformCosts(d)); e == nil {
		t.Fatal("accepted disconnected restriction")
	}
}

func TestDestinationAccessPrefixesAndSuffixes(t *testing.T) {
	d := fixture()
	d.Segments[1].DestinationForward = true
	d.Segments[1].DestinationBackward = true
	s := store(t, d)
	ctx := context.Background()
	r, e := s.Route(ctx, Point{0, 0}, Point{.008, 0})
	// Destination at the shared public boundary need not enter the zone; through
	// traffic between public interiors must use the ordinary-street detour.
	if e != nil {
		t.Fatal(e)
	}
	_ = r
	r, e = s.Route(ctx, Point{.001, 0}, Point{.008, .001})
	if e != nil {
		t.Fatal(e)
	}
	for _, id := range r.Segments {
		if id == "b" {
			t.Fatal("destination shortcut", r)
		}
	}
	for _, pair := range [][2]Point{{{.001, 0}, {.006, 0}}, {{.006, 0}, {.001, 0}}} {
		r, e = s.Route(ctx, pair[0], pair[1])
		if e != nil || r.Distance > 700 {
			t.Fatal("endpoint zone inaccessible", r, e)
		}
	}
	_, e = s.Route(ctx, Point{.001, 0}, Point{.006, .00002})
	outcome(t, e, "unsnappable")
	// A destination zone cannot be entered, exited to public roads, then reentered
	// to bypass a prohibition inside the zone.
	d.Bans = []Ban{{Relation: 1, Path: []EdgeRef{{Segment: "a"}, {Segment: "b"}}}}
	r, e = store(t, d).Route(ctx, Point{.001, 0}, Point{.006, 0})
	if e != nil || r.Distance < 1500 {
		t.Fatal("destination access bypassed turn", r, e)
	}
}

func TestCompetingSnapCandidates(t *testing.T) {
	d := Data{Metadata: Metadata{Version: GraphVersion, Profile: Profile, EndpointBounds: [4]float64{-1, -1, 1, 1}}, Nodes: []Node{{ID: 1, Point: Point{0, 0}}, {ID: 2, Point: Point{0, .001}}, {ID: 3, Point: Point{.001, 0}}}, Segments: []Segment{
		{ID: "spur", Way: 1, From: 1, To: 2, Forward: true, Backward: true, Snap: true, Service: true},
		{ID: "street", Way: 2, From: 1, To: 3, Forward: true, Backward: true, Snap: true},
	}}
	p := Point{.00001, .00005}
	ctx := context.Background()
	r, e := store(t, d).Route(ctx, p, Point{.0008, 0})
	if e != nil || r.Origin.Segment != "street" || r.Origin.SelectionReason == "" || r.Origin.Distance <= r.Origin.NearestDistance {
		t.Fatal(r, e)
	}
	for _, change := range []func(*Data){
		func(d *Data) { d.Segments[1].Backward = false }, // divided/one-way road
		func(d *Data) { d.Segments[1].Elevated = true },
		func(d *Data) {
			d.Bans = []Ban{{Relation: 1, Path: []EdgeRef{{Segment: "spur", Reverse: true}, {Segment: "street"}}}}
		},
		func(d *Data) { d.Nodes = append(d.Nodes, Node{ID: 4, Point: Point{0, 0}}); d.Segments[1].From = 4 }, // geometric crossing, no shared node
	} {
		copy := d
		copy.Segments = append([]Segment(nil), d.Segments...)
		copy.Nodes = append([]Node(nil), d.Nodes...)
		change(&copy)
		a, e := store(t, copy).snap(ctx, p, "origin")
		if e != nil || a.Segment != "spur" || a.SelectionReason != "" {
			t.Fatal("unsafe alternative", a, e)
		}
	}
	a, e := store(t, d).snap(ctx, Point{.00001, .0004}, "origin")
	if e != nil || a.Segment != "spur" {
		t.Fatal("large jump", a, e)
	}
	// Restricted motor road closer than the eligible road cannot be skipped.
	d.Guards = []Guard{{Segment: "private", Way: 3, From: Point{-.001, .00002}, To: Point{.001, .00002}}}
	_, e = store(t, d).snap(ctx, Point{.0004, .00003}, "origin")
	outcome(t, e, "unsnappable")
}
func TestGraphVersionCompatibility(t *testing.T) {
	d := fixture()
	d.Metadata.Version = 1
	d.Metadata.Profile = "driving-distance-v1"
	if _, e := New(uniformCosts(d)); e != nil {
		t.Fatal("legacy unreadable", e)
	}
	for _, v := range []int{0, 3, 999} {
		d.Metadata.Version = v
		if _, e := New(uniformCosts(d)); e == nil {
			t.Fatal("unsupported version loaded", v)
		}
	}
	d.Metadata.Version = 2
	if _, e := New(uniformCosts(d)); e == nil {
		t.Fatal("profile/version mismatch loaded")
	}
}

func TestDestinationBoundaryIsNotAuthorization(t *testing.T) {
	d := fixture()
	d.Segments[1].DestinationForward = true
	d.Segments[1].DestinationBackward = true
	r, e := store(t, d).Route(context.Background(), Point{.004, 0}, Point{.008, 0})
	if e != nil || r.Distance < 1300 {
		t.Fatal("public boundary endpoints authorized through zone", r, e)
	}
}
func TestFartherSnapCannotCrossThirdRoad(t *testing.T) {
	d := fixture()
	d.Nodes = []Node{{ID: 1, Point: Point{0, 0}}, {ID: 2, Point: Point{0, .001}}, {ID: 3, Point: Point{.001, 0}}, {ID: 4, Point: Point{-.001, .000025}}, {ID: 5, Point: Point{.001, .000025}}}
	d.Segments = []Segment{{ID: "spur", Way: 1, From: 1, To: 2, Forward: true, Backward: true, Snap: true, Service: true}, {ID: "street", Way: 2, From: 1, To: 3, Forward: true, Backward: true, Snap: true}, {ID: "divided", Way: 3, From: 4, To: 5, Forward: true, Snap: true}}
	a, e := store(t, d).snap(context.Background(), Point{.00001, .00005}, "origin")
	if e != nil || a.Segment != "spur" {
		t.Fatal("jumped divided road", a, e)
	}
}
