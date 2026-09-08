package routing

import (
	"context"
	"errors"
	"testing"
)

func endpointFixture() Data {
	return Data{Metadata: Metadata{Version: GraphVersion, Profile: Profile, EndpointBounds: [4]float64{-1, -1, 1, 1}}, Nodes: []Node{{ID: 1, Point: Point{0, 0}}, {ID: 2, Point: Point{.002, 0}}, {ID: 3, Point: Point{.002, .001}}, {ID: 4, Point: Point{.004, .001}}}, Segments: []Segment{{ID: "public", Way: 1, From: 1, To: 2, Forward: true, Backward: true, Snap: true}, {ID: "zone", Way: 2, From: 2, To: 3, Forward: true, Backward: true, Snap: true, DestinationForward: true, DestinationBackward: true}, {ID: "exit", Way: 3, From: 3, To: 4, Forward: true, Backward: true, Snap: true}}}
}
func addr(p Point, ways ...int64) Endpoint {
	e := Endpoint{Point: p, Address: true, StreetWays: map[int64]bool{}, Areas: map[int64]bool{}}
	for _, w := range ways {
		e.StreetWays[w] = true
	}
	return e
}
func TestAddressDestinationEvidence(t *testing.T) {
	s := store(t, endpointFixture())
	ctx := context.Background()
	p := Point{.0021, .0005}
	for _, tc := range []struct {
		name    string
		e       Endpoint
		allowed bool
	}{{"street evidence", addr(p, 2), true}, {"nearby different address street", addr(p, 1), false}, {"coordinate remains strict", Endpoint{Point: p}, false}, {"beyond address destination bound", addr(Point{.0024, .0005}, 2), false}} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := s.RouteEndpoints(ctx, Endpoint{Point: Point{.001, 0}}, tc.e)
			if (err == nil) != tc.allowed {
				t.Fatal(r, err)
			}
			if tc.allowed && (!r.Destination.DestinationAccess || r.Destination.Method != "destination_address_street" || r.Destination.Requested != p || r.Destination.Distance < 10) {
				t.Fatal(r)
			}
		})
	}
	// Public endpoints cannot use the zone as a shortcut; association does not
	// alter the restriction phase state machine.
	_, err := s.RouteEndpoints(ctx, addr(Point{.001, .0001}, 1), addr(Point{.003, .0011}, 3))
	var re *Error
	if !errors.As(err, &re) || re.Outcome != "unreachable" {
		t.Fatal(err)
	}
	r, err := s.RouteEndpoints(ctx, addr(p, 2), addr(Point{.001, .0001}, 1))
	if err != nil || !r.Origin.DestinationAccess {
		t.Fatal(r, err)
	}
	// Full turn history also applies to an associated destination.
	d := endpointFixture()
	d.Bans = []Ban{{Relation: 4, Path: []EdgeRef{{Segment: "public"}, {Segment: "zone"}}}}
	_, err = store(t, d).RouteEndpoints(ctx, Endpoint{Point: Point{.001, 0}}, addr(p, 2))
	if !errors.As(err, &re) || re.Outcome != "unreachable" {
		t.Fatal(err)
	}
}
func TestAddressFallbackGuardsAndCompetition(t *testing.T) {
	ctx := context.Background()
	d := endpointFixture()
	d.Segments = d.Segments[:1]
	p := Point{.001, .0001}
	s := store(t, d)
	c, err := s.endpointSnap(ctx, addr(p, 1), "origin")
	if err != nil || c.Method != "address_street" || c.Distance < 11 || c.Uncertainty == "" {
		t.Fatal(c, err)
	}
	for _, label := range []string{"private", "customers", "delivery", "permit", "barrier"} {
		t.Run(label, func(t *testing.T) {
			x := d
			x.Guards = []Guard{{Segment: label, Way: 9, From: Point{0, .00008}, To: Point{.002, .00008}}}
			if _, err := store(t, x).endpointSnap(ctx, addr(p, 1), "origin"); err == nil {
				t.Fatal("bypassed guard")
			}
		})
	}
	x := d
	x.Nodes = append(append([]Node{}, d.Nodes...), Node{ID: 5, Point: Point{0, .0002}}, Node{ID: 6, Point: Point{.002, .0002}})
	x.Segments = append(append([]Segment{}, d.Segments...), Segment{ID: "competing", Way: 5, From: 5, To: 6, Forward: true, Backward: true, Snap: true})
	if _, err := store(t, x).endpointSnap(ctx, addr(p, 1), "origin"); err == nil {
		t.Fatal("arbitrary equal road chosen")
	}
	if _, err := s.endpointSnap(ctx, addr(Point{.001, .00046}, 1), "origin"); err == nil {
		t.Fatal("unbounded fallback")
	}
	// A disconnected nearest road must remain selected; no route-driven retry.
	x = d
	x.Nodes = append(append([]Node{}, d.Nodes...), Node{ID: 5, Point: Point{.003, 0}}, Node{ID: 6, Point: Point{.004, 0}})
	x.Segments = append(append([]Segment{}, d.Segments...), Segment{ID: "island", Way: 5, From: 5, To: 6, Forward: true, Backward: true, Snap: true})
	r, err := store(t, x).RouteEndpoints(ctx, addr(p, 1), addr(Point{.0035, .0001}, 5))
	var re *Error
	if !errors.As(err, &re) || re.Outcome != "unreachable" || r.Destination.Segment != "island" {
		t.Fatal(r, err)
	}
}
func associationFixture() (Data, Endpoint) {
	d := endpointFixture()
	d.Segments = d.Segments[:1]
	ring := []Point{{.0018, .0006}, {.0022, .0006}, {.0022, .0012}, {.0018, .0012}, {.0018, .0006}}
	d.Access = AccessData{Areas: []AccessArea{{Way: 10, Nodes: []int64{10, 11, 12, 13, 10}, Geometry: ring}}, Ways: []AccessWay{{Way: 9, Driveway: true, Nodes: []int64{2, 3}, Geometry: []Point{{.002, 0}, {.002, .001}}}}}
	d.Guards = []Guard{{Segment: "private-driveway", Way: 9, From: Point{.002, 0}, To: Point{.002, .001}}}
	e := addr(Point{.00205, .0008}, 1)
	e.Areas[10] = true
	return d, e
}
func TestMappedDrivewayBoundary(t *testing.T) {
	d, e := associationFixture()
	s := store(t, d)
	r, err := s.RouteEndpoints(context.Background(), Endpoint{Point: Point{.001, 0}}, e)
	if err != nil || r.Destination.Method != "mapped_driveway_public_junction" || r.Destination.Point != (Point{.002, 0}) || r.Destination.DestinationAccess || len(r.Destination.Evidence) != 3 {
		t.Fatal(r, err)
	}
	for _, id := range r.Segments {
		if id != "public" {
			t.Fatal("private travel", id)
		}
	}
	// No building association, wrong addressed street, excessive gap: fail, not
	// travel on the private way or jump over its exclusion.
	for _, edit := range []func(*Endpoint){func(e *Endpoint) { e.Areas = nil }, func(e *Endpoint) { e.StreetWays = nil }, func(e *Endpoint) { e.Point = Point{.00205, .0011} }} {
		x := e
		edit(&x)
		if _, err := s.endpointSnap(context.Background(), x, "destination"); err == nil {
			t.Fatal("unsupported association", x)
		}
	}
	// Source coordinate equality alone does not join a driveway to a public node.
	d.Access.Ways[0].Nodes[0] = 99
	if _, err := store(t, d).endpointSnap(context.Background(), e, "destination"); err == nil {
		t.Fatal("invented junction")
	}
}
func TestMappedParkingEntrance(t *testing.T) {
	d, e := associationFixture()
	d.Guards = nil
	d.Access.Ways = nil
	d.Access.Areas[0].Parking = true
	d.Access.Areas[0].Nodes[1] = 3
	d.Access.Areas[0].Geometry[1] = Point{.002, .001}
	d.Access.Entrances = []AccessEntrance{{Node: 3, Point: Point{.002, .001}}}
	d.Segments = append(d.Segments, Segment{ID: "parking", Way: 2, From: 2, To: 3, Forward: true, Backward: true, Snap: true, DestinationForward: true, DestinationBackward: true})
	e.Point = Point{.00185, .0009}
	c, err := store(t, d).endpointSnap(context.Background(), e, "destination")
	if err != nil || c.Method != "mapped_parking_entrance" || !c.DestinationAccess {
		t.Fatal(c, err)
	}
	d.Access.Areas[0].Restricted = true
	if _, err := store(t, d).endpointSnap(context.Background(), e, "destination"); err == nil {
		t.Fatal("parking area access granted by address")
	}
	d.Access.Areas[0].Restricted = false
	d.Access.Areas[0].Nodes[1] = 99
	c, err = store(t, d).endpointSnap(context.Background(), e, "destination")
	if err == nil && c.Method == "mapped_parking_entrance" {
		t.Fatal("proximity fabricated polygon entrance", c)
	}
}
func TestAddressRetainedVersions(t *testing.T) {
	for v, profile := range map[int]string{1: "driving-distance-v1", 2: "driving-distance-v2"} {
		d := endpointFixture()
		d.Metadata.Version = v
		d.Metadata.Profile = profile
		s := store(t, d)
		_, err := s.RouteEndpoints(context.Background(), addr(Point{.001, .0001}, 1), Endpoint{Point: Point{.002, 0}})
		var re *Error
		if !errors.As(err, &re) || re.Outcome != "address_routing_unavailable" {
			t.Fatal(err)
		}
	}
}

func TestAccessIntegrity(t *testing.T) {
	d, _ := associationFixture()
	d.Access.Ways[0].Geometry[0] = Point{.001, 0}
	if _, err := New(d); err == nil {
		t.Fatal("conflicting source node coordinates accepted")
	}
	d, _ = associationFixture()
	d.Access.Areas[0].Nodes = d.Access.Areas[0].Nodes[:3]
	if _, err := New(d); err == nil {
		t.Fatal("malformed polygon accepted")
	}
	d, _ = associationFixture()
	d.Metadata.Version = 2
	d.Metadata.Profile = "driving-distance-v2"
	if _, err := New(d); err == nil {
		t.Fatal("unversioned access semantics accepted")
	}
}

func TestDrivewaySourceConnectivityAndBounds(t *testing.T) {
	d, e := associationFixture()
	d.Access.Ways = []AccessWay{
		{Way: 9, Driveway: true, Nodes: []int64{2, 7}, Geometry: []Point{{.002, 0}, {.002, .0005}}},
		{Way: 8, Driveway: true, Nodes: []int64{7, 3}, Geometry: []Point{{.002, .0005}, {.002, .001}}},
	}
	c, err := store(t, d).endpointSnap(context.Background(), e, "destination")
	if err != nil || c.Method != "mapped_driveway_public_junction" {
		t.Fatal(c, err)
	}
	// Identical coordinates with a different source node cannot connect pieces.
	d.Access.Ways[1].Nodes[0] = 99
	if _, err := store(t, d).endpointSnap(context.Background(), e, "destination"); err == nil {
		t.Fatal("joined crossing driveway geometry")
	}
	d, e = associationFixture()
	// Two different equally supported public arrival nodes cannot be resolved by
	// their source IDs or by trying which one routes successfully.
	d.Nodes = append(d.Nodes, Node{ID: 5, Point: Point{.0021, 0}}, Node{ID: 6, Point: Point{.003, 0}})
	d.Segments = append(d.Segments, Segment{ID: "second-public", Way: 11, From: 5, To: 6, Forward: true, Backward: true, Snap: true})
	d.Access.Ways = append(d.Access.Ways, AccessWay{Way: 8, Driveway: true, Nodes: []int64{3, 5}, Geometry: []Point{{.002, .001}, {.0021, 0}}})
	e.StreetWays[11] = true
	if _, err := store(t, d).endpointSnap(context.Background(), e, "destination"); err == nil {
		t.Fatal("arbitrary access point selected")
	}
}
