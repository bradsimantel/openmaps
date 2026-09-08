package importer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/paulmach/osm"
	"openmaps/internal/routing"
)

func TestDrivingProfile(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		tags                    map[string]string
		forward, backward, snap bool
	}{
		{"unnamed residential", map[string]string{"highway": "residential"}, true, true, true},
		{"unnamed service", map[string]string{"highway": "service"}, true, true, true},
		{"private", map[string]string{"highway": "residential", "access": "private"}, false, false, false},
		{"destination", map[string]string{"highway": "service", "access": "destination"}, true, true, true},
		{"mode override", map[string]string{"highway": "residential", "access": "no", "motor_vehicle": "yes"}, true, true, true},
		{"car override", map[string]string{"highway": "residential", "motor_vehicle": "yes", "motorcar": "no"}, false, false, false},
		{"oneway", map[string]string{"highway": "residential", "oneway": "yes"}, true, false, true},
		{"reverse", map[string]string{"highway": "residential", "oneway": "-1"}, false, true, true},
		{"roundabout", map[string]string{"highway": "residential", "junction": "roundabout"}, true, false, true},
		{"explicit roundabout override", map[string]string{"highway": "residential", "junction": "roundabout", "oneway": "no"}, true, true, true},
		{"motorway no snapping", map[string]string{"highway": "motorway"}, true, false, false},
		{"conditional", map[string]string{"highway": "residential", "access:conditional": "no @ (Mo-Fr)"}, false, false, false},
		{"conditional direction", map[string]string{"highway": "residential", "oneway:conditional": "yes @ (Mo-Fr)"}, false, false, false},
		{"alternating", map[string]string{"highway": "residential", "oneway": "alternating"}, false, false, false},
		{"directional access", map[string]string{"highway": "residential", "vehicle:backward": "no"}, true, false, true},
		{"direction override", map[string]string{"highway": "residential", "access": "no", "motor_vehicle:forward": "yes"}, true, false, true},
		{"mode outranks general direction", map[string]string{"highway": "residential", "motorcar": "yes", "access:forward": "no"}, true, true, true},
		{"footway", map[string]string{"highway": "footway", "motor_vehicle": "yes"}, false, false, false},
		{"area", map[string]string{"highway": "service", "area": "yes"}, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, b, s, _ := drivingWay(tc.tags)
			if f != tc.forward || b != tc.backward || s != tc.snap {
				t.Fatalf("got %v %v %v", f, b, s)
			}
		})
	}
}
func TestBarriersAndRestrictionInterpretation(t *testing.T) {
	for _, tc := range []struct {
		tags    map[string]string
		blocked bool
	}{{map[string]string{"barrier": "gate"}, true}, {map[string]string{"barrier": "bollard", "motorcar": "yes"}, true}, {map[string]string{"barrier": "gate", "motor_vehicle": "yes"}, false}, {map[string]string{"barrier": "gate", "motor_vehicle": "yes", "locked": "yes"}, true}, {map[string]string{"barrier": "toll_booth"}, false}, {map[string]string{"barrier": "kerb", "kerb": "raised"}, true}, {map[string]string{"barrier": "kerb", "kerb": "flush"}, false}, {map[string]string{"access": "private"}, true}} {
		if got := barrierBlocked(tc.tags); got != tc.blocked {
			t.Fatalf("%v: %v", tc.tags, got)
		}
	}
	v, ok := restrictionValue(map[string]string{"type": "restriction", "restriction:conditional": "no_left_turn @ (Mo-Fr 06:00-09:00)"})
	if !ok || v != "no_left_turn" {
		t.Fatal(v, ok)
	}
	_, ok = restrictionValue(map[string]string{"type": "restriction", "restriction": "no_left_turn", "except": "bus;motorcar"})
	if ok {
		t.Fatal("ignored car exemption")
	}
	v, ok = restrictionValue(map[string]string{"type": "restriction", "restriction:conditional": "no_left_turn @ (Mo); only_right_turn @ (Tu)"})
	if !ok || v != "unsupported" {
		t.Fatal("complex condition wasn't conservative")
	}
}
func TestViaWayRestrictionConnectivity(t *testing.T) {
	arcs := []arc{{1, 2, 10, routing.EdgeRef{Segment: "a"}}, {2, 3, 11, routing.EdgeRef{Segment: "b"}}, {3, 4, 11, routing.EdgeRef{Segment: "c"}}, {4, 5, 12, routing.EdgeRef{Segment: "d"}}, {6, 7, 11, routing.EdgeRef{Segment: "disconnected"}}}
	by, out := map[int64][]arc{}, map[int64][]arc{}
	for _, a := range arcs {
		by[a.way] = append(by[a.way], a)
		out[a.from] = append(out[a.from], a)
	}
	paths, e := restrictionPaths(10, 12, []osm.Member{{Type: osm.TypeWay, Ref: 11}}, by, out)
	if e != nil || len(paths) != 1 || len(paths[0]) != 4 {
		t.Fatal(paths, e)
	}
	paths, e = restrictionPaths(10, 12, []osm.Member{{Type: osm.TypeNode, Ref: 3}}, by, out)
	if e != nil || len(paths) != 0 {
		t.Fatal("invented node connectivity", paths, e)
	}
}
func TestRoutingPBFBuildReproducibilityAndMissingNodes(t *testing.T) {
	m, dir := regionFixture(t)
	b, _, e := Prepare(context.Background(), m, dir, nil)
	if e != nil {
		t.Fatal(e)
	}
	pbf := filepath.Join(dir, "streets.osm.pbf")
	source := m.Inputs[len(m.Inputs)-1]
	a, e := readRouting(context.Background(), pbf, source, m.BBox)
	if e != nil {
		t.Fatal(e)
	}
	z, e := readRouting(context.Background(), pbf, source, m.BBox)
	if e != nil || !reflect.DeepEqual(a, z) {
		t.Fatal("non-reproducible routing", e)
	}
	if len(a.Segments) != 1 {
		t.Fatalf("wrong PBF graph: %+v", a.Metadata)
	}
	db := filepath.Join(dir, "next.sqlite")
	if e = Build(context.Background(), db, b); e != nil {
		t.Fatal(e)
	}
	if e = AddRouting(context.Background(), db, pbf, b.Manifest); e != nil {
		t.Fatal(e)
	}
	snap, e := ReadSnapshot(context.Background(), db)
	if e != nil || snap.Routing == nil {
		t.Fatal("snapshot load", e)
	}
	s, e := routing.Open(context.Background(), db)
	if e != nil || s == nil {
		t.Fatal("runtime load", e)
	}
	if e = AddRouting(context.Background(), db, pbf, b.Manifest); e == nil {
		t.Fatal("overwrote routing graph")
	}
	if e = os.WriteFile(pbf, tinyPBF(true), 0600); e != nil {
		t.Fatal(e)
	}
	if e = AddRouting(context.Background(), db, pbf, b.Manifest); e == nil {
		t.Fatal("accepted changed checksum")
	}
	if _, e = readRouting(context.Background(), pbf, source, m.BBox); e == nil {
		t.Fatal("accepted missing nodes")
	}
	raw, _ := json.Marshal(a)
	if routing.Digest(raw) != snap.Routing.SHA256 {
		t.Fatal("graph fingerprint differs")
	}
}
