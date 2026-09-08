package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"openmaps/internal/places"
	"openmaps/internal/routing"
)

type routeWaypoint struct {
	point   routing.Point
	address string
}

func parseRouteWaypoint(raw json.RawMessage) (routeWaypoint, error) {
	w, err := exactObject(raw, "address", "location")
	if err != nil || len(w) != 1 {
		return routeWaypoint{}, fmt.Errorf("waypoint requires exactly one address or location")
	}
	if v, ok := w["address"]; ok {
		var a string
		if json.Unmarshal(v, &a) != nil || strings.TrimSpace(a) == "" {
			return routeWaypoint{}, fmt.Errorf("address requires a nonempty string")
		}
		return routeWaypoint{address: a}, nil
	}
	p, err := waypoint(raw)
	return routeWaypoint{point: p}, err
}

type resolvedAddress struct {
	ID           string               `json:"id"`
	Label        string               `json:"formatted_address"`
	Point        routing.Point        `json:"source_coordinate"`
	Partial      bool                 `json:"partial_context"`
	Attributions []places.Attribution `json:"attributions"`
}
type addressFailure struct {
	outcome, reason, message string
	count                    int
}

func (h Handler) resolveRouteWaypoint(ctx context.Context, w routeWaypoint) (routing.Endpoint, *resolvedAddress, *addressFailure, error) {
	if w.address == "" {
		return routing.Endpoint{Point: w.point}, nil, nil, nil
	}
	if h.Geocoding == nil {
		return routing.Endpoint{}, nil, &addressFailure{outcome: "address_routing_unavailable", reason: "geocoder_unavailable", message: "Address index unavailable in this snapshot"}, nil
	}
	r, err := h.Geocoding.Forward(ctx, w.address)
	if err != nil {
		if ctx.Err() != nil {
			return routing.Endpoint{}, nil, nil, ctx.Err()
		}
		return routing.Endpoint{}, nil, &addressFailure{outcome: "unsupported_input", reason: r.Outcome, message: err.Error()}, nil
	}
	if len(r.Results) != 1 {
		return routing.Endpoint{}, nil, &addressFailure{outcome: "address_resolution_failed", reason: r.Outcome, count: len(r.Results), message: "Address does not identify exactly one retained address record; no evidence establishes a preferred identity. No route was calculated."}, nil
	}
	a := r.Results[0]
	p := routing.Point{a.Entity.Location.Lng, a.Entity.Location.Lat}
	endpoint := routing.Endpoint{Point: p, Address: true, StreetWays: map[int64]bool{}, Areas: map[int64]bool{}}
	// Use winning source components where available, otherwise the already exact
	// matched entity label. No request suffix or unit is removed here.
	street := a.Components.Street
	number := a.Components.Number
	if street == "" {
		number, street, _ = strings.Cut(a.Entity.Name, " ")
	}
	evidence := h.Routing.AccessEvidence()
	for _, w := range evidence.Ways {
		if w.Name != "" && places.Normalize(w.Name) == places.Normalize(street) {
			endpoint.StreetWays[w.Way] = true
		}
	}
	for _, area := range evidence.Areas {
		if area.Number != "" && places.Normalize(area.Number) != places.Normalize(number) {
			continue
		}
		if area.Street != "" && places.Normalize(area.Street) != places.Normalize(street) {
			continue
		}
		endpoint.Areas[area.Way] = true
	}
	return endpoint, &resolvedAddress{ID: a.Entity.ID, Label: a.Entity.Address, Point: p, Partial: a.Partial, Attributions: a.Entity.Attributions}, nil, nil
}
func routingInputFailure(w http.ResponseWriter, err error) {
	write(w, 400, object{"error": object{"code": 400, "status": "INVALID_ARGUMENT", "message": err.Error()}, "openmaps": object{"outcome": "unsupported_input"}})
}
func writeAddressFailure(w http.ResponseWriter, role string, f *addressFailure) {
	code, status := 400, "INVALID_ARGUMENT"
	if f.outcome == "address_routing_unavailable" {
		code, status = 503, "UNAVAILABLE"
	}
	write(w, code, object{"error": object{"code": code, "status": status, "message": f.message}, "openmaps": object{"outcome": f.outcome, "endpoint": role, "reason": f.reason, "candidate_count": f.count}})
}
func routeEndpointMetadata(s routing.Snap, a *resolvedAddress) any {
	// Preserve all existing coordinate snap fields while adding source identity.
	if s.Segment == "" {
		m := object{}
		if a != nil {
			m["resolved_address"] = a
		}
		return m
	}
	raw, _ := json.Marshal(s)
	m := object{}
	_ = json.Unmarshal(raw, &m)
	if a != nil {
		m["resolved_address"] = a
	}
	if strings.HasPrefix(s.Method, "mapped_") {
		delete(m, "nearest_distance_meters")
	}
	if s.Segment != "" {
		m["off_road_gap_meters"] = s.Distance
		if s.Method == "" {
			m["selection_method"] = "coordinate_snap"
		}
		if s.Uncertainty == "" {
			m["uncertainty"] = "Property entrance and off-road connection are unverified; the gap is not driving distance or access permission."
		}
	}
	return m
}
