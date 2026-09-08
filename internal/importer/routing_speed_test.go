package importer

import (
	"context"
	"github.com/paulmach/osm"
	"math"
	"openmaps/internal/routing"
	"strings"
	"testing"
)

func TestSpeedUnits(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want float64
	}{{"25 mph", 40.2336}, {"25", 25}, {"25 km/h", 25}, {"25 kph", 25}, {"10 knots", 18.52}, {" 12.5 mph ", 20.1168}} {
		got, ok := speedKPH(tc.raw)
		if !ok || math.Abs(got-tc.want) > 1e-8 {
			t.Fatal(tc, got, ok)
		}
	}
	for _, raw := range []string{"", "0", "-5", "NaN", "Inf", "25 MPH", "25mph", "25;30", "25,5", "1e2", "301", "signals", "US:urban"} {
		if _, ok := speedKPH(raw); ok {
			t.Fatal("guessed speed", raw)
		}
	}
}
func TestDrivingSpeedPolicy(t *testing.T) {
	cases := []struct {
		name        string
		tags        map[string]string
		dir         string
		want, limit float64
		note        string
	}{
		{"missing", nil, "forward", 40, 0, "legal maximum absent"},
		{"posted is not average", map[string]string{"maxspeed": "25 mph"}, "forward", 32.18688, 40.2336, "base limit"},
		{"unitless US value", map[string]string{"maxspeed": "25"}, "forward", 20, 25, "base limit"},
		{"forward override", map[string]string{"maxspeed": "20", "maxspeed:forward": "40", "maxspeed:backward": "10"}, "forward", 32, 40, "maxspeed:forward"},
		{"backward override", map[string]string{"maxspeed": "20", "maxspeed:forward": "40", "maxspeed:backward": "10"}, "backward", 8, 10, "maxspeed:backward"},
		{"car precedence", map[string]string{"maxspeed:forward": "10", "maxspeed:motorcar": "30"}, "forward", 24, 30, "maxspeed:motorcar"},
		{"walk", map[string]string{"maxspeed": "walk"}, "forward", 4, 0, "legal walking speed unspecified"},
		{"none", map[string]string{"maxspeed": "none"}, "forward", 40, 0, "no numeric ceiling"},
		{"symbolic", map[string]string{"maxspeed": "US:urban"}, "forward", 5, 0, "unresolved"},
		{"variable", map[string]string{"maxspeed": "signals"}, "forward", 5, 0, "unresolved"},
		{"malformed", map[string]string{"maxspeed": "25;30"}, "forward", 5, 0, "unresolved"},
		{"conditional minimum", map[string]string{"maxspeed": "50", "maxspeed:conditional": "20 mph @ (Mo-Fr 07:00-18:00; PH off); 10 @ wet"}, "forward", 8, 10, "all conditional ceilings"},
		{"conditional direction", map[string]string{"maxspeed:backward:conditional": "10 @ wet"}, "forward", 40, 0, "legal maximum absent"},
		{"conditional malformed", map[string]string{"maxspeed:conditional": "20 @ (wet"}, "forward", 5, 0, "malformed conditional"},
		{"conditional unknown", map[string]string{"maxspeed:conditional": "signals @ wet"}, "forward", 5, 0, "unresolved"},
		{"noncanonical scoped key", map[string]string{"maxspeed:forward:motorcar": "20"}, "forward", 5, 0, "unsupported speed key"},
		{"lanes unresolved", map[string]string{"maxspeed:lanes": "30|50"}, "forward", 5, 0, "unsupported speed key"},
		{"irrelevant heavy vehicle", map[string]string{"maxspeed:hgv:conditional": "5 @ wet"}, "forward", 40, 0, "legal maximum absent"},
		{"advisory not legal", map[string]string{"maxspeed:advisory": "20"}, "forward", 20, 0, "advisory cap"},
		{"parking", map[string]string{"highway": "service", "service": "parking_aisle"}, "forward", 7, 0, "class default"},
		{"residential", map[string]string{"highway": "residential"}, "forward", 25, 0, "class default"},
		{"trunk roundabout", map[string]string{"highway": "trunk", "junction": "roundabout"}, "forward", 20, 0, "circulatory-road cap"},
		{"circular", map[string]string{"highway": "primary", "junction": "circular"}, "forward", 20, 0, "circulatory-road cap"},
		{"rough surface", map[string]string{"surface": "gravel"}, "forward", 15, 0, "surface cap"},
		{"unknown surface", map[string]string{"surface": "unknown"}, "forward", 10, 0, "unknown surface"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tags := map[string]string{"highway": "primary"}
			for k, v := range tc.tags {
				tags[k] = v
			}
			s := drivingSpeed(tags, tc.dir)
			if math.Abs(s.KPH-tc.want) > 1e-8 || math.Abs(s.LimitKPH-tc.limit) > 1e-8 || !strings.Contains(strings.Join(s.Notes, ";"), tc.note) {
				t.Fatal(s, tc)
			}
		})
	}
	for _, v := range []string{"20", "20 @", "20 @ (wet))", "20 @ wet;", "20 @ wet @ snow"} {
		if _, ok := conditionalSpeeds(v); ok {
			t.Fatal("malformed condition", v)
		}
	}
}

func TestFastCostsCannotOpenRestrictedRoads(t *testing.T) {
	for _, tc := range []struct{ name, key, value string }{{"private", "access", "private"}, {"destination", "access", "destination"}, {"dimensions", "maxwidth", "1"}, {"barrier", "barrier", "bollard"}} {
		t.Run(tc.name, func(t *testing.T) {
			n, w := sourceGraphFixture()
			// The direct road is modeled as much faster; it is still ineligible.
			w[1].Tags = osm.Tags{{Key: "highway", Value: "primary"}, {Key: "maxspeed", Value: "100"}, {Key: tc.key, Value: tc.value}}
			_, s := importSourceGraph(t, n, w)
			r, e := s.Route(context.Background(), routing.Point{.001, 0}, routing.Point{.008, .001})
			if e != nil {
				t.Fatal(e)
			}
			for _, id := range r.Segments {
				if id == "2:0" {
					t.Fatal("cost bypassed restriction", tc, r)
				}
			}
		})
	}
}
func TestPointSpeedAndPersistedDirections(t *testing.T) {
	n, w := sourceGraphFixture()
	w[0].Tags = append(w[0].Tags, osm.Tag{Key: "maxspeed:forward", Value: "10"}, osm.Tag{Key: "maxspeed:backward", Value: "20"})
	n[4].Tags = osm.Tags{{Key: "maxspeed:conditional", Value: "5 mph @ flashing"}}
	d, _ := importSourceGraph(t, n, w)
	for _, c := range d.Costs {
		if c.Way == 1 && (c.Forward.KPH != 8 || c.Backward.KPH != 16) {
			t.Fatal(c)
		}
		if c.Way == 4 && (math.Abs(c.Forward.KPH-6.437376) > 1e-8 || !strings.Contains(strings.Join(c.Forward.Notes, ";"), "zone/direction unknown")) {
			t.Fatal(c)
		}
	}
}
