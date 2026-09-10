package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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

func routingInputFailure(w http.ResponseWriter, err error) {
	write(w, 400, object{"error": object{"code": 400, "status": "INVALID_ARGUMENT", "message": err.Error()}, "openmaps": object{"outcome": "unsupported_input"}})
}
