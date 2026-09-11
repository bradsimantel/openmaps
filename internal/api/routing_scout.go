package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"time"

	"openmaps/internal/routing"
)

func (h Handler) computeScoutRoute(w http.ResponseWriter, r *http.Request, origin, destination routeWaypoint, paths []string) {
	lease, err := h.Routing.Acquire()
	if err != nil {
		if errors.Is(err, routing.ErrBusy) {
			w.Header().Set("Retry-After", "1")
			failure(w, 429, "RESOURCE_EXHAUSTED", err.Error())
		} else {
			failure(w, 503, "UNAVAILABLE", err.Error())
		}
		return
	}
	defer lease.Close()
	meta := object{"profile": lease.Metadata.Profile, "search": lease.Metadata.Search, "profile_note": lease.Metadata.Note, "snapshot": lease.Metadata.Snapshot, "snap_limit_meters": 100, "cost_model": "scout-edge-speed-v1", "attribution": "© OpenStreetMap contributors", "attribution_uri": "https://www.openstreetmap.org/copyright", "source_release": "OSM Scout package generation " + lease.Metadata.Timestamp + "; exact OSM cutoff unverified", "time_estimate_note": "Provider encoded edge speed estimates; no traffic or turn penalties; unverified off-road gaps excluded"}
	if resolution := origin.resolutionMetadata(); resolution != nil {
		meta["origin_resolution"] = resolution
	}
	if resolution := destination.resolutionMetadata(); resolution != nil {
		meta["destination_resolution"] = resolution
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	fail := func(err error, endpoint string) {
		status, code, outcome := 500, "INTERNAL", "calculation_failed"
		var missing *routing.MissingTileError
		switch {
		case errors.As(err, &missing):
			status, code, outcome = 503, "UNAVAILABLE", "incomplete_data"
			meta["missing_tile"] = missing.Tile.String()
		case errors.Is(err, routing.ErrUnsnappable):
			status, code, outcome = 400, "INVALID_ARGUMENT", "unsnappable"
		case errors.Is(err, routing.ErrQueryBudget):
			status, code, outcome = 503, "RESOURCE_EXHAUSTED", "query_budget_exhausted"
		case errors.Is(err, context.DeadlineExceeded):
			status, code, outcome = 504, "DEADLINE_EXCEEDED", "query_timeout"
		case errors.Is(err, context.Canceled):
			status, code, outcome = 408, "CANCELLED", "cancelled"
		}
		meta["outcome"] = outcome
		if endpoint != "" {
			meta["endpoint"] = endpoint
		}
		write(w, status, object{"error": object{"code": status, "status": code, "message": err.Error()}, "openmaps": meta})
	}
	a, err := lease.Router.SnapContext(ctx, routing.Point(origin.point))
	if err != nil {
		fail(err, "origin")
		return
	}
	snapMetadata := func(s routing.Snap) object {
		return object{"requested": s.Requested, "point": s.Point, "distance_meters": s.GapMeters, "nearest_distance_meters": s.GapMeters, "off_road_gap_meters": s.GapMeters, "selection_method": "coordinate_snap", "source_segment": lease.Metadata.Snapshot + ":" + s.Edge.String(), "uncertainty": "Nearest eligible retained shape; no excluded-road guards or verified property entrance"}
	}
	meta["origin"] = snapMetadata(a)
	b, err := lease.Router.SnapContext(ctx, routing.Point(destination.point))
	if err != nil {
		fail(err, "destination")
		return
	}
	meta["destination"] = snapMetadata(b)
	result, err := lease.Router.RoutePreparedSnaps(ctx, a, b, 2000000)
	if errors.Is(err, routing.ErrUnreachable) {
		meta["outcome"] = "unreachable"
		meta["message"] = err.Error()
		write(w, 200, object{"routes": []any{}, "openmaps": meta})
		return
	}
	if err != nil {
		fail(err, "")
		return
	}
	duration := fmt.Sprintf("%.0fs", math.Round(result.Seconds))
	route := object{"distanceMeters": int(math.Round(result.Meters)), "duration": duration, "staticDuration": duration, "polyline": object{"geoJsonLinestring": object{"type": "LineString", "coordinates": result.Geometry}}}
	response := project(object{"routes": []any{route}}, paths).(object)
	meta["outcome"] = "routed"
	response["openmaps"] = meta
	write(w, 200, response)
}
