package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"openmaps/internal/importer"
	"openmaps/internal/routing/valhallatiles"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func serviceFixture(t *testing.T) (configuration, string) {
	t.Helper()
	root := t.TempDir()
	raw, e := os.ReadFile("../../internal/importer/testdata/small.json")
	if e != nil {
		t.Fatal(e)
	}
	var bundle importer.Bundle
	json.Unmarshal(raw, &bundle)
	db := filepath.Join(root, "lookup.sqlite")
	if e = importer.Build(context.Background(), db, bundle); e != nil {
		t.Fatal(e)
	}
	raw, e = os.ReadFile("../../internal/routing/valhallatiles/testdata/scout/lock.json")
	if e != nil {
		t.Fatal(e)
	}
	var lock valhallatiles.ScoutLock
	json.Unmarshal(raw, &lock)
	dirs := []string{filepath.Join(root, "first"), filepath.Join(root, "second")}
	for i, out := range dirs {
		budget := valhallatiles.ScoutBudgets{CompressedBytes: 1 << 20, ExpandedBytes: int64(i+1) << 20, ReserveBytes: 32 << 30}
		if e = valhallatiles.PrepareScoutPackages(context.Background(), "../../internal/routing/valhallatiles/testdata/scout", out, lock, budget); e != nil {
			t.Fatal(e)
		}
		if e = valhallatiles.PrepareScoutTurns(context.Background(), out); e != nil {
			t.Fatal(e)
		}
		if e = valhallatiles.PrepareScoutPotential(context.Background(), out); e != nil {
			t.Fatal(e)
		}
	}
	tiles := filepath.Join(root, "map.pmtiles")
	os.WriteFile(tiles, []byte("0123456789"), 0600)
	return configuration{db: db, routing: dirs[0], workers: 2, cache: 1, tiles: tiles, public: "../../public", selection: filepath.Join(root, "selection.json")}, dirs[1]
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
	os.WriteFile(c.selection, []byte(`{"directory":"absent"}`), 0600)
	wait(func(b string) bool {
		return strings.Contains(b, `"reload_error":"`) && !strings.Contains(b, `"reload_error":""`)
	})
	w = call("POST", "/directions/v2:computeRoutes", body, "routes.distanceMeters")
	json.Unmarshal(w.Body.Bytes(), &response)
	if response.Openmaps.Snapshot != first {
		t.Fatal("bad selection replaced graph")
	}
	raw, _ := json.Marshal(map[string]string{"directory": second})
	os.WriteFile(c.selection, raw, 0600)
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

func TestServiceWithoutLookupReturnsUnavailable(t *testing.T) {
	h, closeHandler, e := newService(context.Background(), configuration{workers: 1, cache: 1, public: "../../public"})
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
}
