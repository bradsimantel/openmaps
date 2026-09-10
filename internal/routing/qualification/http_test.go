package qualification

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func httpFixture(t *testing.T) (string, Offline, []byte) {
	t.Helper()
	row := Offline{Case: Case{Name: "fixture", From: Point{1, 2}, To: Point{1.001, 2}}, Outcome: "routed", Verified: true}
	row.Route.Meters = 12.5
	row.Route.Seconds = 1.5
	row.Route.Geometry = []Point{{1, 2}, {1.001, 2}}
	b, _ := json.Marshal(row)
	path := filepath.Join(t.TempDir(), "offline.jsonl")
	os.WriteFile(path, append(b, '\n'), 0600)
	raw := []byte(`{"routes":[{"distanceMeters":13,"duration":"2s","staticDuration":"2s","polyline":{"geoJsonLinestring":{"type":"LineString","coordinates":[[1,2],[1.001,2]]}}}],"openmaps":{"outcome":"routed","profile":"osm-scout-public-auto-v1","snapshot":"fixture","attribution":"fixture"}}`)
	return path, row, raw
}
func TestHTTPChecksFullBodyAndCosts(t *testing.T) {
	_, row, raw := httpFixture(t)
	if _, e := CheckResponse(row, 200, raw); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{strings.Replace(string(raw), "1.001", "1.002", 1), strings.Replace(string(raw), `"duration":"2s"`, `"duration":"1s"`, 1), strings.Replace(string(raw), "osm-scout-public-auto-v1", "driving-time-v4", 1)} {
		if _, e := CheckResponse(row, 200, []byte(bad)); e == nil {
			t.Fatal("bad response accepted")
		}
	}
	if _, e := CheckResponse(row, 429, raw); e == nil {
		t.Fatal("admission failure counted as pass")
	}
}
func TestHTTPConcurrentAndImmutableOutput(t *testing.T) {
	path, _, raw := httpFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Goog-FieldMask") != Mask {
			t.Error("mask missing")
		}
		w.Write(raw)
	}))
	defer server.Close()
	out := filepath.Join(t.TempDir(), "report")
	options := HTTPOptions{URL: server.URL, Out: out, Offline: []string{path}, Workers: 4}
	s, e := VerifyHTTP(context.Background(), options)
	if e != nil || s["passed"] != 1 {
		t.Fatal(s, e)
	}
	if _, e = VerifyHTTP(context.Background(), options); e == nil {
		t.Fatal("overwrote report")
	}
}
func TestHTTPRefusesUnverifiedInput(t *testing.T) {
	path, _, _ := httpFixture(t)
	raw, _ := os.ReadFile(path)
	os.WriteFile(path, []byte(strings.Replace(string(raw), `"verified":true`, `"verified":false`, 1)), 0600)
	if _, e := LoadCases([]string{path}); e == nil {
		t.Fatal("unverified input accepted")
	}
}
