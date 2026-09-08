package api

import (
	"bytes"
	"encoding/json"
	"errors"
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
	if !h.Routing.HasDuration() {
		for _, p := range paths {
			if p == "routes.duration" || p == "routes.staticDuration" {
				write(w, 503, object{"error": object{"code": 503, "status": "UNAVAILABLE", "message": "Estimated duration requires a graph v4 candidate; retained graphs still support distance routing"}, "openmaps": object{"outcome": "time_estimate_unavailable"}})
				return
			}
		}
	}
	a, originAddress, af, err := h.resolveRouteWaypoint(r.Context(), origin)
	if err != nil {
		failure(w, 500, "INTERNAL", "Address resolution failed")
		return
	}
	if af != nil {
		writeAddressFailure(w, "origin", af)
		return
	}
	b, destinationAddress, af, err := h.resolveRouteWaypoint(r.Context(), destination)
	if err != nil {
		failure(w, 500, "INTERNAL", "Address resolution failed")
		return
	}
	if af != nil {
		writeAddressFailure(w, "destination", af)
		return
	}
	result, err := h.Routing.RouteEndpoints(r.Context(), a, b)
	originMeta := routeEndpointMetadata(result.Origin, originAddress)
	destinationMeta := routeEndpointMetadata(result.Destination, destinationAddress)
	if err != nil {
		var re *routing.Error
		if errors.As(err, &re) {
			switch re.Outcome {
			case "unavailable", "address_routing_unavailable":
				write(w, 503, object{"error": object{"code": 503, "status": "UNAVAILABLE", "message": "Routing or address association data unavailable in this snapshot; address requests require graph format 3"}, "openmaps": object{"outcome": re.Outcome, "endpoint": re.Endpoint, "origin": originMeta, "destination": destinationMeta}})
			case "unreachable":
				write(w, 200, object{"routes": []any{}, "openmaps": object{"outcome": "unreachable", "message": "No driving route connects these bounded snaps; no safe alternative snap is available. Disconnected roads and restrictions are preserved.", "origin": originMeta, "destination": destinationMeta, "profile": h.Routing.Metadata().Profile}})
			default:
				message := fmt.Sprintf("No suitable driving road within 100 metres of the %s without bypassing restricted road access", re.Endpoint)
				if re.Outcome == "endpoint_association_failed" {
					message = "No acceptable address-to-road association within the documented bounds; access restrictions and competing roads are preserved"
				}
				if re.Outcome == "outside_coverage" {
					message = fmt.Sprintf("The %s is outside the supported Newport endpoint rectangle", re.Endpoint)
				}
				write(w, 400, object{"error": object{"code": 400, "status": "INVALID_ARGUMENT", "message": message}, "openmaps": object{"outcome": re.Outcome, "endpoint": re.Endpoint, "origin": originMeta, "destination": destinationMeta}})
			}
			return
		}
		failure(w, 500, "INTERNAL", "Routing calculation failed")
		return
	}
	route := object{"distanceMeters": int(math.Round(result.Distance)), "polyline": object{"geoJsonLinestring": object{"type": "LineString", "coordinates": result.Geometry}}}
	if h.Routing.HasDuration() {
		// Round once after accumulation, never per edge. Whole seconds avoid
		// presenting an uncalibrated estimate with spurious fractional precision.
		duration := fmt.Sprintf("%.0fs", math.Round(result.Duration))
		route["duration"], route["staticDuration"] = duration, duration
	}
	routes := object{"routes": []any{route}}
	response := project(routes, paths).(object)
	response["openmaps"] = object{"outcome": "routed", "profile": h.Routing.Metadata().Profile, "snap_limit_meters": routing.SnapLimit, "address_snap_limit_meters": routing.AddressSnapLimit, "access_point_limit_meters": routing.AccessPointLimit, "origin": originMeta, "destination": destinationMeta, "attribution": "© OpenStreetMap contributors", "attribution_uri": h.Routing.Metadata().Attribution, "source_release": h.Routing.Metadata().Release}
	if h.Routing.HasDuration() {
		response["openmaps"].(object)["cost_model"] = h.Routing.Metadata().CostModel
		response["openmaps"].(object)["time_estimate_note"] = "Uncalibrated estimated driving time; excludes live/historical traffic and unverified off-road gaps. Conservative speed assumptions and conditional ceilings apply."
	}
	write(w, 200, response)
}
