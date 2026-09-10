//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"openmaps/internal/routing/valhallatiles"
	"os"
	"testing"
)

func TestScoutHTTP(t *testing.T) {
	dir := os.Getenv("OPENMAPS_SCOUT_PREPARED")
	if dir == "" {
		t.Skip("set OPENMAPS_SCOUT_PREPARED; no downloads")
	}
	c, err := valhallatiles.OpenCandidate(context.Background(), dir, 1, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	server := httptest.NewServer(RoutingAdmission(Handler{Routing: c}, 1))
	defer server.Close()
	coordinate := `{"origin":{"location":{"latLng":{"longitude":-71.31373108,"latitude":41.49138952}}},"destination":{"location":{"latLng":{"longitude":-71.30830418,"latitude":41.48654393}}},"polylineEncoding":"GEO_JSON_LINESTRING"}`
	for _, tc := range []struct {
		name, body, mask, outcome string
		code                      int
	}{
		{"full", coordinate, "routes.distanceMeters,routes.duration,routes.staticDuration,routes.polyline", "routed", 200},
		{"distance mask", coordinate, "routes.distanceMeters", "routed", 200},
		{"broad mask", coordinate, "routes", "unsupported_input", 400},
		{"address", `{"origin":{"address":"Newport"},"destination":{"address":"Boston"},"polylineEncoding":"GEO_JSON_LINESTRING"}`, "routes.duration", "address_routing_unavailable", 503},
		{"absent graph", `{"origin":{"location":{"latLng":{"longitude":0,"latitude":0}}},"destination":{"location":{"latLng":{"longitude":0,"latitude":0}}},"polylineEncoding":"GEO_JSON_LINESTRING"}`, "routes.duration", "incomplete_data", 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("POST", server.URL+"/directions/v2:computeRoutes", bytes.NewBufferString(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("X-Goog-FieldMask", tc.mask)
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			raw, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.code || body["openmaps"].(map[string]any)["outcome"] != tc.outcome {
				t.Fatalf("wrong response: %d %s", response.StatusCode, raw)
			}
			if tc.outcome == "routed" {
				meta := body["openmaps"].(map[string]any)
				if meta["profile"] != valhallatiles.CandidateProfile || meta["snapshot"] == "" {
					t.Fatal("missing candidate identity")
				}
				route := body["routes"].([]any)[0].(map[string]any)
				if tc.name == "distance mask" {
					if len(route) != 1 {
						t.Fatal("mask leaked fields")
					}
				} else {
					if route["duration"] != route["staticDuration"] || route["polyline"].(map[string]any)["geoJsonLinestring"].(map[string]any)["type"] != "LineString" {
						t.Fatal("bad cost/geometry response")
					}
				}
			}
		})
	}
	// The domain lease includes encoding time and remains one shared limit.
	lease, err := c.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/directions/v2:computeRoutes", bytes.NewBufferString(coordinate))
	request.Header.Set("X-Goog-FieldMask", "routes.duration")
	response := httptest.NewRecorder()
	Handler{Routing: c}.ServeHTTP(response, request)
	lease.Close()
	if response.Code != 429 || response.Header().Get("Retry-After") != "1" {
		t.Fatal("candidate admission did not preserve 429 contract")
	}
}
