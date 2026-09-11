package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"openmaps/internal/places"
	"openmaps/internal/routing"
)

type routeWaypoint struct {
	point   routing.Point
	kind    string
	value   string
	entity  places.Entity
	partial bool
}

func parseRouteWaypoint(raw json.RawMessage) (routeWaypoint, error) {
	w, err := exactObject(raw, "address", "location", "placeId")
	if err != nil || len(w) != 1 {
		return routeWaypoint{}, fmt.Errorf("waypoint requires exactly one of location, placeId or address")
	}
	if v, ok := w["address"]; ok {
		var a string
		if json.Unmarshal(v, &a) != nil || strings.TrimSpace(a) == "" {
			return routeWaypoint{}, fmt.Errorf("address requires a nonempty string")
		}
		return routeWaypoint{kind: "address", value: a}, nil
	}
	if v, ok := w["placeId"]; ok {
		var id string
		if json.Unmarshal(v, &id) != nil || strings.TrimSpace(id) == "" || id != strings.TrimSpace(id) {
			return routeWaypoint{}, fmt.Errorf("placeId requires a nonempty string without surrounding whitespace")
		}
		return routeWaypoint{kind: "place_id", value: id}, nil
	}
	p, err := waypoint(raw)
	return routeWaypoint{point: p, kind: "coordinate"}, err
}

func routingInputFailure(w http.ResponseWriter, err error) {
	write(w, 400, object{"error": object{"code": 400, "status": "INVALID_ARGUMENT", "message": err.Error()}, "openmaps": object{"outcome": "unsupported_input"}})
}

type routeResolutionError struct {
	status, candidateCount int
	code, outcome, message string
}

func (e *routeResolutionError) Error() string { return e.message }

func (h Handler) resolveRouteWaypoint(ctx context.Context, waypoint routeWaypoint) (routeWaypoint, error) {
	switch waypoint.kind {
	case "coordinate":
		return waypoint, nil
	case "place_id":
		if h.Places == nil {
			return routeWaypoint{}, &routeResolutionError{503, 0, "UNAVAILABLE", "lookup_unavailable", "Place lookup data unavailable"}
		}
		entity, err := h.Places.Details(ctx, waypoint.value)
		if errors.Is(err, sql.ErrNoRows) {
			return routeWaypoint{}, &routeResolutionError{400, 0, "INVALID_ARGUMENT", "unknown_place_id", "Place ID was not found in the selected lookup snapshot"}
		}
		if err != nil {
			return routeWaypoint{}, &routeResolutionError{503, 0, "UNAVAILABLE", "lookup_unavailable", "Place lookup data unavailable"}
		}
		waypoint.point = routing.Point{entity.Location.Lng, entity.Location.Lat}
		waypoint.entity = entity
		return waypoint, nil
	case "address":
		if h.Geocoding == nil {
			return routeWaypoint{}, &routeResolutionError{503, 0, "UNAVAILABLE", "lookup_unavailable", "Exact address lookup data unavailable"}
		}
		response, err := h.Geocoding.Forward(ctx, waypoint.value)
		if err != nil {
			if response.Outcome == "invalid_input" || response.Outcome == "unsupported_input" {
				return routeWaypoint{}, &routeResolutionError{400, 0, "INVALID_ARGUMENT", "unsupported_address_syntax", err.Error()}
			}
			return routeWaypoint{}, &routeResolutionError{503, 0, "UNAVAILABLE", "lookup_unavailable", "Exact address lookup data unavailable"}
		}
		if len(response.Results) == 0 {
			return routeWaypoint{}, &routeResolutionError{400, 0, "INVALID_ARGUMENT", "unresolved_address", "Address did not exactly match a supported address in the selected lookup snapshot"}
		}
		if len(response.Results) != 1 {
			return routeWaypoint{}, &routeResolutionError{400, len(response.Results), "INVALID_ARGUMENT", "ambiguous_address", "Address exactly matched multiple distinct address identities; use an Open Maps place ID or coordinates"}
		}
		result := response.Results[0]
		waypoint.point = routing.Point{result.Entity.Location.Lng, result.Entity.Location.Lat}
		waypoint.entity = result.Entity
		waypoint.partial = result.Partial
		return waypoint, nil
	default:
		return routeWaypoint{}, fmt.Errorf("unsupported waypoint form")
	}
}

func routeResolutionFailure(w http.ResponseWriter, endpoint string, err error) {
	var resolution *routeResolutionError
	if !errors.As(err, &resolution) {
		resolution = &routeResolutionError{500, 0, "INTERNAL", "lookup_unavailable", "Waypoint lookup unavailable"}
	}
	meta := object{"outcome": resolution.outcome, "endpoint": endpoint}
	if resolution.candidateCount != 0 {
		meta["candidate_count"] = resolution.candidateCount
	}
	write(w, resolution.status, object{"error": object{"code": resolution.status, "status": resolution.code, "message": resolution.message}, "openmaps": meta})
}

func (w routeWaypoint) resolutionMetadata() object {
	if w.kind == "coordinate" {
		return nil
	}
	precision := "source_entity_point"
	if w.entity.Kind == "address" {
		precision = "source_address_point"
	}
	meta := object{
		"input_type":       w.kind,
		"place_id":         w.entity.ID,
		"entity_kind":      w.entity.Kind,
		"source_point":     w.point,
		"source_precision": precision,
		"uncertainty":      "Source entity point; not a verified entrance or routing access point",
	}
	if w.partial {
		meta["partial_context"] = true
	}
	return meta
}
