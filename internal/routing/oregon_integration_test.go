//go:build integration

package routing

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

type oregonCase struct {
	Name                string
	Origin, Destination Point
	Outcome             string
	Max                 float64 `json:"max_meters"`
	RequireBridge       bool    `json:"require_bridge"`
	Note                string  `json:"evidence_note"`
	Evidence            []struct {
		Way     int64
		Segment string
		From    int64 `json:"from_node"`
		To      int64 `json:"to_node"`
		Tags    map[string]string
	}
}

func errorOutcome(err error) string {
	if err == nil {
		return "routed"
	}
	if e, ok := err.(*Error); ok {
		return e.Outcome
	}
	return err.Error()
}
func TestOregonRouting(t *testing.T) {
	path := os.Getenv("OPENMAPS_OREGON_DB")
	if path == "" {
		t.Skip("set OPENMAPS_OREGON_DB")
	}
	raw, err := os.ReadFile("testdata/oregon.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []oregonCase
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	results := make([]Result, len(cases))
	wanted := map[int64]bool{}
	segment := func(id string) Segment {
		i := sort.Search(len(s.segments), func(i int) bool { return s.segments[i].ID >= id })
		if i == len(s.segments) || s.segments[i].ID != id {
			t.Fatal("unknown segment", id)
		}
		return s.segments[i]
	}
	for i, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			if tc.Note == "" {
				t.Fatal("missing independent expectation")
			}
			for _, e := range tc.Evidence {
				wanted[e.Way] = true
			}
			query, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			start := time.Now()
			fast, fe := s.Route(query, tc.Origin, tc.Destination)
			fastMS := float64(time.Since(start).Microseconds()) / 1000
			start = time.Now()
			ref, re := s.RouteReferenceEndpoints(query, Endpoint{Point: tc.Origin}, Endpoint{Point: tc.Destination})
			refMS := float64(time.Since(start).Microseconds()) / 1000
			if errorOutcome(fe) != tc.Outcome {
				t.Errorf("source/invariant expectation %s got %v", tc.Outcome, fe)
			}
			tolerance := math.Max(1e-6, math.Abs(ref.Duration)*1e-10)
			if errorOutcome(fe) != errorOutcome(re) || math.Abs(fast.Duration-ref.Duration) > tolerance || !reflect.DeepEqual(fast.Origin, ref.Origin) || !reflect.DeepEqual(fast.Destination, ref.Destination) {
				t.Fatal("reference disagreement", fast.Duration, ref.Duration, fe, re)
			}
			maxLat := -90.0
			for _, p := range fast.Geometry {
				maxLat = math.Max(maxLat, p[1])
			}
			t.Logf("OREGON name=%q outcome=%s meters=%.3f seconds=%.6f accelerated_ms=%.3f reference_ms=%.3f path_equal=%v max_latitude=%.7f", tc.Name, errorOutcome(fe), fast.Distance, fast.Duration, fastMS, refMS, reflect.DeepEqual(fast.Segments, ref.Segments), maxLat)
			results[i] = fast
			if fe != nil {
				return
			}
			if fast.Distance+1e-5 < Distance(fast.Origin.Point, fast.Destination.Point) || fast.Distance > tc.Max {
				t.Error("geographic distance bound", fast.Distance, tc.Max)
			}
			if fast.Origin.Distance > 1e-5 || fast.Destination.Distance > 1e-5 {
				t.Error("source midpoint moved off road", fast.Origin.Distance, fast.Destination.Distance)
			}
			for _, id := range fast.Segments {
				wanted[segment(id).Way] = true
			}
		})
	}
	// Stream only the source records used by the independently specified evidence
	// and returned paths; full regional provenance does not stay in the test heap.
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, _, _, err := readManifest(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	type way struct {
		Nodes []int64
		Tags  map[string]string
	}
	ways := map[int64]way{}
	costs := map[int64]WayCost{}
	bans := map[EdgeRef][]Ban{}
	for _, c := range m.Chunks {
		switch c.Kind {
		case "sources":
			sources, e := readChunk[Source](ctx, db, c)
			if e != nil {
				t.Fatal(e)
			}
			for _, source := range sources {
				if source.Kind == "way" && wanted[source.ID] {
					var w way
					if e = json.Unmarshal(source.Raw, &w); e != nil {
						t.Fatal(e)
					}
					ways[source.ID] = w
				}
			}
		case "costs":
			cs, e := readChunk[WayCost](ctx, db, c)
			if e != nil {
				t.Fatal(e)
			}
			for _, c := range cs {
				if wanted[c.Way] {
					costs[c.Way] = c
				}
			}
		case "bans":
			bs, e := readChunk[Ban](ctx, db, c)
			if e != nil {
				t.Fatal(e)
			}
			for _, b := range bs {
				bans[b.Path[0]] = append(bans[b.Path[0]], b)
			}
		}
	}
	for i, tc := range cases {
		t.Run(tc.Name+"/source-path", func(t *testing.T) {
			for _, ev := range tc.Evidence {
				seg := segment(ev.Segment)
				if seg.Way != ev.Way || seg.From != ev.From || seg.To != ev.To {
					t.Fatal("source segment identity changed")
				}
				for k, v := range ev.Tags {
					if ways[ev.Way].Tags[k] != v {
						t.Fatal("source tag changed", ev.Way, k)
					}
				}
			}
			r := results[i]
			if tc.Outcome != "routed" {
				return
			}
			if len(r.Geometry) != len(r.Segments)+1 {
				t.Fatal("unexpanded geometry")
			}
			path := []EdgeRef{}
			distance, seconds := 0.0, 0.0
			bridge := false
			for j, id := range r.Segments {
				seg := segment(id)
				w := ways[seg.Way]
				adjacent := false
				for k := 1; k < len(w.Nodes); k++ {
					if w.Nodes[k-1] == seg.From && w.Nodes[k] == seg.To {
						adjacent = true
						break
					}
				}
				if !adjacent {
					t.Fatal("not adjacent source nodes", id)
				}
				a, b := r.Geometry[j], r.Geometry[j+1]
				from, to := s.point(seg.From), s.point(seg.To)
				full := Distance(from, to)
				for _, p := range []Point{a, b} {
					if math.Abs(Distance(from, p)+Distance(p, to)-full) > .05 {
						t.Fatal("geometry left source road")
					}
				}
				reverse := Distance(from, a) > Distance(from, b)
				if reverse && !seg.Backward || !reverse && !seg.Forward {
					t.Fatal("one-way violated")
				}
				path = append(path, EdgeRef{id, reverse})
				length := Distance(a, b)
				distance += length
				speed := costs[seg.Way].Forward.KPH
				if reverse {
					speed = costs[seg.Way].Backward.KPH
				}
				seconds += length * 3.6 / speed
				bridge = bridge || w.Tags["bridge"] == "yes"
				if j > 0 {
					prev := segment(r.Segments[j-1])
					pr := path[j-1].Reverse
					end := prev.To
					if pr {
						end = prev.From
					}
					begin := seg.From
					if reverse {
						begin = seg.To
					}
					if end != begin {
						t.Fatal("invented junction")
					}
				}
				if (seg.DestinationForward && !reverse || seg.DestinationBackward && reverse) && !r.Origin.DestinationAccess && !r.Destination.DestinationAccess {
					t.Fatal("destination shortcut")
				}
			}
			for j, first := range path {
				for _, ban := range bans[first] {
					if j+len(ban.Path) <= len(path) && reflect.DeepEqual(path[j:j+len(ban.Path)], ban.Path) {
						t.Fatal("prohibited source maneuver", ban.Relation)
					}
				}
			}
			if math.Abs(distance-r.Distance) > 1e-6 || math.Abs(seconds-r.Duration) > math.Max(1e-6, seconds*1e-10) {
				t.Fatal("independent cost sum disagrees")
			}
			if tc.RequireBridge && !bridge {
				t.Fatal("required source bridge absent")
			}
			type roadUse struct {
				Name, Ref, Highway string
				Meters, Seconds    float64
			}
			used := []roadUse{}
			for j, id := range r.Segments {
				seg := segment(id)
				w := ways[seg.Way]
				length := Distance(r.Geometry[j], r.Geometry[j+1])
				speed := costs[seg.Way].Forward.KPH
				if path[j].Reverse {
					speed = costs[seg.Way].Backward.KPH
				}
				v := roadUse{w.Tags["name"], w.Tags["ref"], w.Tags["highway"], length, length * 3.6 / speed}
				if len(used) > 0 && used[len(used)-1].Name == v.Name && used[len(used)-1].Ref == v.Ref {
					used[len(used)-1].Meters += v.Meters
					used[len(used)-1].Seconds += v.Seconds
				} else {
					used = append(used, v)
				}
			}
			encoded, _ := json.Marshal(map[string]any{"name": tc.Name, "roads": used, "geometry": r.Geometry, "segments": r.Segments})
			if out := os.Getenv("OPENMAPS_OREGON_REPORT"); out != "" {
				f, e := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
				if e != nil {
					t.Fatal(e)
				}
				if _, e = f.Write(append(encoded, '\n')); e != nil {
					t.Fatal(e)
				}
				f.Close()
			}

		})
	}
	runtime.KeepAlive(s)
}

// Shared wire request used by the live benchmark and isolated deployment tests.
func routeRequest(a, b Point) []byte {
	wp := func(p Point) any {
		return map[string]any{"location": map[string]any{"latLng": map[string]float64{"latitude": p[1], "longitude": p[0]}}}
	}
	raw, _ := json.Marshal(map[string]any{"origin": wp(a), "destination": wp(b), "polylineEncoding": "GEO_JSON_LINESTRING"})
	return raw
}
func TestLiveOregonRouting(t *testing.T) {
	base := os.Getenv("OPENMAPS_OREGON_URL")
	if base == "" {
		t.Skip("set OPENMAPS_OREGON_URL")
	}
	raw, err := os.ReadFile("testdata/oregon.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []oregonCase
	json.Unmarshal(raw, &cases)
	client := &http.Client{Timeout: 30 * time.Second}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			req, _ := http.NewRequest("POST", base+"/directions/v2:computeRoutes", bytes.NewReader(routeRequest(tc.Origin, tc.Destination)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Goog-FieldMask", "routes.distanceMeters,routes.duration,routes.staticDuration,routes.polyline")
			res, err := client.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			raw, err := io.ReadAll(res.Body)
			if err != nil {
				t.Fatal(err)
			}
			var body struct {
				Routes []struct {
					Distance                 float64 `json:"distanceMeters"`
					Duration, StaticDuration string
				}
				Openmaps struct{ Outcome string }
			}
			if err = json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			expected := tc.Outcome
			if expected == "invalid_coordinate" {
				expected = "unsupported_input"
			}
			if body.Openmaps.Outcome != expected {
				t.Fatal(res.Status, string(raw))
			}
			if expected == "routed" {
				if res.StatusCode != 200 || len(body.Routes) != 1 || body.Routes[0].Duration != body.Routes[0].StaticDuration {
					t.Fatal(string(raw))
				}
				t.Logf("HTTP %s %s", tc.Name, body.Routes[0].Duration)
			} else if expected == "unreachable" {
				if res.StatusCode != 200 || len(body.Routes) != 0 {
					t.Fatal(string(raw))
				}
			} else if res.StatusCode != 400 {
				t.Fatal(res.Status, string(raw))
			}
		})
	}
}

func TestOregonSensitivity(t *testing.T) {
	path := os.Getenv("OPENMAPS_OREGON_DB")
	if path == "" {
		t.Skip("set OPENMAPS_OREGON_DB")
	}
	ctx := context.Background()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile("testdata/oregon.json")
	var cases []oregonCase
	json.Unmarshal(raw, &cases)
	for _, tc := range cases {
		if tc.Name != "Bend to Medford" && tc.Name != "Klamath Falls to Ashland" {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			a, b := Endpoint{Point: tc.Origin}, Endpoint{Point: tc.Destination}
			fast, e := s.RouteEndpoints(ctx, a, b)
			short, f := s.RouteDistanceEndpoints(ctx, a, b)
			if e != nil || f != nil || short.Distance > fast.Distance+1e-6 || fast.Duration > short.Duration+1e-6 {
				t.Fatal("objective disagreement", e, f)
			}
			t.Logf("SENSITIVITY %s time_meters=%.3f time_seconds=%.3f shortest_meters=%.3f shortest_seconds=%.3f", tc.Name, fast.Distance, fast.Duration, short.Distance, short.Duration)
			wanted := map[int64]bool{}
			for _, id := range short.Segments {
				i := sort.Search(len(s.segments), func(i int) bool { return s.segments[i].ID >= id })
				wanted[s.segments[i].Way] = true
			}
			db, e := sql.Open("sqlite", "file:"+path+"?mode=ro")
			if e != nil {
				t.Fatal(e)
			}
			defer db.Close()
			m, _, _, e := readManifest(ctx, db)
			if e != nil {
				t.Fatal(e)
			}
			costs := map[int64]WayCost{}
			sources := []Source{}
			for _, c := range m.Chunks {
				if c.Kind == "costs" {
					values, e := readChunk[WayCost](ctx, db, c)
					if e != nil {
						t.Fatal(e)
					}
					for _, v := range values {
						if wanted[v.Way] {
							costs[v.Way] = v
						}
					}
				} else if c.Kind == "sources" {
					values, e := readChunk[Source](ctx, db, c)
					if e != nil {
						t.Fatal(e)
					}
					for _, v := range values {
						if v.Kind == "way" && wanted[v.ID] {
							sources = append(sources, v)
						}
					}
				}
			}
			if out := os.Getenv("OPENMAPS_OREGON_REPORT"); out != "" {
				raw, _ := json.Marshal(map[string]any{"name": tc.Name, "fast": fast, "short": short, "costs": costs, "sources": sources})
				f, e := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
				if e != nil {
					t.Fatal(e)
				}
				f.Write(append(raw, '\n'))
				f.Close()
			}
		})
	}
}

func TestOregonBoundaryCoverage(t *testing.T) {
	before, after := os.Getenv("OPENMAPS_OREGON_BOUNDARY_BEFORE"), os.Getenv("OPENMAPS_OREGON_DB")
	if before == "" || after == "" {
		t.Skip("set Oregon boundary comparison paths")
	}
	raw, _ := os.ReadFile("testdata/oregon.json")
	var all []oregonCase
	json.Unmarshal(raw, &all)
	cases := []oregonCase{}
	for _, c := range all {
		if strings.Contains(c.Name, "via Idaho") {
			cases = append(cases, c)
		}
	}
	observations := make([][]Result, 2)
	outcomes := make([][]string, 2)
	for i, path := range []string{before, after} {
		s, err := Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cases {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			r, e := s.Route(ctx, c.Origin, c.Destination)
			cancel()
			observations[i] = append(observations[i], r)
			outcomes[i] = append(outcomes[i], errorOutcome(e))
			t.Logf("BOUNDARY input=%d case=%q outcome=%s meters=%.3f seconds=%.3f", i, c.Name, errorOutcome(e), r.Distance, r.Duration)
		}
		s = nil
		runtime.GC()
	}
	for i := range cases {
		r := observations[1][i]
		if outcomes[1][i] != "routed" {
			t.Fatal("Idaho corridor did not connect", cases[i].Name)
		}
		crossed := false
		for _, p := range r.Geometry {
			crossed = crossed || p[0] > -117 && p[1] > 43.2 && p[1] < 43.7
		}
		if !crossed {
			t.Fatal("expected US 95 corridor in Idaho absent")
		}
		if outcomes[0][i] == "routed" && observations[0][i].Duration <= r.Duration {
			t.Fatal("buffer did not demonstrate improved boundary path")
		}
	}
}
