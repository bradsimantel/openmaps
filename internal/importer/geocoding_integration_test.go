//go:build integration

package importer

import (
	"context"
	"encoding/json"
	"math"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"openmaps/internal/api"
	"openmaps/internal/geocoding"
	"openmaps/internal/places"
)

// TestNewportGeocoding is opt-in, offline, and checks pinned expectations against
// both retained snapshots and original source records. It is not a census or
// independent measurement of real-world positional accuracy.
func TestNewportGeocoding(t *testing.T) {
	var suite struct {
		Cases []struct {
			Name, Address, Outcome string
			LatLng                 []float64
			IDs                    []string
			Distances              []float64
			Partial                bool
		}
		Evidence []struct {
			ID             string
			SourceKey      string `json:"source_key"`
			Number, Street string
			Location       places.Location
		}
	}
	raw, err := os.ReadFile("../geocoding/testdata/newport.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	snapshots := []Snapshot{}
	for _, env := range []string{"OPENMAPS_BASELINE", "OPENMAPS_CANDIDATE"} {
		path := os.Getenv(env)
		if path == "" {
			t.Fatalf("set absolute %s", env)
		}
		snapshot, err := ReadSnapshot(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		snapshots = append(snapshots, snapshot)
		s, err := geocoding.Open(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		ps, err := places.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer ps.Close()
		// Provider-specific evidence stays with the import adapter. Every expected
		// point is checked against the raw number, street and GeoJSON coordinates.
		for _, e := range suite.Evidence {
			source, ok := snapshot.Sources[e.SourceKey]
			if !ok || source.ID != e.ID {
				t.Fatalf("%s identity continuity lost: %s", env, e.ID)
			}
			var f overtureFeature
			if err = json.Unmarshal(source.Raw, &f.feature); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal(f.Properties, &f.Props); err != nil {
				t.Fatal(err)
			}
			if f.Props.Number != e.Number || f.Props.Street != e.Street || f.Geometry.Coordinates[0] != e.Location.Lng || f.Geometry.Coordinates[1] != e.Location.Lat {
				t.Fatalf("%s source evidence changed: %s", env, e.ID)
			}
			p, err := ps.Details(context.Background(), e.ID)
			if err != nil || p.Kind != "address" || p.Location != e.Location || p.Name != strings.TrimSpace(e.Number+" "+e.Street) {
				t.Fatalf("%s source/entity mismatch: %+v %v", env, p, err)
			}
		}
		for _, tc := range suite.Cases {
			t.Run(env+"/"+tc.Name, func(t *testing.T) {
				query := url.Values{}
				if len(tc.LatLng) > 0 {
					b, _ := json.Marshal(tc.LatLng)
					query.Set("latlng", strings.Trim(string(b), "[]"))
				} else {
					query.Set("address", tc.Address)
				}
				times := []time.Duration{}
				for repeat := 0; repeat < 5; repeat++ {
					w := httptest.NewRecorder()
					start := time.Now()
					api.Handler{Places: ps, Geocoding: s}.ServeHTTP(w, httptest.NewRequest("GET", "/maps/api/geocode/json?"+query.Encode(), nil))
					times = append(times, time.Since(start))
					var body struct {
						Status   string
						Openmaps struct{ Outcome string }
						Results  []struct {
							Components []struct {
								Long  string   `json:"long_name"`
								Short string   `json:"short_name"`
								Types []string `json:"types"`
							} `json:"address_components"`
							ID       string `json:"place_id"`
							Partial  bool   `json:"partial_match"`
							Geometry struct {
								Location places.Location
								Type     string `json:"location_type"`
							}
							Openmaps struct {
								Distance  *float64 `json:"distance_meters"`
								Precision string
							}
						}
					}
					if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
						t.Fatal(err)
					}
					status := "OK"
					if len(tc.IDs) == 0 {
						status = "ZERO_RESULTS"
					}
					if tc.Outcome == "unsupported_input" {
						status = "INVALID_REQUEST"
					}
					if w.Code != 200 || body.Status != status || body.Openmaps.Outcome != tc.Outcome || len(body.Results) != len(tc.IDs) {
						t.Fatal(w.Code, w.Body.String())
					}
					for i, r := range body.Results {
						var source overtureFeature
						found := false
						for key, record := range snapshot.Sources {
							if record.ID == r.ID && strings.HasPrefix(key, "overture:address:") {
								if err := json.Unmarshal(record.Raw, &source.feature); err != nil {
									t.Fatal(err)
								}
								if err := json.Unmarshal(source.Properties, &source.Props); err != nil {
									t.Fatal(err)
								}
								found = true
								break
							}
						}
						if !found {
							t.Fatal("missing component source", r.ID)
						}
						wantParts := map[string]string{"street_number": source.Props.Number, "route": source.Props.Street, "postal_code": source.Props.Postcode, "country": source.Props.Country, "administrative_area_level_1": "RI"}
						if len(r.Components) != len(wantParts) {
							t.Fatalf("missing or invented components: %+v", r.Components)
						}
						for _, c := range r.Components {
							if len(c.Types) == 0 || c.Short == "" || wantParts[c.Types[0]] != c.Short {
								t.Fatalf("incorrect component %+v", c)
							}
							long := c.Short
							if c.Types[0] == "country" {
								long = "United States"
							}
							if c.Types[0] == "administrative_area_level_1" {
								long = "Rhode Island"
							}
							if c.Long != long {
								t.Fatalf("incorrect name %+v", c)
							}
							delete(wantParts, c.Types[0])
						}
						if r.ID != tc.IDs[i] || r.Partial != tc.Partial || r.Geometry.Type != "APPROXIMATE" || r.Openmaps.Precision != "source_address_point" || r.Geometry.Location != snapshot.Entities[r.ID].Location {
							t.Fatalf("unexpected result: %+v", r)
						}
						if len(tc.Distances) > 0 && (r.Openmaps.Distance == nil || math.Abs(*r.Openmaps.Distance-tc.Distances[i]) > 0.00001) {
							t.Fatalf("unexpected distance: %+v", r)
						}
					}
				}
				sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
				t.Logf("%s: %d results, median %s", tc.Outcome, len(tc.IDs), times[2])
			})
		}
		t.Logf("%s: %d cases and %d raw-source identity/coordinate checks", env, len(suite.Cases), len(suite.Evidence))
	}
	same, changed, absent, added := 0, 0, 0, 0
	for id, b := range snapshots[0].Entities {
		if b.Kind != "address" {
			continue
		}
		c, ok := snapshots[1].Entities[id]
		if !ok {
			absent++
		} else if reflect.DeepEqual(b, c) {
			same++
		} else {
			changed++
		}
	}
	for id, c := range snapshots[1].Entities {
		if c.Kind == "address" {
			if _, ok := snapshots[0].Entities[id]; !ok {
				added++
			}
		}
	}
	t.Logf("All address entities: %d identical, %d changed, %d absent, %d added", same, changed, absent, added)
}
