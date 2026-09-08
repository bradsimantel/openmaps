package api

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"openmaps/internal/routing"
	"strings"
	"testing"
)

const routeBody = `{"origin":{"location":{"latLng":{"latitude":0,"longitude":0}}},"destination":{"location":{"latLng":{"latitude":0,"longitude":0.004}}},"polylineEncoding":"GEO_JSON_LINESTRING"}`

func routeHandler(t *testing.T) Handler {
	t.Helper()
	s, e := routing.New(routing.Data{Costs: []routing.WayCost{{Way: 1, Forward: routing.Speed{KPH: 36, Notes: []string{"fixture"}}, Backward: routing.Speed{KPH: 36, Notes: []string{"fixture"}}}}, Metadata: routing.Metadata{Version: routing.GraphVersion, CostModel: routing.CostModel, Profile: routing.Profile, EndpointBounds: [4]float64{-1, -1, 1, 1}, Attribution: "https://www.openstreetmap.org/copyright", Release: "fixture"}, Nodes: []routing.Node{{ID: 1, Point: routing.Point{0, 0}}, {ID: 2, Point: routing.Point{.004, 0}}}, Segments: []routing.Segment{{ID: "1:0", Way: 1, From: 1, To: 2, Forward: true, Snap: true}}})
	if e != nil {
		t.Fatal(e)
	}
	return Handler{Routing: s}
}
func routeRequest(h Handler, body, mask, path, verb string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(verb, path, strings.NewReader(body))
	if mask != "" {
		r.Header.Set("X-Goog-FieldMask", mask)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestRouteContract(t *testing.T) {
	h := routeHandler(t)
	path := "/directions/v2:computeRoutes"
	w := routeRequest(h, routeBody, "routes.distanceMeters,routes.polyline", path, "POST")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var body map[string]json.RawMessage
	if e := json.Unmarshal(w.Body.Bytes(), &body); e != nil {
		t.Fatal(e)
	}
	var routes []map[string]json.RawMessage
	json.Unmarshal(body["routes"], &routes)
	if len(routes) != 1 || len(routes[0]) != 2 || string(routes[0]["distanceMeters"]) != "445" || strings.Contains(w.Body.String(), "duration") {
		t.Fatal(w.Body.String())
	}
	w = routeRequest(h, routeBody, "routes.distanceMeters", path, "POST")
	if w.Code != 200 || strings.Contains(w.Body.String(), "polyline") {
		t.Fatal(w.Body.String())
	}
	w = routeRequest(h, routeBody, "", path+"?fields=routes.distanceMeters&key=ignored", "POST")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for name, body := range map[string]string{"unknown body": strings.Replace(routeBody, `"polylineEncoding"`, `"trafficModel":"BEST_GUESS","polylineEncoding"`, 1), "duration input": strings.Replace(routeBody, `"origin"`, `"departureTime":"2026-09-07T12:00:00Z","origin"`, 1), "partial coordinate": strings.Replace(routeBody, `"latitude":0,`, "", 1), "null coordinate": strings.Replace(routeBody, `"latitude":0`, `"latitude":null`, 1), "uppercase coordinate": strings.Replace(routeBody, `"latitude"`, `"Latitude"`, 1), "duplicate coordinate": strings.Replace(routeBody, `"latitude":0`, `"latitude":0,"latitude":1`, 1), "missing encoding": strings.Replace(routeBody, `,"polylineEncoding":"GEO_JSON_LINESTRING"`, "", 1), "invalid latitude": strings.Replace(routeBody, `"latitude":0`, `"latitude":91`, 1), "trailing JSON": routeBody + `{}`, "null": "null", "oversize": strings.Repeat(" ", 17000) + routeBody} {
		t.Run(name, func(t *testing.T) {
			w := routeRequest(h, body, "routes.distanceMeters", path, "POST")
			if w.Code != 400 || !strings.Contains(w.Body.String(), "INVALID_ARGUMENT") {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	for _, mask := range []string{"", "*", "routes", "routes,routes.duration", "routes.duration,*", "routes.legs.duration", "routes.polyline.encodedPolyline"} {
		w := routeRequest(h, routeBody, mask, path, "POST")
		if w.Code != 400 {
			t.Fatal(mask, w.Code, w.Body.String())
		}
	}
	for _, query := range []string{"?languageCode=en", "?fields=routes.distanceMeters", "?key=a&key=b", "?extraComputations=TOLLS"} {
		w := routeRequest(h, routeBody, "routes.distanceMeters", path+query, "POST")
		if w.Code != 400 {
			t.Fatal(query, w.Code)
		}
	}
	w = routeRequest(h, routeBody, "routes.distanceMeters", path, "GET")
	if w.Code != 405 || w.Header().Get("Allow") != "POST" {
		t.Fatal(w.Code)
	}
}
func TestRouteFailures(t *testing.T) {
	h := routeHandler(t)
	path := "/directions/v2:computeRoutes"
	for _, tc := range []struct {
		h        Handler
		body     string
		code     int
		contains string
	}{{Handler{}, routeBody, 503, "UNAVAILABLE"}, {h, strings.Replace(routeBody, `"latitude":0`, `"latitude":2`, 1), 400, "outside_coverage"}, {h, strings.Replace(routeBody, `"latitude":0`, `"latitude":0.01`, 1), 400, "unsnappable"}, {h, strings.ReplaceAll(strings.ReplaceAll(routeBody, `"longitude":0}`, `"longitude":0.004}`), `"longitude":0.004}}},"polylineEncoding"`, `"longitude":0}}},"polylineEncoding"`), 200, "unreachable"}} {
		w := routeRequest(tc.h, tc.body, "routes.distanceMeters", path, "POST")
		if w.Code != tc.code || !strings.Contains(w.Body.String(), tc.contains) {
			t.Fatalf("want %d %s: %d %s", tc.code, tc.contains, w.Code, w.Body.String())
		}
	}
}

func TestEstimatedDurationContract(t *testing.T) {
	h := routeHandler(t)
	path := "/directions/v2:computeRoutes"
	for _, mask := range []string{"routes.duration", "routes.staticDuration", "routes.duration,routes.staticDuration,routes.distanceMeters"} {
		w := routeRequest(h, routeBody, mask, path, "POST")
		var body struct {
			Routes   []map[string]any
			Openmaps map[string]any
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if w.Code != 200 || len(body.Routes) != 1 || body.Openmaps["cost_model"] != routing.CostModel {
			t.Fatal(w.Body.String())
		}
		for _, key := range []string{"duration", "staticDuration"} {
			value, exists := body.Routes[0][key]
			if exists != strings.Contains(mask, "routes."+key) || exists && value != "44s" {
				t.Fatal(w.Body.String())
			}
		}
	}
	// Repeated tiny edges must be accumulated before rounding, and source-to-road
	// gaps must never contribute. Zero-length routes return an explicit 0s.
	zero := strings.Replace(routeBody, `"longitude":0.004`, `"longitude":0`, 1)
	w := routeRequest(h, zero, "routes.duration,routes.staticDuration", path, "POST")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"duration":"0s"`) || !strings.Contains(w.Body.String(), `"staticDuration":"0s"`) {
		t.Fatal(w.Body.String())
	}
	for _, field := range []string{`"routingPreference":"TRAFFIC_AWARE"`, `"routingPreference":"TRAFFIC_AWARE_OPTIMAL"`, `"departureTime":"2026-09-08T10:00:00Z"`, `"arrivalTime":"2026-09-08T10:00:00Z"`, `"trafficModel":"BEST_GUESS"`, `"routeModifiers":{"avoidHighways":true}`, `"computeAlternativeRoutes":true`, `"requestedReferenceRoutes":["SHORTER_DISTANCE"]`, `"extraComputations":["TRAFFIC_ON_POLYLINE"]`} {
		body := strings.Replace(routeBody, `"origin":`, field+`,"origin":`, 1)
		w := routeRequest(h, body, "routes.duration", path, "POST")
		if w.Code != 400 || !strings.Contains(w.Body.String(), "INVALID_ARGUMENT") {
			t.Fatal(w.Body.String())
		}
	}
	d := routing.Data{Metadata: routing.Metadata{Version: 3, Profile: "driving-distance-v3", EndpointBounds: [4]float64{-1, -1, 1, 1}}, Nodes: []routing.Node{{ID: 1, Point: routing.Point{0, 0}}, {ID: 2, Point: routing.Point{.004, 0}}}, Segments: []routing.Segment{{ID: "a", Way: 1, From: 1, To: 2, Forward: true, Snap: true}}}
	var err error
	h.Routing, err = routing.New(d)
	if err != nil {
		t.Fatal(err)
	}
	w = routeRequest(h, routeBody, "routes.duration", path, "POST")
	if w.Code != 503 || !strings.Contains(w.Body.String(), "time_estimate_unavailable") {
		t.Fatal(w.Body.String())
	}
	w = routeRequest(h, routeBody, "routes.distanceMeters", path, "POST")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
}

func TestDurationRoundsOnlyAfterAccumulation(t *testing.T) {
	d := routing.Data{Metadata: routing.Metadata{Version: routing.GraphVersion, Profile: routing.Profile, CostModel: routing.CostModel, EndpointBounds: [4]float64{-1, -1, 1, 1}}, Costs: []routing.WayCost{{Way: 1, Forward: routing.Speed{KPH: 36, Notes: []string{"fixture"}}, Backward: routing.Speed{KPH: 36, Notes: []string{"fixture"}}}}}
	for i := 0; i <= 10; i++ {
		d.Nodes = append(d.Nodes, routing.Node{ID: int64(i + 1), Point: routing.Point{float64(i) * .00002, 0}})
		if i > 0 {
			d.Segments = append(d.Segments, routing.Segment{ID: fmt.Sprint(i), Way: 1, From: int64(i), To: int64(i + 1), Forward: true, Snap: true})
		}
	}
	s, err := routing.New(d)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.ReplaceAll(strings.ReplaceAll(routeBody, `"longitude":0.004`, `"longitude":0.0002`), `"latitude":0`, `"latitude":0.0001`)
	w := routeRequest(Handler{Routing: s}, body, "routes.duration,routes.staticDuration,routes.distanceMeters", "/directions/v2:computeRoutes", "POST")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"duration":"2s"`) || !strings.Contains(w.Body.String(), `"distanceMeters":22`) {
		t.Fatal(w.Body.String())
	}
}
