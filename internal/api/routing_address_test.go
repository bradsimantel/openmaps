package api_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"openmaps/internal/api"
	"openmaps/internal/importer"
	"openmaps/internal/routing"
)

func addressRouteRequest(h api.Handler, origin, destination string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/directions/v2:computeRoutes", strings.NewReader(`{"origin":`+origin+`,"destination":`+destination+`,"polylineEncoding":"GEO_JSON_LINESTRING"}`))
	req.Header.Set("X-Goog-FieldMask", "routes.distanceMeters")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}
func addressRoutingHandler(t *testing.T, edits ...func(*importer.Bundle)) api.Handler {
	h := geocodingHandler(t, edits...)
	s, err := routing.New(routing.Data{Metadata: routing.Metadata{Version: 3, Profile: "driving-distance-v3", EndpointBounds: [4]float64{-71.33, 41.47, -71.29, 41.51}}, Nodes: []routing.Node{{ID: 1, Point: routing.Point{-71.311, 41.4901}}, {ID: 2, Point: routing.Point{-71.309, 41.4901}}}, Segments: []routing.Segment{{ID: "1:0", Way: 1, From: 1, To: 2, Forward: true, Backward: true, Snap: true}}, Access: routing.AccessData{Ways: []routing.AccessWay{{Way: 1, Name: "Marlborough Street", Nodes: []int64{1, 2}, Geometry: []routing.Point{{-71.311, 41.4901}, {-71.309, 41.4901}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	h.Routing = s
	return h
}
func TestAddressRouteCombinations(t *testing.T) {
	h := addressRoutingHandler(t)
	a := `{"address":"26 Marlborough St"}`
	c := `{"location":{"latLng":{"latitude":41.4901,"longitude":-71.3095}}}`
	for _, pair := range [][2]string{{a, a}, {a, c}, {c, a}, {c, c}} {
		w := addressRouteRequest(h, pair[0], pair[1])
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		var b struct {
			Openmaps struct {
				Origin, Destination struct {
					Resolved *struct {
						ID     string
						Source routing.Point `json:"source_coordinate"`
					} `json:"resolved_address"`
					Point  routing.Point
					Gap    float64 `json:"off_road_gap_meters"`
					Method string  `json:"selection_method"`
				}
			}
		}
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatal(err)
		}
		if (b.Openmaps.Origin.Resolved != nil) != (pair[0] == a) || (b.Openmaps.Destination.Resolved != nil) != (pair[1] == a) {
			t.Fatal(w.Body.String())
		}
		if pair[0] == a {
			g, err := h.Geocoding.Forward(context.Background(), "26 Marlborough St")
			if err != nil {
				t.Fatal(err)
			}
			if b.Openmaps.Origin.Resolved.ID != g.Results[0].Entity.ID || b.Openmaps.Origin.Resolved.Source != (routing.Point{-71.31, 41.49}) || b.Openmaps.Origin.Gap < 11 || b.Openmaps.Origin.Method != "address_street" {
				t.Fatal(w.Body.String())
			}
		}
	}
}
func TestAddressRouteFailures(t *testing.T) {
	h := addressRoutingHandler(t)
	c := `{"location":{"latLng":{"latitude":41.4901,"longitude":-71.3095}}}`
	for _, tc := range []struct{ input, outcome string }{{`{"address":"26 Marlborough St Apt 2"}`, "unsupported_input"}, {`{"address":"99999 Marlborough Street"}`, "address_resolution_failed"}, {`{"address":"Marlborough Street"}`, "unsupported_input"}, {`{"address":"26 Marlborough Street, Boston"}`, "address_resolution_failed"}, {`{"address":"26 Marlborough Street","location":{}}`, "unsupported_input"}, {`{"placeId":"om_any"}`, "unsupported_input"}, {`{"address":null}`, "unsupported_input"}} {
		w := addressRouteRequest(h, c, tc.input)
		if w.Code != 400 || !strings.Contains(w.Body.String(), tc.outcome) {
			t.Fatal(w.Body.String())
		}
	}
	h = addressRoutingHandler(t, func(b *importer.Bundle) {
		for _, r := range b.Records {
			if r.Kind == "address" && r.SourceID == "26" {
				r.SourceID = "duplicate"
				r.Attributes = map[string]json.RawMessage{}
				r.Attributes["name"] = json.RawMessage(`"26 Marlborough Street"`)
				r.Attributes["address"] = json.RawMessage(`"26 Marlborough Street, RI, 02840, US"`)
				r.Attributes["location"] = json.RawMessage(`{"lat":41.4901,"lng":-71.31}`)
				b.Records = append(b.Records, r)
				break
			}
		}
	})
	w := addressRouteRequest(h, c, `{"address":"26 Marlborough Street"}`)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"reason":"ambiguous"`) || !strings.Contains(w.Body.String(), `"candidate_count":2`) {
		t.Fatal(w.Body.String())
	}
	// Standalone geocoding continues to expose both distinct identities.
	g, err := h.Geocoding.Forward(context.Background(), "26 Marlborough Street")
	if err != nil || len(g.Results) != 2 {
		t.Fatal(g, err)
	}
}

func TestAddressRouteUnreachableAndAssociationMetadata(t *testing.T) {
	h := addressRoutingHandler(t)
	// A distinct coordinate component makes this a real mixed-input no-route,
	// after both endpoints have been selected independently.
	d := routing.Data{Metadata: routing.Metadata{Version: 3, Profile: "driving-distance-v3", EndpointBounds: [4]float64{-71.33, 41.47, -71.29, 41.51}}, Nodes: []routing.Node{{ID: 1, Point: routing.Point{-71.311, 41.4901}}, {ID: 2, Point: routing.Point{-71.309, 41.4901}}, {ID: 3, Point: routing.Point{-71.305, 41.4901}}, {ID: 4, Point: routing.Point{-71.303, 41.4901}}}, Segments: []routing.Segment{{ID: "a", Way: 1, From: 1, To: 2, Forward: true, Backward: true, Snap: true}, {ID: "b", Way: 2, From: 3, To: 4, Forward: true, Backward: true, Snap: true}}}
	var err error
	h.Routing, err = routing.New(d)
	if err != nil {
		t.Fatal(err)
	}
	a := `{"address":"26 Marlborough Street"}`
	b := `{"location":{"latLng":{"latitude":41.4901,"longitude":-71.304}}}`
	w := addressRouteRequest(h, a, b)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"outcome":"unreachable"`) || !strings.Contains(w.Body.String(), `"resolved_address"`) || !strings.Contains(w.Body.String(), `"routes":[]`) {
		t.Fatal(w.Body.String())
	}
	d.Guards = []routing.Guard{{Segment: "private", Way: 9, From: routing.Point{-71.311, 41.49002}, To: routing.Point{-71.309, 41.49002}}}
	h.Routing, err = routing.New(d)
	if err != nil {
		t.Fatal(err)
	}
	w = addressRouteRequest(h, a, b)
	if w.Code != 400 || !strings.Contains(w.Body.String(), `"outcome":"endpoint_association_failed"`) || !strings.Contains(w.Body.String(), `"source_coordinate":[-71.31,41.49]`) || strings.Contains(w.Body.String(), `"point":[0,0]`) {
		t.Fatal(w.Body.String())
	}
}
