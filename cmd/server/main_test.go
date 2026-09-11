package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"openmaps/internal/importer"
	"openmaps/internal/placesgeocoding/snapshots"
	"openmaps/internal/routing"
)

func routingLookupBundle(t *testing.T) importer.Bundle {
	t.Helper()
	raw, err := os.ReadFile("../../internal/importer/testdata/small.json")
	if err != nil {
		t.Fatal(err)
	}
	var bundle importer.Bundle
	if err = json.Unmarshal(raw, &bundle); err != nil {
		t.Fatal(err)
	}
	bundle.Manifest = json.RawMessage(`{"region":"synthetic-routing","bbox":[8.7,53.0,8.8,53.2],"release":"fixture-v1"}`)
	for index, location := range map[int]map[string]float64{
		0: {"lat": 53.08, "lng": 8.7495},
		1: {"lat": 53.08, "lng": 8.7515},
		6: {"lat": 53.09, "lng": 8.7495},
	} {
		bundle.Records[index].Attributes["location"], _ = json.Marshal(location)
	}
	return bundle
}

func serviceFixture(t *testing.T) (configuration, string) {
	t.Helper()
	root := t.TempDir()
	bundle := routingLookupBundle(t)
	db := filepath.Join(root, "lookup.sqlite")
	if e := importer.Build(context.Background(), db, bundle); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile("../../internal/routing/testdata/scout/lock.json")
	if e != nil {
		t.Fatal(e)
	}
	var lock routing.ScoutLock
	json.Unmarshal(raw, &lock)
	dirs := []string{filepath.Join(root, "first"), filepath.Join(root, "second")}
	for i, out := range dirs {
		budget := routing.ScoutBudgets{CompressedBytes: 1 << 20, ExpandedBytes: int64(i+1) << 20, ReserveBytes: 32 << 30}
		if e = routing.PrepareScoutPackages(context.Background(), "../../internal/routing/testdata/scout", out, lock, budget); e != nil {
			t.Fatal(e)
		}
		if e = routing.PrepareScoutTurns(context.Background(), out); e != nil {
			t.Fatal(e)
		}
		if e = routing.PrepareScoutPotential(context.Background(), out); e != nil {
			t.Fatal(e)
		}
	}
	tiles := filepath.Join(root, "map.pmtiles")
	os.WriteFile(tiles, []byte("0123456789"), 0600)
	return configuration{db: db, routingSnapshot: dirs[0], routingConcurrency: 2, routingCache: 1, tiles: tiles, public: "../../public", routingSelection: filepath.Join(root, "selection.json")}, dirs[1]
}
func TestUnifiedServiceLookupTilesAndRoutingReplacement(t *testing.T) {
	c, second := serviceFixture(t)
	wd, e := os.Getwd()
	if e != nil {
		t.Fatal(e)
	}
	c.db, e = filepath.Rel(wd, c.db)
	if e != nil {
		t.Fatal(e)
	}
	h, closeHandler, e := newService(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	defer closeHandler()
	call := func(method, path, body, mask string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if mask != "" {
			r.Header.Set("X-Goog-FieldMask", mask)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("POST", "/v1/places:autocomplete", `{"input":"White"}`, ""); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	r := httptest.NewRequest("GET", "/tiles/newport.pmtiles", nil)
	r.Header.Set("Range", "bytes=2-5")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "2345" {
		t.Fatal(w.Code, w.Body.String())
	}
	body := `{"origin":{"location":{"latLng":{"longitude":8.7495,"latitude":53.08}}},"destination":{"location":{"latLng":{"longitude":8.7515,"latitude":53.08}}},"polylineEncoding":"GEO_JSON_LINESTRING"}`
	w = call("POST", "/directions/v2:computeRoutes", body, "routes.distanceMeters,routes.duration,routes.polyline")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct{ Openmaps struct{ Snapshot string } }
	json.Unmarshal(w.Body.Bytes(), &response)
	first := response.Openmaps.Snapshot
	var coordinateResponse map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &coordinateResponse); e != nil {
		t.Fatal(e)
	}
	coordinateRoutes, _ := json.Marshal(coordinateResponse["routes"])
	wantCoordinateRoutes := `[{"distanceMeters":309,"duration":"37s","polyline":{"geoJsonLinestring":{"coordinates":[[8.7495,53.08],[8.7502,53.08],[8.750499999999999,53.080999999999996],[8.751,53.08],[8.7515,53.08]],"type":"LineString"}}}]`
	coordinateMeta := coordinateResponse["openmaps"].(map[string]any)
	if string(coordinateRoutes) != wantCoordinateRoutes || coordinateMeta["origin_resolution"] != nil || coordinateMeta["destination_resolution"] != nil {
		t.Fatal("coordinate route compatibility changed", string(coordinateRoutes), coordinateMeta)
	}
	lookupID := func(input string) string {
		t.Helper()
		response := call("POST", "/v1/places:autocomplete", `{"input":"`+input+`"}`, "")
		var body struct {
			Suggestions []struct {
				PlacePrediction struct {
					ID string `json:"placeId"`
				} `json:"placePrediction"`
			} `json:"suggestions"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Suggestions) == 0 {
			t.Fatal(response.Code, response.Body.String())
		}
		id := body.Suggestions[0].PlacePrediction.ID
		details := call("GET", "/v1/places/"+id, "", "id,location,types")
		if details.Code != 200 || !strings.Contains(details.Body.String(), id) {
			t.Fatal(details.Code, details.Body.String())
		}
		return id
	}
	businessID := lookupID("White Horse Tavern")
	addressID := lookupID("26 Marlborough St")
	placeBody := `{"origin":{"placeId":"` + businessID + `"},"destination":{"placeId":"` + addressID + `"},"polylineEncoding":"GEO_JSON_LINESTRING"}`
	placeResponse := call("POST", "/directions/v2:computeRoutes", placeBody, "routes.distanceMeters,routes.duration,routes.polyline")
	var placeResult map[string]any
	if placeResponse.Code != 200 || json.Unmarshal(placeResponse.Body.Bytes(), &placeResult) != nil || !reflect.DeepEqual(placeResult["routes"], coordinateResponse["routes"]) {
		t.Fatal(placeResponse.Code, placeResponse.Body.String())
	}
	placeMeta := placeResult["openmaps"].(map[string]any)
	if placeMeta["origin_resolution"].(map[string]any)["entity_kind"] != "business" || placeMeta["destination_resolution"].(map[string]any)["entity_kind"] != "address" {
		t.Fatal("place identities were conflated", placeMeta)
	}
	maskedPlace := call("POST", "/directions/v2:computeRoutes", placeBody, "routes.distanceMeters")
	var maskedResult map[string]any
	if maskedPlace.Code != 200 || json.Unmarshal(maskedPlace.Body.Bytes(), &maskedResult) != nil || len(maskedResult["routes"].([]any)[0].(map[string]any)) != 1 {
		t.Fatal("place route mask leaked fields", maskedPlace.Code, maskedPlace.Body.String())
	}
	geocode := call("GET", "/maps/api/geocode/json?address=26%20Marlborough%20St", "", "")
	if geocode.Code != 200 || !strings.Contains(geocode.Body.String(), addressID) {
		t.Fatal(geocode.Code, geocode.Body.String())
	}
	addressBody := `{"origin":{"placeId":"` + businessID + `"},"destination":{"address":"26 Marlborough St"},"polylineEncoding":"GEO_JSON_LINESTRING"}`
	addressResponse := call("POST", "/directions/v2:computeRoutes", addressBody, "routes.distanceMeters,routes.duration,routes.polyline")
	var addressResult map[string]any
	if addressResponse.Code != 200 || json.Unmarshal(addressResponse.Body.Bytes(), &addressResult) != nil || !reflect.DeepEqual(addressResult["routes"], coordinateResponse["routes"]) {
		t.Fatal(addressResponse.Code, addressResponse.Body.String())
	}
	if addressResult["openmaps"].(map[string]any)["destination_resolution"].(map[string]any)["place_id"] != addressID {
		t.Fatal("route did not preserve exact geocoding identity", addressResponse.Body.String())
	}
	unsnappable := `{"origin":{"placeId":"` + importer.PublicID("fixture:place:cafe") + `"},"destination":{"placeId":"` + addressID + `"},"polylineEncoding":"GEO_JSON_LINESTRING"}`
	if response := call("POST", "/directions/v2:computeRoutes", unsnappable, "routes.duration"); response.Code != 400 || !strings.Contains(response.Body.String(), `"outcome":"unsnappable"`) || !strings.Contains(response.Body.String(), `"endpoint":"origin"`) || !strings.Contains(response.Body.String(), importer.PublicID("fixture:place:cafe")) {
		t.Fatal(response.Code, response.Body.String())
	}
	// Route traffic continues while selection loading and retirement occur.
	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); wg.Wait() }()
	wg.Go(func() {
		for ctx.Err() == nil {
			w := call("POST", "/directions/v2:computeRoutes", body, "routes.distanceMeters")
			if w.Code != 200 {
				t.Error(w.Code, w.Body.String())
				return
			}
			time.Sleep(time.Millisecond)
		}
	})
	wait := func(predicate func(string) bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if predicate(call("GET", "/healthz", "", "").Body.String()) {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("selection did not settle")
	}
	os.WriteFile(c.routingSelection, []byte(`{"directory":"absent"}`), 0600)
	wait(func(b string) bool {
		return strings.Contains(b, `"reload_error":"`) && !strings.Contains(b, `"reload_error":""`)
	})
	w = call("POST", "/directions/v2:computeRoutes", body, "routes.distanceMeters")
	json.Unmarshal(w.Body.Bytes(), &response)
	if response.Openmaps.Snapshot != first {
		t.Fatal("bad selection replaced graph")
	}
	raw, _ := json.Marshal(map[string]string{"directory": second})
	os.WriteFile(c.routingSelection, raw, 0600)
	wait(func(b string) bool { return strings.Contains(b, second) && strings.Contains(b, `"reload_error":""`) })
	w = call("POST", "/directions/v2:computeRoutes", body, "routes.distanceMeters")
	json.Unmarshal(w.Body.Bytes(), &response)
	if w.Code != 200 || response.Openmaps.Snapshot == first {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = call("GET", "/maps/api/geocode/json?address=50%20Bellevue%20Ave", "", ""); w.Code != http.StatusOK {
		t.Fatal(w.Code, w.Body.String())
	}
}

type blockedHTTPWriter struct {
	*httptest.ResponseRecorder
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (w *blockedHTTPWriter) Write(p []byte) (int, error) {
	w.once.Do(func() {
		close(w.entered)
		<-w.release
	})
	return w.ResponseRecorder.Write(p)
}

func TestRouteLookupSnapshotLeaseDuringReplacement(t *testing.T) {
	c, _ := serviceFixture(t)
	c.routingSelection = ""
	base := c.db
	candidate := filepath.Join(filepath.Dir(base), "lookup-next.sqlite")
	bundle := routingLookupBundle(t)
	for index, location := range map[int]map[string]float64{
		0: {"lat": 53.08, "lng": 8.75},
		1: {"lat": 53.08, "lng": 8.751},
	} {
		bundle.Records[index].Attributes["location"], _ = json.Marshal(location)
	}
	if err := importer.Build(context.Background(), candidate, bundle); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(filepath.Dir(base), "deployment.json")
	if err := snapshots.Init(context.Background(), state, base); err != nil {
		t.Fatal(err)
	}
	c.deployment = state
	h, closeHandler, err := newService(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer closeHandler()
	originID := importer.PublicID("fixture:place:tavern")
	destinationID := importer.PublicID("fixture:address:26")
	body := `{"origin":{"placeId":"` + originID + `"},"destination":{"placeId":"` + destinationID + `"},"polylineEncoding":"GEO_JSON_LINESTRING"}`
	request := func() *http.Request {
		r := httptest.NewRequest("POST", "/directions/v2:computeRoutes", strings.NewReader(body))
		r.Header.Set("X-Goog-FieldMask", "routes.distanceMeters")
		return r
	}
	first := &blockedHTTPWriter{ResponseRecorder: httptest.NewRecorder(), entered: make(chan struct{}), release: make(chan struct{})}
	firstDone := make(chan struct{})
	go func() {
		h.ServeHTTP(first, request())
		close(firstDone)
	}()
	<-first.entered
	nextFile, err := snapshots.Describe(candidate)
	if err != nil {
		t.Fatal(err)
	}
	if err = snapshots.Change(state, func(s *snapshots.Selection) error {
		s.Current = nextFile
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	second := httptest.NewRecorder()
	secondDone := make(chan struct{})
	go func() {
		h.ServeHTTP(second, request())
		close(secondDone)
	}()
	select {
	case <-secondDone:
		close(first.release)
		t.Fatal("lookup replacement retired an in-flight route lease")
	case <-time.After(20 * time.Millisecond):
	}
	close(first.release)
	<-firstDone
	<-secondDone
	baseFile, err := snapshots.Describe(base)
	if err != nil {
		t.Fatal(err)
	}
	check := func(response *httptest.ResponseRecorder, file snapshots.Reference, originLng, destinationLng float64) {
		t.Helper()
		var result struct {
			Openmaps struct {
				Origin struct {
					Source routing.Point `json:"source_point"`
				} `json:"origin_resolution"`
				Destination struct {
					Source routing.Point `json:"source_point"`
				} `json:"destination_resolution"`
			} `json:"openmaps"`
		}
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatal(response.Code, response.Body.String())
		}
		if response.Header().Get("X-OpenMaps-Lookup-Snapshot") != file.SHA256 || response.Header().Get("X-OpenMaps-Dataset") != "" || result.Openmaps.Origin.Source[0] != originLng || result.Openmaps.Destination.Source[0] != destinationLng {
			t.Fatalf("mixed lookup snapshot: header=%s body=%s", response.Header().Get("X-OpenMaps-Lookup-Snapshot"), response.Body.String())
		}
	}
	check(first.ResponseRecorder, baseFile, 8.7495, 8.7515)
	check(second, nextFile, 8.75, 8.751)
}

func TestServiceWithoutLookupReturnsUnavailable(t *testing.T) {
	h, closeHandler, e := newService(context.Background(), configuration{routingConcurrency: 1, routingCache: 1, public: "../../public"})
	if e != nil {
		t.Fatal(e)
	}
	defer closeHandler()
	for _, path := range []string{"/v1/places:autocomplete", "/v1/places/om_missing"} {
		verb, body := "GET", ""
		if strings.HasSuffix(path, ":autocomplete") {
			verb, body = "POST", `{"input":"Newport"}`
		}
		r := httptest.NewRequest(verb, path, strings.NewReader(body))
		r.Header.Set("X-Goog-FieldMask", "*")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 503 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	coordinate := `{"origin":{"location":{"latLng":{"longitude":8.7495,"latitude":53.08}}},"destination":{"location":{"latLng":{"longitude":8.7515,"latitude":53.08}}},"polylineEncoding":"GEO_JSON_LINESTRING"}`
	place := `{"origin":{"placeId":"om_missing"},"destination":{"location":{"latLng":{"longitude":8.7515,"latitude":53.08}}},"polylineEncoding":"GEO_JSON_LINESTRING"}`
	for body, outcome := range map[string]string{coordinate: "unavailable", place: "lookup_unavailable"} {
		r := httptest.NewRequest("POST", "/directions/v2:computeRoutes", strings.NewReader(body))
		r.Header.Set("X-Goog-FieldMask", "routes.duration")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 503 || !strings.Contains(w.Body.String(), `"outcome":"`+outcome+`"`) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
