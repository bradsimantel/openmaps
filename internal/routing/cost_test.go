package routing

import (
	"context"
	"math"
	"reflect"
	"testing"
)

func timedFixture() Data {
	d := fixture()
	d.Metadata.CostModel = CostModel
	for _, s := range d.Segments {
		v := 36.0
		if s.ID == "b" {
			v = 5
		}
		d.Costs = append(d.Costs, WayCost{Way: s.Way, Forward: Speed{KPH: v, Notes: []string{"fixture"}}, Backward: Speed{KPH: v, Notes: []string{"fixture"}}})
	}
	return d
}
func TestLongerRouteFasterWithRestrictionState(t *testing.T) {
	ctx := context.Background()
	d := timedFixture()
	a, b := Endpoint{Point: Point{.001, 0}}, Endpoint{Point: Point{.008, 0}}
	s := store(t, d)
	fast, e := s.RouteEndpoints(ctx, a, b)
	if e != nil {
		t.Fatal(e)
	}
	short, e := s.RouteDistanceEndpoints(ctx, a, b)
	if e != nil || fast.Distance <= short.Distance || fast.Duration >= short.Duration || !reflect.DeepEqual(fast.Segments, []string{"a", "c", "d", "e"}) || !reflect.DeepEqual(short.Segments, []string{"a", "b"}) {
		t.Fatal(fast, short, e)
	}
	if !reflect.DeepEqual(fast.Origin, short.Origin) || !reflect.DeepEqual(fast.Destination, short.Destination) {
		t.Fatal("cost-dependent endpoint")
	}
	if math.Abs(fast.Duration-fast.Distance/10) > 1e-9 {
		t.Fatal("unexpected hidden delay or gap", fast)
	}
	// A very fast route is still illegal with either a multi-edge prohibited
	// maneuver or a destination-only through zone; both must take slow public b.
	for _, kind := range []string{"via turn", "destination"} {
		t.Run(kind, func(t *testing.T) {
			x := timedFixture()
			if kind == "via turn" {
				x.Bans = []Ban{{Relation: 10, Path: []EdgeRef{{Segment: "a"}, {Segment: "c"}, {Segment: "d"}, {Segment: "e"}}}}
			} else {
				x.Segments[3].DestinationForward = true
				x.Segments[3].DestinationBackward = true
			}
			r, e := store(t, x).RouteEndpoints(ctx, a, b)
			if e != nil || !reflect.DeepEqual(r.Segments, short.Segments) {
				t.Fatal(r, e)
			}
		})
	}
}
func TestDurationPartialDirectionalAndZero(t *testing.T) {
	d := timedFixture()
	d.Segments = d.Segments[:1]
	d.Costs = d.Costs[:1]
	d.Costs[0].Backward.KPH = 18
	s := store(t, d)
	a, b := Point{.001, .0001}, Point{.003, .0001}
	for _, pair := range [][2]Point{{a, b}, {b, a}, {a, a}} {
		r, e := s.Route(context.Background(), pair[0], pair[1])
		if e != nil {
			t.Fatal(e)
		}
		v := 10.0
		if pair[0] == b {
			v = 5
		}
		if math.Abs(r.Duration-r.Distance/v) > 1e-9 {
			t.Fatal(r)
		}
		if r.Origin.Distance < 11 {
			t.Fatal("fixture must have excluded off-road gaps")
		}
	}
}
func TestDeterministicTimeTie(t *testing.T) {
	d := timedFixture()
	d.Nodes = []Node{{ID: 1, Point: Point{0, 0}}, {ID: 2, Point: Point{.001, .001}}, {ID: 3, Point: Point{.002, 0}}, {ID: 4, Point: Point{.001, -.001}}}
	d.Segments = []Segment{{ID: "a", Way: 1, From: 1, To: 2, Forward: true, Snap: true}, {ID: "b", Way: 2, From: 2, To: 3, Forward: true, Snap: true}, {ID: "c", Way: 3, From: 1, To: 4, Forward: true, Snap: true}, {ID: "d", Way: 4, From: 4, To: 3, Forward: true, Snap: true}}
	d.Costs = d.Costs[:4]
	for i := range d.Costs {
		d.Costs[i].Forward.KPH = 36
	}
	for i := 0; i < 4; i++ {
		r, e := store(t, d).Route(context.Background(), Point{0, 0}, Point{.002, 0})
		if e != nil || !reflect.DeepEqual(r.Segments, []string{"a", "b"}) {
			t.Fatal(r, e)
		}
		d.Segments = append(d.Segments[1:], d.Segments[0])
	}
}
func TestCostVersionAndInvalidSpeeds(t *testing.T) {
	for _, edit := range []func(*Data){func(d *Data) { d.Metadata.CostModel = "future" }, func(d *Data) { d.Costs = nil }, func(d *Data) { d.Costs[0].Forward.KPH = 0 }, func(d *Data) { d.Costs[0].Forward.KPH = math.NaN() }, func(d *Data) { d.Costs[0].Forward.LimitKPH = 10 }, func(d *Data) { d.Costs = append(d.Costs, d.Costs[0]) }} {
		d := timedFixture()
		edit(&d)
		if _, e := New(d); e == nil {
			t.Fatal("invalid costs accepted")
		}
	}
	for _, v := range []int{1, 2, 3} {
		d := fixture()
		d.Metadata.Version = v
		d.Metadata.Profile = map[int]string{1: "driving-distance-v1", 2: "driving-distance-v2", 3: "driving-distance-v3"}[v]
		s, e := New(d)
		if e != nil || s.HasDuration() {
			t.Fatal(v, e)
		}
		r, e := s.Route(context.Background(), Point{0, 0}, Point{.008, 0})
		if e != nil || r.Duration != 0 || !reflect.DeepEqual(r.Segments, []string{"a", "b"}) {
			t.Fatal(r, e)
		}
	}
}
