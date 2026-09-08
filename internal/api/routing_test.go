package api

import (
	"encoding/json"
	"net/http/httptest"
	"openmaps/internal/routing"
	"strings"
	"testing"
)

const routeBody = `{"origin":{"location":{"latLng":{"latitude":0,"longitude":0}}},"destination":{"location":{"latLng":{"latitude":0,"longitude":0.004}}},"polylineEncoding":"GEO_JSON_LINESTRING"}`

func routeHandler(t *testing.T) Handler {
	t.Helper()
	s, e := routing.New(routing.Data{Metadata: routing.Metadata{Version: routing.GraphVersion, Profile: routing.Profile, EndpointBounds: [4]float64{-1, -1, 1, 1}, Attribution: "https://www.openstreetmap.org/copyright", Release: "fixture"}, Nodes: []routing.Node{{ID: 1, Point: routing.Point{0, 0}}, {ID: 2, Point: routing.Point{.004, 0}}}, Segments: []routing.Segment{{ID: "1:0", Way: 1, From: 1, To: 2, Forward: true, Snap: true}}})
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
	for _, mask := range []string{"", "*", "routes", "routes.duration", "routes.distanceMeters,routes.staticDuration", "routes.polyline.encodedPolyline"} {
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
