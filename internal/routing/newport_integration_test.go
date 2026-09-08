//go:build integration

package routing_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"openmaps/internal/api"
	"openmaps/internal/dataset"
	"openmaps/internal/importer"
	"openmaps/internal/routing"
)

func regionalPaths(t *testing.T) (string, string) {
	t.Helper()
	base, next := os.Getenv("OPENMAPS_BASELINE"), os.Getenv("OPENMAPS_CANDIDATE")
	if base == "" || next == "" {
		t.Fatal("set OPENMAPS_BASELINE and OPENMAPS_CANDIDATE to retained and routing snapshot paths")
	}
	return base, next
}
func TestNewportRouting(t *testing.T) {
	_, next := regionalPaths(t)
	ctx := context.Background()
	db, e := sql.Open("sqlite", "file:"+next+"?mode=ro")
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	var raw []byte
	if e = db.QueryRow("SELECT data FROM routing_graph WHERE id=1").Scan(&raw); e != nil {
		t.Fatal(e)
	}
	var data routing.Data
	if e = json.Unmarshal(raw, &data); e != nil {
		t.Fatal(e)
	}
	raw = nil
	s, e := routing.New(data)
	if e != nil {
		t.Fatal(e)
	}
	segments := map[string]routing.Segment{}
	nodes := map[int64]routing.Point{}
	sources := map[int64]struct {
		Tags  map[string]string
		Nodes []int64
	}{}
	for _, v := range data.Nodes {
		nodes[v.ID] = v.Point
	}
	for _, v := range data.Segments {
		segments[v.ID] = v
	}
	for _, v := range data.Sources {
		if v.Kind == "way" {
			var way struct {
				Tags  map[string]string
				Nodes []int64
			}
			if e = json.Unmarshal(v.Raw, &way); e != nil {
				t.Fatal(e)
			}
			sources[v.ID] = way
		}
	}
	var cases []struct {
		Name                string
		Origin, Destination routing.Point
		Outcome             string
		Min                 float64 `json:"min_meters"`
		Max                 float64 `json:"max_meters"`
		MaxSnap             float64 `json:"max_snap_meters"`
		RequiredWays        []int64 `json:"required_ways"`
		ForbidDestination   bool    `json:"forbid_destination"`
		FartherOrigin       bool    `json:"farther_origin"`
		EvidenceNote        string  `json:"evidence_note"`
		Evidence            []struct {
			Way  int64
			Tags map[string]string
		}
		Outside bool `json:"outside_preview"`
	}
	raw, e = os.ReadFile("testdata/newport.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &cases); e != nil {
		t.Fatal(e)
	}
	observe := os.Getenv("OPENMAPS_ROUTING_OBSERVE") == "1"
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			start := time.Now()
			r, e := s.Route(ctx, tc.Origin, tc.Destination)
			if tc.EvidenceNote == "" {
				t.Fatal("missing independent evidence")
			}
			for _, ev := range tc.Evidence {
				for k, want := range ev.Tags {
					if sources[ev.Way].Tags[k] != want {
						t.Fatalf("source evidence changed: way %d %s", ev.Way, k)
					}
				}
			}
			actual := "routed"
			if e != nil {
				var re *routing.Error
				if !errors.As(e, &re) {
					t.Fatal(e)
				}
				actual = re.Outcome
			}
			t.Logf("RESULT %s: %s; %.3f m; snaps %.3f/%.3f m; %s", tc.Name, actual, r.Distance, r.Origin.Distance, r.Destination.Distance, r.Origin.SelectionReason)
			if !observe && actual != tc.Outcome {
				t.Fatalf("expected %s from source evidence: %s; got %v", tc.Outcome, tc.EvidenceNote, e)
			}
			if e != nil {
				return
			}
			if !observe {
				if r.Distance < tc.Min || tc.Max > 0 && r.Distance > tc.Max {
					t.Fatalf("distance %.3f outside source-derived bounds %.1f..%.1f: %s", r.Distance, tc.Min, tc.Max, tc.EvidenceNote)
				}
				if tc.MaxSnap > 0 && (r.Origin.Distance > tc.MaxSnap || r.Destination.Distance > tc.MaxSnap) {
					t.Fatal("on-road source point moved", r.Origin, r.Destination)
				}
				if tc.FartherOrigin && (r.Origin.SelectionReason == "" || r.Origin.Distance <= r.Origin.NearestDistance) {
					t.Fatal("missing bounded farther-candidate explanation", r.Origin)
				}
				used := map[int64]bool{}
				for _, id := range r.Segments {
					seg := segments[id]
					used[seg.Way] = true
					if tc.ForbidDestination && (seg.DestinationForward || seg.DestinationBackward) {
						t.Fatal("destination through shortcut", id)
					}
				}
				for _, way := range tc.RequiredWays {
					if !used[way] {
						t.Fatalf("source-required way %d missing", way)
					}
				}
			}
			outside := false
			var sum float64
			path := []routing.EdgeRef{}
			names := []string{}
			if len(r.Geometry) != len(r.Segments)+1 {
				t.Fatal("geometry/segment mismatch")
			}
			for i, id := range r.Segments {
				seg, ok := segments[id]
				if !ok {
					t.Fatal("unknown segment", id)
				}
				a, b := r.Geometry[i], r.Geometry[i+1]
				from, to := nodes[seg.From], nodes[seg.To]
				length := routing.Distance(from, to)
				onSegment := func(p routing.Point) bool {
					return math.Abs(routing.Distance(from, p)+routing.Distance(p, to)-length) < .05
				}
				if !onSegment(a) || !onSegment(b) {
					t.Fatal("geometry left source segment", id)
				}
				reverse := routing.Distance(from, a) > routing.Distance(from, b)
				if reverse && !seg.Backward || !reverse && !seg.Forward {
					t.Fatal("one-way violated", id)
				}
				path = append(path, routing.EdgeRef{Segment: id, Reverse: reverse})
				sum += routing.Distance(a, b)
				way := sources[seg.Way]
				found := false
				for j := 1; j < len(way.Nodes); j++ {
					if way.Nodes[j-1] == seg.From && way.Nodes[j] == seg.To {
						found = true
						break
					}
				}
				if !found {
					t.Fatal("segment not adjacent source nodes", id)
				}
				name := way.Tags["name"]
				if name == "" {
					name = "(unnamed)"
				}
				if len(names) == 0 || names[len(names)-1] != name {
					names = append(names, name)
				}
			}
			if math.Abs(sum-r.Distance) > .1 {
				t.Fatal("distance/geometry differs", sum, r.Distance)
			}
			for _, p := range r.Geometry {
				outside = outside || p[0] < -71.33 || p[0] > -71.29 || p[1] < 41.47 || p[1] > 41.51
			}
			if !observe && outside != tc.Outside {
				t.Fatal("boundary detour changed")
			}
			for _, ban := range data.Bans {
				for i := 0; i+len(ban.Path) <= len(path); i++ {
					if reflect.DeepEqual(path[i:i+len(ban.Path)], ban.Path) {
						t.Fatal("prohibited maneuver", ban.Relation)
					}
				}
			}
			wp := func(p routing.Point) any {
				return map[string]any{"location": map[string]any{"latLng": map[string]float64{"latitude": p[1], "longitude": p[0]}}}
			}
			body, _ := json.Marshal(map[string]any{"origin": wp(tc.Origin), "destination": wp(tc.Destination), "polylineEncoding": "GEO_JSON_LINESTRING"})
			request := httptest.NewRequest("POST", "/directions/v2:computeRoutes", strings.NewReader(string(body)))
			request.Header.Set("X-Goog-FieldMask", "routes.distanceMeters,routes.polyline")
			response := httptest.NewRecorder()
			api.Handler{Routing: s}.ServeHTTP(response, request)
			if response.Code != 200 {
				t.Fatal(response.Body.String())
			}
			t.Logf("%d m; snaps %.2f/%.2f m; outside preview=%v; %v including HTTP; streets %s", int(math.Round(r.Distance)), r.Origin.Distance, r.Destination.Distance, outside, time.Since(start), strings.Join(names, " → "))
		})
	}
	// Source findings that must not disappear silently on a new graph build.
	via := map[int64]int{}
	for _, b := range data.Bans {
		if b.Relation == 20445534 || b.Relation == 20750731 {
			via[b.Relation] = len(b.Path)
		}
	}
	if via[20445534] != 3 || via[20750731] != 9 {
		t.Fatal("retained Newport via restrictions missing", via)
	}
}
func TestNewportRoutingSnapshotCycle(t *testing.T) {
	base, next := regionalPaths(t)
	ctx := context.Background()
	dir := t.TempDir()
	state := filepath.Join(dir, "isolated-deployment.json")
	reportPath, reviewPath := filepath.Join(dir, "report.json"), filepath.Join(dir, "review.json")
	var checks []importer.QueryCheck
	raw, e := os.ReadFile("../../imports/newport.queries.json")
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(raw, &checks); e != nil {
		t.Fatal(e)
	}
	report, e := importer.Compare(ctx, base, next, checks)
	if e != nil {
		t.Fatal(e)
	}
	if len(report.Violations) != 0 || len(report.Added)+len(report.Removed)+len(report.Changed) != 0 || report.ContinuingIDs != 11602 || report.CandidateRouting == nil {
		t.Fatal("unexpected comparison")
	}
	if e = importer.WriteJSON(reportPath, report); e != nil {
		t.Fatal(e)
	}
	sum, e := importer.Checksum(reportPath)
	if e != nil {
		t.Fatal(e)
	}
	if e = importer.WriteJSON(reviewPath, dataset.Review{ReportSHA256: sum, Reviewer: "integration fixture", Reason: "isolated local routing cycle; no active deployment changes"}); e != nil {
		t.Fatal(e)
	}
	if e = dataset.Init(ctx, state, base); e != nil {
		t.Fatal(e)
	}
	live, e := dataset.Open(ctx, state)
	if e != nil {
		t.Fatal(e)
	}
	defer live.Close()
	check := func(available bool, profile string) {
		w := httptest.NewRecorder()
		live.ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
		var h struct {
			Routing bool `json:"routing_available"`
		}
		json.Unmarshal(w.Body.Bytes(), &h)
		if w.Code != 200 || h.Routing != available {
			t.Fatal(w.Body.String())
		}
		if available {
			req := httptest.NewRequest("POST", "/directions/v2:computeRoutes", strings.NewReader(`{"origin":{"location":{"latLng":{"latitude":41.49138952,"longitude":-71.31373108}}},"destination":{"location":{"latLng":{"latitude":41.48654393,"longitude":-71.30830418}}},"polylineEncoding":"GEO_JSON_LINESTRING"}`))
			req.Header.Set("X-Goog-FieldMask", "routes.distanceMeters")
			rw := httptest.NewRecorder()
			live.ServeHTTP(rw, req)
			if rw.Code != 200 || !strings.Contains(rw.Body.String(), profile) {
				t.Fatal("wrong loaded routing profile", rw.Body.String())
			}
		}
		ar := httptest.NewRequest("POST", "/directions/v2:computeRoutes", strings.NewReader(`{"origin":{"address":"26 Marlborough Street"},"destination":{"address":"1 Resolute Road"},"polylineEncoding":"GEO_JSON_LINESTRING"}`))
		ar.Header.Set("X-Goog-FieldMask", "routes.distanceMeters")
		aw := httptest.NewRecorder()
		live.ServeHTTP(aw, ar)
		if profile == "driving-distance-v3" {
			if aw.Code != 200 || !strings.Contains(aw.Body.String(), "destination_address_street") {
				t.Fatal("address association not loaded", aw.Body.String())
			}
		} else if aw.Code != 503 {
			t.Fatal("old snapshot accepted address associations", aw.Body.String())
		}
		p := httptest.NewRequest("POST", "/v1/places:autocomplete", strings.NewReader(`{"input":"White Horse"}`))
		w = httptest.NewRecorder()
		live.ServeHTTP(w, p)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "om_a5e3dc7692e4d3b90b71b94fba66ec5b") {
			t.Fatal("place regression")
		}
		w = httptest.NewRecorder()
		live.ServeHTTP(w, httptest.NewRequest("GET", "/maps/api/geocode/json?address=364%20Bellevue%20Avenue", nil))
		if !strings.Contains(w.Body.String(), `"candidate_count":8`) {
			t.Fatal("ambiguity regression", w.Body.String())
		}
	}
	baseProfile := ""
	if report.BaselineRouting != nil {
		baseProfile = report.BaselineRouting.Metadata.Profile
	}
	check(report.BaselineRouting != nil, baseProfile)
	if e = dataset.Activate(ctx, state, next, reportPath, reviewPath); e != nil {
		t.Fatal(e)
	}
	check(true, report.CandidateRouting.Metadata.Profile)
	if e = dataset.Rollback(ctx, state); e != nil {
		t.Fatal(e)
	}
	check(report.BaselineRouting != nil, baseProfile)
	t.Log("isolated baseline → routing candidate → baseline (including loaded profile): unchanged Places IDs and eight ambiguous Bellevue addresses; real deployment untouched")
}
