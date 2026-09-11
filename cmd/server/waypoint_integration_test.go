//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

func TestDownloadedNewportWaypointFlows(t *testing.T) {
	db := os.Getenv("OPENMAPS_LOOKUP_DB")
	prepared := os.Getenv("OPENMAPS_SCOUT_PREPARED")
	if db == "" || prepared == "" {
		t.Skip("set OPENMAPS_LOOKUP_DB and OPENMAPS_SCOUT_PREPARED; no downloads")
	}
	handler, closeHandler, err := newService(context.Background(), configuration{db: db, routingSnapshot: prepared, routingConcurrency: 1, routingCache: 64, public: "../../public"})
	if err != nil {
		t.Fatal(err)
	}
	defer closeHandler()
	server := httptest.NewServer(handler)
	defer server.Close()
	request := func(method, path string, body any, mask string) map[string]any {
		t.Helper()
		var payload io.Reader
		if body != nil {
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			payload = bytes.NewReader(raw)
		}
		req, err := http.NewRequest(method, server.URL+path, payload)
		if err != nil {
			t.Fatal(err)
		}
		if mask != "" {
			req.Header.Set("X-Goog-FieldMask", mask)
		}
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		raw, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		var result map[string]any
		if err = json.Unmarshal(raw, &result); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("%s %s: %d %s", method, path, response.StatusCode, raw)
		}
		return result
	}
	autocomplete := request("POST", "/v1/places:autocomplete", map[string]any{"input": "White Horse Tavern"}, "")
	suggestions := autocomplete["suggestions"].([]any)
	if len(suggestions) == 0 {
		t.Fatal("White Horse Tavern autocomplete returned no result")
	}
	businessID := suggestions[0].(map[string]any)["placePrediction"].(map[string]any)["placeId"].(string)
	business := request("GET", "/v1/places/"+businessID, nil, "id,location,types")
	if business["id"] != businessID || business["types"].([]any)[0] != "establishment" {
		t.Fatal("autocomplete/details business identity changed", business)
	}
	geocode := func(address string) (string, map[string]any) {
		t.Helper()
		result := request("GET", "/maps/api/geocode/json?address="+address, nil, "")
		rows := result["results"].([]any)
		if result["status"] != "OK" || len(rows) != 1 {
			t.Fatalf("address did not resolve uniquely: %s %+v", address, result)
		}
		row := rows[0].(map[string]any)
		return row["place_id"].(string), row["geometry"].(map[string]any)["location"].(map[string]any)
	}
	address26ID, address26Point := geocode("26%20Marlborough%20Street")
	address50ID, address50Point := geocode("50%20Bellevue%20Avenue")
	addressDetails := request("GET", "/v1/places/"+address50ID, nil, "id,location,types")
	if addressDetails["types"].([]any)[0] != "street_address" {
		t.Fatal("standalone address identity changed", addressDetails)
	}
	mask := "routes.distanceMeters,routes.duration,routes.staticDuration,routes.polyline"
	latLng := func(point map[string]any) map[string]any {
		if latitude, ok := point["latitude"]; ok {
			return map[string]any{"latitude": latitude, "longitude": point["longitude"]}
		}
		return map[string]any{"latitude": point["lat"], "longitude": point["lng"]}
	}
	route := func(origin, destination map[string]any) map[string]any {
		return request("POST", "/directions/v2:computeRoutes", map[string]any{"origin": origin, "destination": destination, "polylineEncoding": "GEO_JSON_LINESTRING"}, mask)
	}
	placeRoute := route(map[string]any{"placeId": businessID}, map[string]any{"placeId": address50ID})
	businessPoint := business["location"].(map[string]any)
	placeCoordinateRoute := route(map[string]any{"location": map[string]any{"latLng": latLng(businessPoint)}}, map[string]any{"location": map[string]any{"latLng": latLng(address50Point)}})
	if !reflect.DeepEqual(placeRoute["routes"], placeCoordinateRoute["routes"]) {
		t.Fatal("place ID routing changed the source-coordinate route")
	}
	addressRoute := route(map[string]any{"address": "26 Marlborough Street"}, map[string]any{"address": "50 Bellevue Avenue"})
	coordinateRoute := route(map[string]any{"location": map[string]any{"latLng": latLng(address26Point)}}, map[string]any{"location": map[string]any{"latLng": latLng(address50Point)}})
	if !reflect.DeepEqual(addressRoute["routes"], coordinateRoute["routes"]) {
		t.Fatal("exact address routing changed the source-coordinate route")
	}
	if addressRoute["openmaps"].(map[string]any)["origin_resolution"].(map[string]any)["place_id"] != address26ID {
		t.Fatal("address route did not preserve geocoding identity")
	}
}
