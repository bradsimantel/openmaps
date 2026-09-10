package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"openmaps/internal/routing"
)

// exactObject rejects unknown keys and nulls rather than accepting Google's
// materially different waypoint variants or Go's case-insensitive field names.
func exactObject(raw json.RawMessage, keys ...string) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("expected JSON object")
	}
	for k, v := range obj {
		found := false
		for _, key := range keys {
			found = found || k == key
		}
		if !found || string(v) == "null" {
			return nil, fmt.Errorf("unsupported or null field: %s", k)
		}
	}
	return obj, nil
}
func waypoint(raw json.RawMessage) (routing.Point, error) {
	fail := func() (routing.Point, error) {
		return routing.Point{}, fmt.Errorf("waypoint requires location.latLng.latitude and longitude only")
	}
	w, err := exactObject(raw, "location")
	if err != nil {
		return fail()
	}
	l, err := exactObject(w["location"], "latLng")
	if err != nil {
		return fail()
	}
	p, err := exactObject(l["latLng"], "latitude", "longitude")
	if err != nil || len(p) != 2 {
		return fail()
	}
	var result routing.Point
	if json.Unmarshal(p["longitude"], &result[0]) != nil || json.Unmarshal(p["latitude"], &result[1]) != nil || (math.IsNaN(result[0]) || math.IsNaN(result[1]) || math.IsInf(result[0], 0) || math.IsInf(result[1], 0) || result[0] < -180 || result[0] > 180 || result[1] < -90 || result[1] > 90) {
		return fail()
	}
	return result, nil
}

// Walk JSON tokens once so repeated keys cannot silently override coordinates.
func uniqueJSON(d *json.Decoder) error {
	t, e := d.Token()
	if e != nil {
		return e
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	if delim != '{' && delim != '[' {
		return fmt.Errorf("invalid JSON")
	}
	seen := map[string]bool{}
	for d.More() {
		if delim == '{' {
			key, e := d.Token()
			if e != nil {
				return e
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return fmt.Errorf("duplicate JSON field: %v", key)
			}
			seen[s] = true
		}
		if e := uniqueJSON(d); e != nil {
			return e
		}
	}
	_, e = d.Token()
	return e
}
func (h Handler) computeRoute(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		method(w, "POST")
		return
	}
	mask, err := parameters(r, true)
	if err != nil {
		routingInputFailure(w, err)
		return
	}
	// parameters is shared with Places; these options are not routing URL options.
	for key := range r.URL.Query() {
		if key != "key" && key != "fields" && key != "$fields" {
			routingInputFailure(w, fmt.Errorf("unsupported routing query parameter: %s", key))
			return
		}
	}
	supported := []string{"routes.distanceMeters", "routes.duration", "routes.staticDuration", "routes.polyline.geoJsonLinestring"}
	// Broad masks could imply unsupported navigation/traffic fields. Require explicit paths.
	for _, part := range strings.Split(mask, ",") {
		if part = strings.TrimSpace(part); part == "*" || part == "routes" {
			routingInputFailure(w, fmt.Errorf("request explicit supported route fields; wildcard routes masks are unsupported"))
			return
		}
	}
	paths, err := parseMask(mask, supported)
	if err != nil {
		routingInputFailure(w, err)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		routingInputFailure(w, fmt.Errorf("request too large or unreadable"))
		return
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err = uniqueJSON(dec); err != nil {
		routingInputFailure(w, err)
		return
	}
	if _, err = dec.Token(); err != io.EOF {
		routingInputFailure(w, fmt.Errorf("expected exactly one JSON object"))
		return
	}
	fields, err := exactObject(raw, "origin", "destination", "travelMode", "routingPreference", "polylineEncoding", "polylineQuality")
	if err != nil {
		routingInputFailure(w, err)
		return
	}
	for key, value := range map[string]string{"travelMode": "DRIVE", "routingPreference": "TRAFFIC_UNAWARE", "polylineEncoding": "GEO_JSON_LINESTRING", "polylineQuality": "HIGH_QUALITY"} {
		got, exists := fields[key]
		if !exists && key != "polylineEncoding" {
			continue
		}
		var s string
		if !exists || json.Unmarshal(got, &s) != nil || s != value {
			routingInputFailure(w, fmt.Errorf("%s supports only %s%s", key, value, map[bool]string{true: " (required)", false: ""}[key == "polylineEncoding"]))
			return
		}
	}
	origin, err := parseRouteWaypoint(fields["origin"])
	if err != nil {
		routingInputFailure(w, fmt.Errorf("origin: %w", err))
		return
	}
	destination, err := parseRouteWaypoint(fields["destination"])
	if err != nil {
		routingInputFailure(w, fmt.Errorf("destination: %w", err))
		return
	}
	if h.Routing == nil {
		write(w, 503, object{"error": object{"code": 503, "status": "UNAVAILABLE", "message": "Routing snapshot unavailable"}, "openmaps": object{"outcome": "unavailable"}})
		return
	}
	h.computeScoutRoute(w, r, origin, destination, paths)
}
