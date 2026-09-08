package dataset

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openmaps/internal/importer"
	"openmaps/internal/routing"
)

func fixture(t *testing.T) (string, string, string, string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	base := filepath.Join(dir, "base.sqlite")
	candidate := filepath.Join(dir, "next.sqlite")
	state := filepath.Join(dir, "deployment.json")
	raw, e := os.ReadFile("../importer/testdata/small.json")
	if e != nil {
		t.Fatal(e)
	}
	var b importer.Bundle
	if e = json.Unmarshal(raw, &b); e != nil {
		t.Fatal(e)
	}
	if e = importer.Build(ctx, base, b); e != nil {
		t.Fatal(e)
	}
	s, e := importer.ReadSnapshot(ctx, base)
	if e != nil {
		t.Fatal(e)
	}
	b.Records[0].Attributes["website"] = json.RawMessage(`"https://example.org/refreshed"`)
	b.Records[1].Attributes["location"] = json.RawMessage(`{"lat":41.4902,"lng":-71.3102}`)
	b, h, e := importer.Reconcile(b, s, nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = importer.Build(ctx, candidate, b); e != nil {
		t.Fatal(e)
	}
	if e = importer.SaveRefreshMetadata(candidate, h, nil, "fixture"); e != nil {
		t.Fatal(e)
	}
	if e = Init(ctx, state, base); e != nil {
		t.Fatal(e)
	}
	report, e := importer.Compare(ctx, base, candidate, []importer.QueryCheck{{Input: "White Horse Tavern", FirstID: importer.PublicID("fixture:place:tavern")}})
	if e != nil {
		t.Fatal(e)
	}
	if len(report.Violations) != 0 {
		t.Fatal(report.Violations)
	}
	reportPath := filepath.Join(dir, "report.json")
	if e = importer.WriteJSON(reportPath, report); e != nil {
		t.Fatal(e)
	}
	sum, e := importer.Checksum(reportPath)
	if e != nil {
		t.Fatal(e)
	}
	if e = importer.WriteJSON(filepath.Join(dir, "review.json"), Review{sum, "fixture reviewer", "reviewed data and uncertainty"}); e != nil {
		t.Fatal(e)
	}
	return base, candidate, state, reportPath
}
func serve(l *Live, path string) (int, string) {
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("X-Goog-FieldMask", "id,websiteUri")
	l.ServeHTTP(w, r)
	return w.Code, w.Body.String()
}
func TestLiveActivationRollbackAndFailedSelection(t *testing.T) {
	base, next, state, report := fixture(t)
	ctx := context.Background()
	l, e := Open(ctx, state)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	id := importer.PublicID("fixture:place:tavern")
	_, before := serve(l, "/v1/places/"+id)
	if !strings.Contains(before, "https://example.org/tavern") {
		t.Fatal(before)
	}
	if e = Activate(ctx, state, next, report, filepath.Join(filepath.Dir(state), "review.json")); e != nil {
		t.Fatal(e)
	}
	_, after := serve(l, "/v1/places/"+id)
	if !strings.Contains(after, "https://example.org/refreshed") || !strings.Contains(after, id) {
		t.Fatal(after)
	}
	if e = Rollback(ctx, state); e != nil {
		t.Fatal(e)
	}
	_, restored := serve(l, "/v1/places/"+id)
	if restored != before {
		t.Fatalf("rollback changed response: %s", restored)
	}
	s, e := Read(state)
	if e != nil {
		t.Fatal(e)
	}
	baseFile, err := Describe(base)
	if err != nil {
		t.Fatal(err)
	}
	if s.Current != baseFile || s.Baseline != baseFile {
		t.Fatal("baseline lost")
	}
	// Invalid selection leaves the old service working and health visibly degraded.
	s.Current.Path = filepath.Join(filepath.Dir(state), "missing.sqlite")
	if e = importer.WriteJSON(state, s); e != nil {
		t.Fatal(e)
	}
	code, health := serve(l, "/healthz")
	if code != 503 || !strings.Contains(health, "degraded") {
		t.Fatal(health)
	}
	_, body := serve(l, "/v1/places/"+id)
	if body != before {
		t.Fatal("failed reload lost working handler")
	}
}
func TestActivationRejectsStaleOrForgedReview(t *testing.T) {
	for _, scenario := range []string{"missing-review", "report-changed", "candidate-changed", "forged-report", "locked", "baseline-changed"} {
		t.Run(scenario, func(t *testing.T) {
			base, next, state, report := fixture(t)
			review := filepath.Join(filepath.Dir(state), "review.json")
			before, _ := os.ReadFile(state)
			switch scenario {
			case "missing-review":
				review += "missing"
			case "report-changed":
				f, e := os.OpenFile(report, os.O_APPEND|os.O_WRONLY, 0600)
				if e != nil {
					t.Fatal(e)
				}
				f.WriteString("\n")
				f.Close()
			case "candidate-changed":
				f, e := os.OpenFile(next, os.O_APPEND|os.O_WRONLY, 0600)
				if e != nil {
					t.Fatal(e)
				}
				f.WriteString("changed")
				f.Close()
			case "forged-report":
				var r importer.Report
				raw, _ := os.ReadFile(report)
				json.Unmarshal(raw, &r)
				r.ContinuingIDs = 0
				importer.WriteJSON(report, r)
				sum, _ := importer.Checksum(report)
				importer.WriteJSON(review, Review{sum, "reviewer", "forged assertion"})
			case "locked":
				os.WriteFile(state+".lock", nil, 0600)
			case "baseline-changed":
				f, e := os.OpenFile(base, os.O_APPEND|os.O_WRONLY, 0600)
				if e != nil {
					t.Fatal(e)
				}
				f.WriteString("changed")
				f.Close()
			}
			if e := Activate(context.Background(), state, next, report, review); e == nil {
				t.Fatal("invalid activation accepted")
			}
			after, _ := os.ReadFile(state)
			if string(before) != string(after) {
				t.Fatal("failed activation changed state")
			}
		})
	}
}
func TestFailedRollbackPreservesSelection(t *testing.T) {
	_, next, state, report := fixture(t)
	ctx := context.Background()
	if e := Rollback(ctx, state); e == nil {
		t.Fatal("rollback without previous accepted")
	}
	if e := Init(ctx, state, next); e == nil {
		t.Fatal("overwrote baseline")
	}
	if e := Activate(ctx, state, next, report, filepath.Join(filepath.Dir(state), "review.json")); e != nil {
		t.Fatal(e)
	}
	s, _ := Read(state)
	os.Rename(s.Previous.Path, s.Previous.Path+".retained")
	before, _ := os.ReadFile(state)
	if e := Rollback(ctx, state); e == nil {
		t.Fatal("missing rollback target accepted")
	}
	after, _ := os.ReadFile(state)
	if string(before) != string(after) {
		t.Fatal("failed rollback changed selection")
	}
}

func TestGeocodingUsesActivatedSnapshotAndRollback(t *testing.T) {
	_, next, state, report := fixture(t)
	ctx := context.Background()
	live, err := Open(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	lookup := func() (string, string) {
		t.Helper()
		w := httptest.NewRecorder()
		live.ServeHTTP(w, httptest.NewRequest("GET", "/maps/api/geocode/json?address=26+Marlborough+Street", nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"OK"`) {
			t.Fatal(w.Code, w.Body.String())
		}
		return w.Body.String(), w.Header().Get("X-OpenMaps-Dataset")
	}
	before, baseHash := lookup()
	if err = Activate(ctx, state, next, report, filepath.Join(filepath.Dir(state), "review.json")); err != nil {
		t.Fatal(err)
	}
	after, nextHash := lookup()
	id := importer.PublicID("fixture:address:26")
	if before == after || baseHash == nextHash || !strings.Contains(before, id) || !strings.Contains(after, id) || !strings.Contains(after, `"lat":41.4902`) {
		t.Fatal("geocoding coordinate update, identity or dataset header changed incorrectly")
	}
	if err = Rollback(ctx, state); err != nil {
		t.Fatal(err)
	}
	restored, restoredHash := lookup()
	if restored != before || restoredHash != baseHash {
		t.Fatal("rollback did not restore geocoding")
	}
}

func TestRoutingAvailabilityAndAtomicRollback(t *testing.T) {
	base, next, state, reportPath := fixture(t)
	ctx := context.Background()
	live, e := Open(ctx, state)
	if e != nil {
		t.Fatal(e)
	}
	defer live.Close()
	body := `{"origin":{"location":{"latLng":{"latitude":41.49,"longitude":-71.31}}},"destination":{"location":{"latLng":{"latitude":41.49,"longitude":-71.30}}},"polylineEncoding":"GEO_JSON_LINESTRING"}`
	route := func() int {
		r := httptest.NewRequest("POST", "/directions/v2:computeRoutes", strings.NewReader(body))
		r.Header.Set("X-Goog-FieldMask", "routes.distanceMeters")
		w := httptest.NewRecorder()
		live.ServeHTTP(w, r)
		return w.Code
	}
	if got := route(); got != 503 {
		t.Fatal("legacy routing should be unavailable", got)
	}
	d := routing.Data{Metadata: routing.Metadata{Version: routing.GraphVersion, Profile: routing.Profile, EndpointBounds: [4]float64{-71.33, 41.47, -71.29, 41.51}, SourceSHA256: strings.Repeat("a", 64), Release: "fixture", URL: "https://example.org/fixture.pbf", Attribution: "synthetic"}, Nodes: []routing.Node{{ID: 1, Point: routing.Point{-71.31, 41.49}}, {ID: 2, Point: routing.Point{-71.30, 41.49}}}, Segments: []routing.Segment{{ID: "1:0", Way: 1, From: 1, To: 2, Forward: true, Backward: true, Snap: true}}}
	d.Sources = []routing.Source{{Kind: "way", ID: 1, Raw: json.RawMessage(`{"id":1}`), Decision: "fixture"}}
	raw, e := json.Marshal(d)
	if e != nil {
		t.Fatal(e)
	}
	db, e := sql.Open("sqlite", next)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("CREATE TABLE routing_graph(id INTEGER PRIMARY KEY,data BLOB,sha256 TEXT)"); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("INSERT INTO routing_graph VALUES(1,?,?)", raw, routing.Digest(raw)); e != nil {
		t.Fatal(e)
	}
	db.Close()
	report, e := importer.Compare(ctx, base, next, []importer.QueryCheck{{Input: "White Horse Tavern", FirstID: importer.PublicID("fixture:place:tavern")}})
	if e != nil {
		t.Fatal(e)
	}
	if report.BaselineRouting != nil || report.CandidateRouting == nil {
		t.Fatal("missing availability comparison")
	}
	if e = importer.WriteJSON(reportPath, report); e != nil {
		t.Fatal(e)
	}
	sum, e := importer.Checksum(reportPath)
	if e != nil {
		t.Fatal(e)
	}
	review := filepath.Join(filepath.Dir(state), "routing-review.json")
	if e = importer.WriteJSON(review, Review{sum, "fixture", "routing review"}); e != nil {
		t.Fatal(e)
	}
	if e = Activate(ctx, state, next, reportPath, review); e != nil {
		t.Fatal(e)
	}
	if got := route(); got != 200 {
		t.Fatal("routing didn't load", got)
	}
	_, health := serve(live, "/healthz")
	if !strings.Contains(health, `"routing_available":true`) {
		t.Fatal(health)
	}
	if e = Rollback(ctx, state); e != nil {
		t.Fatal(e)
	}
	if got := route(); got != 503 {
		t.Fatal("routing survived legacy rollback", got)
	}
	_, health = serve(live, "/healthz")
	if !strings.Contains(health, `"routing_available":false`) {
		t.Fatal(health)
	}
	// A corrupt graph with an intact SQL table must fail selection validation.
	db, e = sql.Open("sqlite", next)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("UPDATE routing_graph SET sha256='bad'"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if e = Rollback(ctx, state); e == nil {
		t.Fatal("accepted corrupt rollback graph")
	}
	if got := route(); got != 503 {
		t.Fatal("corrupt selection changed loaded snapshot", got)
	}
	d.Sources = nil
	raw, e = json.Marshal(d)
	if e != nil {
		t.Fatal(e)
	}
	db, e = sql.Open("sqlite", next)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("UPDATE routing_graph SET data=?,sha256=?", raw, routing.Digest(raw)); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if _, e = importer.ReadSnapshot(ctx, next); e == nil {
		t.Fatal("accepted a segment without source provenance")
	}
}
