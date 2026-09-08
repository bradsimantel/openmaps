//go:build integration

package routing_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"openmaps/internal/api"
	"openmaps/internal/geocoding"
	"openmaps/internal/routing"
)

// Retained-address benchmark: the source points are never moved to manufacture
// on-road endpoints. Before is an explicitly labelled coordinate-pipeline
// observation: the previous API itself rejected address waypoints.
func TestNewportAddressRouting(t *testing.T) {
	_, next := regionalPaths(t)
	ctx := context.Background()
	raw, err := os.ReadFile("testdata/newport-addresses.json")
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Cases []struct {
			Name, Origin, Destination, Outcome string
			DestinationMethod                  string `json:"destination_method"`
			Outside                            bool   `json:"outside_preview"`
			Note                               string `json:"evidence_note"`
			Evidence                           []struct {
				Way  int64
				Tags map[string]string
			}
		}
		Addresses []struct {
			ID, Label, Number, Street string
			SourceKey                 string `json:"source_key"`
			Point                     routing.Point
		}
	}
	if err = json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+next+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = db.QueryRow("SELECT data FROM routing_graph").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var data routing.Data
	if err = json.Unmarshal(raw, &data); err != nil {
		t.Fatal(err)
	}
	raw = nil
	graph, err := routing.New(data)
	if err != nil {
		t.Fatal(err)
	}
	geocoder, err := geocoding.Open(ctx, next)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[int64]map[string]string{}
	for _, s := range data.Sources {
		if s.Kind == "way" {
			var w struct{ Tags map[string]string }
			if err = json.Unmarshal(s.Raw, &w); err != nil {
				t.Fatal(err)
			}
			sources[s.ID] = w.Tags
		}
	}
	for _, a := range suite.Addresses {
		var name, key, raw string
		var lat, lng float64
		err = db.QueryRow(`SELECT e.name,e.lat,e.lng,s.source_key,s.raw FROM entities e JOIN source_records s ON s.entity_id=e.id WHERE e.id=? AND s.source_key=?`, a.ID, a.SourceKey).Scan(&name, &lat, &lng, &key, &raw)
		if err != nil || name != a.Label || key != a.SourceKey || (routing.Point{lng, lat}) != a.Point {
			t.Fatal("retained identity/source point changed", a.ID, err)
		}
		var source struct {
			Geometry   struct{ Coordinates routing.Point }
			Properties struct{ Number, Street string }
		}
		if err = json.Unmarshal([]byte(raw), &source); err != nil || source.Geometry.Coordinates != a.Point || source.Properties.Number != a.Number || source.Properties.Street != a.Street {
			t.Fatal("source evidence changed", a.ID, err)
		}
	}
	server := httptest.NewServer(api.Handler{Routing: graph, Geocoding: geocoder})
	defer server.Close()
	var before *routing.Store
	if path := os.Getenv("OPENMAPS_ADDRESS_BEFORE"); path != "" {
		before, err = routing.Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
	}
	nodes := map[int64]routing.Point{}
	segments := map[string]routing.Segment{}
	// Interior geometry pairs have exact source nodes. Endpoint partial segments
	// are identified by the response's source segment reference.
	type pair [2]routing.Point
	directed := map[pair][]routing.EdgeRef{}
	for _, n := range data.Nodes {
		nodes[n.ID] = n.Point
	}
	for _, s := range data.Segments {
		segments[s.ID] = s
		if s.Forward {
			directed[pair{nodes[s.From], nodes[s.To]}] = append(directed[pair{nodes[s.From], nodes[s.To]}], routing.EdgeRef{Segment: s.ID})
		}
		if s.Backward {
			directed[pair{nodes[s.To], nodes[s.From]}] = append(directed[pair{nodes[s.To], nodes[s.From]}], routing.EdgeRef{Segment: s.ID, Reverse: true})
		}
	}
	for _, tc := range suite.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			if tc.Note == "" {
				t.Fatal("missing source rationale")
			}
			for _, e := range tc.Evidence {
				for k, v := range e.Tags {
					if sources[e.Way][k] != v {
						t.Fatalf("source way %d %s changed", e.Way, k)
					}
				}
			}
			a, ae := geocoder.Forward(ctx, tc.Origin)
			b, be := geocoder.Forward(ctx, tc.Destination)
			old := "API unsupported address; coordinate observation unavailable"
			if before != nil && ae == nil && be == nil && len(a.Results) == 1 && len(b.Results) == 1 {
				x, y := a.Results[0].Entity.Location, b.Results[0].Entity.Location
				r, e := before.Route(ctx, routing.Point{x.Lng, x.Lat}, routing.Point{y.Lng, y.Lat})
				observation := map[string]any{"outcome": "routed", "road_meters": nil, "origin_gap": nil, "destination_gap": nil}
				if e != nil {
					observation["outcome"] = e.Error()
				} else {
					observation["road_meters"] = r.Distance
				}
				if r.Origin.Segment != "" {
					observation["origin_gap"] = r.Origin.Distance
				}
				if r.Destination.Segment != "" {
					observation["destination_gap"] = r.Destination.Distance
				}
				payload, _ := json.Marshal(observation)
				old = string(payload)
			}
			body, _ := json.Marshal(map[string]any{"origin": map[string]string{"address": tc.Origin}, "destination": map[string]string{"address": tc.Destination}, "polylineEncoding": "GEO_JSON_LINESTRING"})
			req, _ := http.NewRequest("POST", server.URL+"/directions/v2:computeRoutes", strings.NewReader(string(body)))
			req.Header.Set("X-Goog-FieldMask", "routes.distanceMeters,routes.polyline")
			res, err := server.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			bytes, err := io.ReadAll(res.Body)
			res.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			type endpoint struct {
				routing.Snap
				Address *struct {
					ID    string
					Point routing.Point `json:"source_coordinate"`
				} `json:"resolved_address"`
				Gap float64 `json:"off_road_gap_meters"`
			}
			var response struct {
				Routes []struct {
					Distance int `json:"distanceMeters"`
					Polyline struct {
						Line struct{ Coordinates []routing.Point } `json:"geoJsonLinestring"`
					}
				}
				Openmaps struct {
					Outcome             string
					Origin, Destination endpoint
				}
			}
			if err = json.Unmarshal(bytes, &response); err != nil {
				t.Fatal(err)
			}
			m := response.Openmaps
			t.Logf("BEFORE %s; AFTER %s", old, string(bytes))
			if m.Outcome != tc.Outcome {
				t.Fatalf("source expectation %s: %s; got %s", tc.Outcome, tc.Note, bytes)
			}
			if m.Outcome != "routed" {
				if res.StatusCode != 400 || len(response.Routes) != 0 {
					t.Fatal(string(bytes))
				}
				return
			}
			if res.StatusCode != 200 || len(response.Routes) != 1 {
				t.Fatal(string(bytes))
			}
			for i, e := range []endpoint{m.Origin, m.Destination} {
				g := a
				if i == 1 {
					g = b
				}
				if len(g.Results) != 1 || e.Address == nil {
					t.Fatal("missing address identity")
				}
				loc := g.Results[0].Entity.Location
				p := routing.Point{loc.Lng, loc.Lat}
				if e.Address.ID != g.Results[0].Entity.ID || e.Address.Point != p || e.Requested != p {
					t.Fatal("source coordinate moved", e)
				}
				limit := routing.AddressSnapLimit
				if strings.HasPrefix(e.Method, "mapped_") {
					limit = routing.AccessPointLimit
				}
				if e.Distance > limit || math.Abs(routing.Distance(p, e.Point)-e.Distance) > .001 || e.Gap != e.Distance || e.Uncertainty == "" || len(e.Evidence) == 0 {
					t.Fatal("missing displacement/evidence", e)
				}
			}
			if tc.DestinationMethod != "" && m.Destination.Method != tc.DestinationMethod {
				t.Fatal("method violates source evidence", m.Destination)
			}
			points := response.Routes[0].Polyline.Line.Coordinates
			if len(points) < 2 || points[0] != m.Origin.Point || points[len(points)-1] != m.Destination.Point {
				t.Fatal("geometry endpoints differ")
			}
			sum := 0.0
			outside := false
			path := []routing.EdgeRef{}
			for i := 1; i < len(points); i++ {
				x, y := points[i-1], points[i]
				sum += routing.Distance(x, y)
				outside = outside || x[0] < -71.33 || x[0] > -71.29 || x[1] < 41.47 || x[1] > 41.51
				refs := directed[pair{x, y}]
				if len(refs) == 0 {
					for _, id := range []string{m.Origin.Segment, m.Destination.Segment} {
						seg := segments[id]
						from, to := nodes[seg.From], nodes[seg.To]
						length := routing.Distance(from, to)
						if math.Abs(routing.Distance(from, x)+routing.Distance(x, to)-length) < .01 && math.Abs(routing.Distance(from, y)+routing.Distance(y, to)-length) < .01 {
							reverse := routing.Distance(from, x) > routing.Distance(from, y)
							if reverse && seg.Backward || !reverse && seg.Forward {
								refs = append(refs, routing.EdgeRef{Segment: id, Reverse: reverse})
								break
							}
						}
					}
				}
				if len(refs) == 0 {
					t.Fatal("geometry leaves permitted source edges", i)
				}
				ref := refs[0]
				path = append(path, ref)
				seg := segments[ref.Segment]
				if !m.Origin.DestinationAccess && !m.Destination.DestinationAccess && (seg.DestinationForward || seg.DestinationBackward) {
					t.Fatal("destination-only through shortcut")
				}
			}
			if math.Abs(sum-float64(response.Routes[0].Distance)) > .501 || outside != tc.Outside {
				t.Fatal("road distance or boundary-detour invariant", sum, outside)
			}
			for _, ban := range data.Bans {
				for i := 0; i+len(ban.Path) <= len(path); i++ {
					if reflect.DeepEqual(path[i:i+len(ban.Path)], ban.Path) {
						t.Fatal("prohibited turn", ban.Relation)
					}
				}
			}
		})
	}
}
