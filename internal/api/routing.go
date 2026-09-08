package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"

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
	if json.Unmarshal(p["longitude"], &result[0]) != nil || json.Unmarshal(p["latitude"], &result[1]) != nil || !result.Valid() {
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
		invalid(w, err)
		return
	}
	// parameters is shared with Places; these options are not routing URL options.
	for key := range r.URL.Query() {
		if key != "key" && key != "fields" && key != "$fields" {
			invalid(w, fmt.Errorf("unsupported routing query parameter: %s", key))
			return
		}
	}
	supported := []string{"routes.distanceMeters", "routes.polyline.geoJsonLinestring"}
	// Broad masks could imply fabricated duration/traffic fields. Require explicit paths.
	if mask == "*" || mask == "routes" {
		invalid(w, fmt.Errorf("request routes.distanceMeters and/or routes.polyline; wildcard routes masks are unsupported"))
		return
	}
	paths, err := parseMask(mask, supported)
	if err != nil {
		invalid(w, err)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
	if err != nil {
		invalid(w, fmt.Errorf("request too large or unreadable"))
		return
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err = uniqueJSON(dec); err != nil {
		invalid(w, err)
		return
	}
	if _, err = dec.Token(); err != io.EOF {
		invalid(w, fmt.Errorf("expected exactly one JSON object"))
		return
	}
	fields, err := exactObject(raw, "origin", "destination", "travelMode", "routingPreference", "polylineEncoding", "polylineQuality")
	if err != nil {
		invalid(w, err)
		return
	}
	for key, value := range map[string]string{"travelMode": "DRIVE", "routingPreference": "TRAFFIC_UNAWARE", "polylineEncoding": "GEO_JSON_LINESTRING", "polylineQuality": "HIGH_QUALITY"} {
		got, exists := fields[key]
		if !exists && key != "polylineEncoding" {
			continue
		}
		var s string
		if !exists || json.Unmarshal(got, &s) != nil || s != value {
			invalid(w, fmt.Errorf("%s supports only %s%s", key, value, map[bool]string{true: " (required)", false: ""}[key == "polylineEncoding"]))
			return
		}
	}
	origin, err := waypoint(fields["origin"])
	if err != nil {
		invalid(w, fmt.Errorf("origin: %w", err))
		return
	}
	destination, err := waypoint(fields["destination"])
	if err != nil {
		invalid(w, fmt.Errorf("destination: %w", err))
		return
	}
	result, err := h.Routing.Route(r.Context(), origin, destination)
	if err != nil {
		var re *routing.Error
		if errors.As(err, &re) {
			switch re.Outcome {
			case "unavailable":
				failure(w, 503, "UNAVAILABLE", "Routing unavailable in this snapshot")
			case "unreachable":
				write(w, 200, object{"routes": []any{}, "openmaps": object{"outcome": "unreachable", "message": "No driving route connects the snapped endpoints"}})
			default:
				message := fmt.Sprintf("No suitable driving road within 100 metres of the %s", re.Endpoint)
				if re.Outcome == "outside_coverage" {
					message = fmt.Sprintf("The %s is outside the supported Newport endpoint rectangle", re.Endpoint)
				}
				write(w, 400, object{"error": object{"code": 400, "status": "INVALID_ARGUMENT", "message": message}, "openmaps": object{"outcome": re.Outcome, "endpoint": re.Endpoint}})
			}
			return
		}
		failure(w, 500, "INTERNAL", "Routing calculation failed")
		return
	}
	routes := object{"routes": []any{object{"distanceMeters": int(math.Round(result.Distance)), "polyline": object{"geoJsonLinestring": object{"type": "LineString", "coordinates": result.Geometry}}}}}
	response := project(routes, paths).(object)
	response["openmaps"] = object{"outcome": "routed", "profile": routing.Profile, "snap_limit_meters": routing.SnapLimit, "origin": result.Origin, "destination": result.Destination, "attribution": "© OpenStreetMap contributors", "attribution_uri": h.Routing.Metadata().Attribution, "source_release": h.Routing.Metadata().Release}
	write(w, 200, response)
}
