package importer

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"testing"

	"github.com/paulmach/osm"
	"google.golang.org/protobuf/encoding/protowire"
	"openmaps/internal/routing"
)

// Minimal real PBF encoder for deterministic node/way/relation fixtures. No
// downloads, external executables or provider-generated regional data are needed.
func routingPBF(nodes []*osm.Node, ways []*osm.Way, relations []*osm.Relation) []byte {
	bytesField := func(n protowire.Number, b []byte) []byte {
		return protowire.AppendBytes(protowire.AppendTag(nil, n, protowire.BytesType), b)
	}
	scalar := func(n protowire.Number, v uint64) []byte {
		return protowire.AppendVarint(protowire.AppendTag(nil, n, protowire.VarintType), v)
	}
	packed := func(n protowire.Number, vs []uint64) []byte {
		var b []byte
		for _, v := range vs {
			b = protowire.AppendVarint(b, v)
		}
		return bytesField(n, b)
	}
	stringsSet := map[string]bool{"": true, "from": true, "to": true, "via": true}
	tags := []osm.Tags{}
	for _, n := range nodes {
		tags = append(tags, n.Tags)
	}
	for _, w := range ways {
		tags = append(tags, w.Tags)
	}
	for _, r := range relations {
		tags = append(tags, r.Tags)
	}
	for _, ts := range tags {
		for _, t := range ts {
			stringsSet[t.Key] = true
			stringsSet[t.Value] = true
		}
	}
	tableValues := make([]string, 0, len(stringsSet))
	for s := range stringsSet {
		tableValues = append(tableValues, s)
	}
	sort.Strings(tableValues)
	indexes := map[string]uint64{}
	var table []byte
	for i, s := range tableValues {
		indexes[s] = uint64(i)
		table = append(table, bytesField(1, []byte(s))...)
	}
	encodeTags := func(ts osm.Tags) []byte {
		keys, values := []uint64{}, []uint64{}
		for _, t := range ts {
			keys = append(keys, indexes[t.Key])
			values = append(values, indexes[t.Value])
		}
		return append(packed(2, keys), packed(3, values)...)
	}
	var group []byte
	ids, lats, lons, tagsPacked := []uint64{}, []uint64{}, []uint64{}, []uint64{}
	var lastID, lastLat, lastLon int64
	for _, n := range nodes {
		lat, lon := int64(math.Round(n.Lat*1e7)), int64(math.Round(n.Lon*1e7))
		ids = append(ids, protowire.EncodeZigZag(int64(n.ID)-lastID))
		lastID = int64(n.ID)
		lats = append(lats, protowire.EncodeZigZag(lat-lastLat))
		lastLat = lat
		lons = append(lons, protowire.EncodeZigZag(lon-lastLon))
		lastLon = lon
		for _, tag := range n.Tags {
			tagsPacked = append(tagsPacked, indexes[tag.Key], indexes[tag.Value])
		}
		tagsPacked = append(tagsPacked, 0)
	}
	dense := packed(1, ids)
	dense = append(dense, packed(8, lats)...)
	dense = append(dense, packed(9, lons)...)
	dense = append(dense, packed(10, tagsPacked)...)
	group = append(group, bytesField(2, dense)...)
	for _, w := range ways {
		b := scalar(1, uint64(w.ID))
		b = append(b, encodeTags(w.Tags)...)
		var refs []uint64
		var last int64
		for _, n := range w.Nodes {
			refs = append(refs, protowire.EncodeZigZag(int64(n.ID)-last))
			last = int64(n.ID)
		}
		b = append(b, packed(8, refs)...)
		group = append(group, bytesField(3, b)...)
	}
	for _, r := range relations {
		b := scalar(1, uint64(r.ID))
		b = append(b, encodeTags(r.Tags)...)
		roles, refs, types := []uint64{}, []uint64{}, []uint64{}
		var last int64
		for _, m := range r.Members {
			roles = append(roles, indexes[m.Role])
			refs = append(refs, protowire.EncodeZigZag(m.Ref-last))
			last = m.Ref
			typ := uint64(0)
			if m.Type == osm.TypeWay {
				typ = 1
			}
			types = append(types, typ)
		}
		b = append(b, packed(8, roles)...)
		b = append(b, packed(9, refs)...)
		b = append(b, packed(10, types)...)
		group = append(group, bytesField(4, b)...)
	}
	block := func(kind string, b []byte) []byte {
		blob := bytesField(1, b)
		header := append(bytesField(1, []byte(kind)), scalar(3, uint64(len(blob)))...)
		out := make([]byte, 4)
		binary.BigEndian.PutUint32(out, uint32(len(header)))
		return append(append(out, header...), blob...)
	}
	primitive := append(bytesField(1, table), bytesField(2, group)...)
	return append(block("OSMHeader", append(bytesField(4, []byte("OsmSchema-V0.6")), bytesField(4, []byte("DenseNodes"))...)), block("OSMData", primitive)...)
}
func sourceGraphFixture() ([]*osm.Node, []*osm.Way) {
	nodes := []*osm.Node{{ID: 1, Lon: 0, Lat: 0}, {ID: 2, Lon: .004, Lat: 0}, {ID: 3, Lon: .008, Lat: 0}, {ID: 4, Lon: .004, Lat: .004}, {ID: 5, Lon: .008, Lat: .004}}
	ways := []*osm.Way{}
	for i, pair := range [][2]int64{{1, 2}, {2, 3}, {2, 4}, {4, 5}, {5, 3}} {
		ways = append(ways, &osm.Way{ID: osm.WayID(i + 1), Tags: osm.Tags{{Key: "highway", Value: "residential"}}, Nodes: osm.WayNodes{{ID: osm.NodeID(pair[0])}, {ID: osm.NodeID(pair[1])}}})
	}
	return nodes, ways
}
func sourceRestriction(value string, from, to int64, via ...osm.Member) *osm.Relation {
	members := []osm.Member{{Type: osm.TypeWay, Ref: from, Role: "from"}}
	members = append(members, via...)
	members = append(members, osm.Member{Type: osm.TypeWay, Ref: to, Role: "to"})
	return &osm.Relation{ID: 99, Tags: osm.Tags{{Key: "type", Value: "restriction"}, {Key: "restriction", Value: value}}, Members: members}
}
func importSourceGraph(t *testing.T, n []*osm.Node, w []*osm.Way, r ...*osm.Relation) (routing.Data, *routing.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.osm.pbf")
	if e := os.WriteFile(path, routingPBF(n, w, r), 0600); e != nil {
		t.Fatal(e)
	}
	d, e := readRouting(context.Background(), path, Input{Release: "fixture"}, [4]float64{-1, -1, 1, 1})
	if e != nil {
		t.Fatal(e)
	}
	s, e := routing.New(d)
	if e != nil {
		t.Fatal(e)
	}
	return d, s
}
func TestImportedDrivingRestrictions(t *testing.T) {
	nodeVia := osm.Member{Type: osm.TypeNode, Ref: 2, Role: "via"}
	for _, value := range []string{"no_straight_on", "only_left_turn", "conditional"} {
		t.Run(value, func(t *testing.T) {
			n, w := sourceGraphFixture()
			to := int64(2)
			if value == "only_left_turn" {
				to = 3
			}
			r := sourceRestriction(value, 1, to, nodeVia)
			if value == "conditional" {
				r.Tags = osm.Tags{{Key: "type", Value: "restriction"}, {Key: "restriction:conditional", Value: "no_straight_on @ (Mo-Fr 06:00-09:00)"}}
			}
			_, s := importSourceGraph(t, n, w, r)
			route, e := s.Route(context.Background(), routing.Point{0, 0}, routing.Point{.008, 0})
			if e != nil || route.Distance < 1700 {
				t.Fatalf("restriction failed: %+v %v", route, e)
			}
		})
	}
	t.Run("motor access detour", func(t *testing.T) {
		n, w := sourceGraphFixture()
		w[1].Tags = append(w[1].Tags, osm.Tag{Key: "motor_vehicle", Value: "no"})
		_, s := importSourceGraph(t, n, w)
		r, e := s.Route(context.Background(), routing.Point{0, 0}, routing.Point{.008, 0})
		if e != nil || r.Distance < 1700 {
			t.Fatal(r, e)
		}
	})
	t.Run("barrier removes incident travel", func(t *testing.T) {
		n, w := sourceGraphFixture()
		n[1].Tags = osm.Tags{{Key: "barrier", Value: "gate"}}
		d, _ := importSourceGraph(t, n, w)
		for _, s := range d.Segments {
			if s.From == 2 || s.To == 2 {
				t.Fatal("barrier node traversable", s)
			}
		}
	})
	t.Run("malformed relation closes junction", func(t *testing.T) {
		n, w := sourceGraphFixture()
		r := sourceRestriction("only_straight_on", 1, 2, nodeVia)
		r.Members = r.Members[1:]
		d, _ := importSourceGraph(t, n, w, r)
		for _, s := range d.Segments {
			if s.From == 2 || s.To == 2 {
				t.Fatal("malformed relation silently ignored", s)
			}
		}
	})
	t.Run("via ways prohibit full sequence only", func(t *testing.T) {
		n, w := sourceGraphFixture()
		w[1].Tags = append(w[1].Tags, osm.Tag{Key: "access", Value: "private"})
		r := sourceRestriction("no_straight_on", 1, 5, osm.Member{Type: osm.TypeWay, Ref: 3, Role: "via"}, osm.Member{Type: osm.TypeWay, Ref: 4, Role: "via"})
		d, s := importSourceGraph(t, n, w, r)
		if len(d.Bans) != 1 || len(d.Bans[0].Path) != 4 {
			t.Fatal("missing via-way path", d.Bans)
		}
		if _, e := s.Route(context.Background(), routing.Point{0, 0}, routing.Point{.008, 0}); e == nil {
			t.Fatal("prohibited via path found")
		}
		if _, e := s.Route(context.Background(), routing.Point{.004, .004}, routing.Point{.008, 0}); e != nil {
			t.Fatal("side entrance wrongly prohibited", e)
		}
	})
	t.Run("only via way with excluded target", func(t *testing.T) {
		n, w := sourceGraphFixture()
		w[4].Tags = append(w[4].Tags, osm.Tag{Key: "access", Value: "private"})
		r := sourceRestriction("only_straight_on", 1, 5, osm.Member{Type: osm.TypeWay, Ref: 3, Role: "via"}, osm.Member{Type: osm.TypeWay, Ref: 4, Role: "via"})
		_, s := importSourceGraph(t, n, w, r)
		if _, e := s.Route(context.Background(), routing.Point{0, 0}, routing.Point{.008, 0}); e == nil {
			t.Fatal("excluded only target freed illegal exit")
		}
	})
	t.Run("no u turn preserves straight on same way", func(t *testing.T) {
		n, w := sourceGraphFixture()
		w[0].Nodes = append(w[0].Nodes, osm.WayNode{ID: 3})
		w = append(w[:1], w[2:]...)
		r := sourceRestriction("no_u_turn", 1, 1, nodeVia)
		_, s := importSourceGraph(t, n, w, r)
		route, e := s.Route(context.Background(), routing.Point{0, 0}, routing.Point{.008, 0})
		if e != nil || route.Distance > 900 {
			t.Fatal(route, e)
		}
	})
}

func TestImportedCarLimitsAndRestrictedEndpoints(t *testing.T) {
	for _, value := range []string{"destination", "private", "customers", "delivery", "permit"} {
		t.Run(value, func(t *testing.T) {
			n, w := sourceGraphFixture()
			w[1].Tags = append(w[1].Tags, osm.Tag{Key: "access", Value: value})
			d, s := importSourceGraph(t, n, w)
			r, e := s.Route(context.Background(), routing.Point{.001, 0}, routing.Point{.006, 0})
			if value == "destination" {
				if e != nil || r.Distance > 700 || !r.Destination.DestinationAccess {
					t.Fatal(r, e)
				}
			} else {
				if e == nil || len(d.Guards) == 0 {
					t.Fatal("unauthorized endpoint borrowed public road", r, e)
				}
			}
			r, e = s.Route(context.Background(), routing.Point{.001, 0}, routing.Point{.008, .001})
			if e != nil || r.Distance < 1300 {
				t.Fatal("restricted through shortcut", r, e)
			}
		})
	}
	for _, limit := range []string{"2 st", "1 st", "garbled"} {
		n, w := sourceGraphFixture()
		w[1].Tags = append(w[1].Tags, osm.Tag{Key: "maxweight", Value: limit})
		_, s := importSourceGraph(t, n, w)
		r, e := s.Route(context.Background(), routing.Point{0, 0}, routing.Point{.008, 0})
		if e != nil {
			t.Fatal(e)
		}
		if (limit == "2 st") != (r.Distance < 900) {
			t.Fatal("imported car threshold", limit, r)
		}
	}
}

// Included and excluded pieces interleave in numeric source order, which differs
// from lexical reference order at way 2/10 and vertex 2/10. Reordered PBF objects,
// barrier gaps, duplicate vertices and absent-profile ways must preserve the join.
func TestRoutingGuardSourceOrderJoin(t *testing.T) {
	nodes := []*osm.Node{}
	for i := 1; i <= 15; i++ {
		nodes = append(nodes, &osm.Node{ID: osm.NodeID(i), Lon: float64(i) * .001, Lat: 0})
	}
	nodes[4].Tags = osm.Tags{{Key: "barrier", Value: "gate"}, {Key: "access", Value: "no"}}
	way := func(id osm.WayID, highway, access string, refs ...int) *osm.Way {
		w := &osm.Way{ID: id, Tags: osm.Tags{{Key: "highway", Value: highway}}}
		if access != "" {
			w.Tags = append(w.Tags, osm.Tag{Key: "access", Value: access})
		}
		for _, n := range refs {
			w.Nodes = append(w.Nodes, osm.WayNode{ID: osm.NodeID(n)})
		}
		return w
	}
	ways := []*osm.Way{way(10, "residential", "", 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15), way(2, "residential", "private", 1, 2), way(3, "footway", "", 2, 3), way(1, "residential", "", 1, 1, 2)}
	d, _ := importSourceGraph(t, nodes, ways)
	included := []string{}
	for _, s := range d.Segments {
		included = append(included, s.ID)
	}
	want := []string{"1:1", "10:0", "10:1", "10:2", "10:5", "10:6", "10:7", "10:8", "10:9", "10:10", "10:11", "10:12", "10:13"}
	// Source construction owns numeric order; subsequent routing.Load owns the
	// canonical lexical order. importSourceGraph's New preserves caller ownership.
	if !reflect.DeepEqual(included, want) {
		t.Fatalf("included pieces: %v", included)
	}
	guards := []string{}
	for _, g := range d.Guards {
		guards = append(guards, g.Segment)
	}
	if !reflect.DeepEqual(guards, []string{"2:0", "10:3", "10:4"}) {
		t.Fatalf("guards: %v", guards)
	}
	slices.Reverse(ways)
	next, _ := importSourceGraph(t, nodes, ways)
	if !reflect.DeepEqual(d, next) {
		t.Fatal("PBF object ordering changed graph or provenance")
	}
}
