package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

const routeBody = `{"origin":{"location":{"latLng":{"latitude":0,"longitude":0}}},"destination":{"location":{"latLng":{"latitude":0,"longitude":0.004}}},"polylineEncoding":"GEO_JSON_LINESTRING"}`

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
	h := Handler{}
	path := "/directions/v2:computeRoutes"
	for name, body := range map[string]string{
		"unknown body":         strings.Replace(routeBody, `"polylineEncoding"`, `"trafficModel":"BEST_GUESS","polylineEncoding"`, 1),
		"duration input":       strings.Replace(routeBody, `"origin"`, `"departureTime":"2026-09-07T12:00:00Z","origin"`, 1),
		"partial coordinate":   strings.Replace(routeBody, `"latitude":0,`, "", 1),
		"null coordinate":      strings.Replace(routeBody, `"latitude":0`, `"latitude":null`, 1),
		"uppercase coordinate": strings.Replace(routeBody, `"latitude"`, `"Latitude"`, 1),
		"duplicate coordinate": strings.Replace(routeBody, `"latitude":0`, `"latitude":0,"latitude":1`, 1),
		"multiple forms":       strings.Replace(routeBody, `"origin":{"location":`, `"origin":{"placeId":"om_x","location":`, 1),
		"empty place ID":       strings.Replace(routeBody, `"origin":{"location":{"latLng":{"latitude":0,"longitude":0}}}`, `"origin":{"placeId":""}`, 1),
		"place ID whitespace":  strings.Replace(routeBody, `"origin":{"location":{"latLng":{"latitude":0,"longitude":0}}}`, `"origin":{"placeId":" om_x "}`, 1),
		"null address":         strings.Replace(routeBody, `"origin":{"location":{"latLng":{"latitude":0,"longitude":0}}}`, `"origin":{"address":null}`, 1),
		"missing encoding":     strings.Replace(routeBody, `,"polylineEncoding":"GEO_JSON_LINESTRING"`, "", 1),
		"invalid latitude":     strings.Replace(routeBody, `"latitude":0`, `"latitude":91`, 1),
		"trailing JSON":        routeBody + `{}`,
		"null":                 "null",
		"oversize":             strings.Repeat(" ", 17000) + routeBody,
	} {
		t.Run(name, func(t *testing.T) {
			w := routeRequest(h, body, "routes.distanceMeters", path, "POST")
			if w.Code != 400 || !strings.Contains(w.Body.String(), "INVALID_ARGUMENT") {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	for _, mask := range []string{"", "*", "routes", "routes,routes.duration", "routes.duration,*", "routes.legs.duration", "routes.polyline.encodedPolyline", "geocodingResults.origin.placeId"} {
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
	w := routeRequest(h, routeBody, "routes.distanceMeters", path, "GET")
	if w.Code != 405 || w.Header().Get("Allow") != "POST" {
		t.Fatal(w.Code)
	}
}

func TestRouteSupportedInputWithoutSnapshot(t *testing.T) {
	for _, path := range []string{"/directions/v2:computeRoutes?fields=routes.duration&key=ignored", "/directions/v2:computeRoutes?$fields=routes.polyline"} {
		w := routeRequest(Handler{}, routeBody, "", path, "POST")
		if w.Code != 503 || !strings.Contains(w.Body.String(), `"outcome":"unavailable"`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	for _, field := range []string{`"travelMode":"WALK"`, `"routingPreference":"TRAFFIC_AWARE"`, `"polylineQuality":"OVERVIEW"`, `"arrivalTime":"2026-09-09T10:00:00Z"`, `"routeModifiers":{"avoidHighways":true}`} {
		body := strings.Replace(routeBody, `"origin":`, field+`,"origin":`, 1)
		w := routeRequest(Handler{}, body, "routes.duration", "/directions/v2:computeRoutes", "POST")
		if w.Code != 400 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
