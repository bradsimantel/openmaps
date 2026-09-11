//go:build integration

package api_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"openmaps/internal/importer"
)

// TestLiveDemo is an opt-in HTTP check, separate from the offline routine suite.
// It does not claim to verify browser rendering or marker placement.
func TestLiveDemo(t *testing.T) {
	base := os.Getenv("OPENMAPS_URL")
	if base == "" {
		t.Fatal("set OPENMAPS_URL to the running demo")
	}
	path := os.Getenv("OPENMAPS_QUERIES")
	checks := importer.NewportPlacesQueryChecks()
	if path != "" {
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		if e = json.Unmarshal(raw, &checks); e != nil {
			t.Fatal(e)
		}
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, e := client.Get(base + "/healthz")
	if e != nil {
		t.Fatal(e)
	}
	var health struct {
		Status         string `json:"status"`
		LookupSnapshot struct {
			SHA256 string `json:"sha256"`
		} `json:"lookup_snapshot"`
		Dataset struct {
			SHA256 string `json:"sha256"`
		} `json:"dataset"` // Deprecated compatibility alias.
	}
	if e = json.NewDecoder(resp.Body).Decode(&health); e != nil {
		t.Fatal(e)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || health.Status != "ok" || health.LookupSnapshot.SHA256 == "" || health.Dataset.SHA256 != health.LookupSnapshot.SHA256 {
		t.Fatalf("deployment not healthy: %+v", health)
	}
	t.Logf("Serving lookup snapshot %s", health.LookupSnapshot.SHA256)
	for _, q := range checks {
		t.Run(q.Input, func(t *testing.T) {
			body, _ := json.Marshal(map[string]string{"input": q.Input})
			resp, e := client.Post(base+"/v1/places:autocomplete", "application/json", bytes.NewReader(body))
			if e != nil {
				t.Fatal(e)
			}
			var results struct {
				Suggestions []struct {
					Prediction struct {
						ID   string `json:"placeId"`
						Text struct {
							Text string `json:"text"`
						} `json:"text"`
						Types []string `json:"types"`
					} `json:"placePrediction"`
				} `json:"suggestions"`
			}
			if e = json.NewDecoder(resp.Body).Decode(&results); e != nil {
				t.Fatal(e)
			}
			resp.Body.Close()
			if resp.StatusCode != 200 || resp.Header.Get("X-OpenMaps-Lookup-Snapshot") != health.LookupSnapshot.SHA256 {
				t.Fatal("request failed or lookup snapshot changed during check")
			}
			if q.Empty {
				if len(results.Suggestions) != 0 {
					t.Fatal("expected no results")
				}
				return
			}
			if len(results.Suggestions) == 0 {
				t.Fatal("no results")
			}
			first := results.Suggestions[0].Prediction
			if q.FirstID != "" && first.ID != q.FirstID {
				t.Fatalf("first ID %s, want %s", first.ID, q.FirstID)
			}
			kindType := map[string]string{"business": "establishment", "address": "street_address", "street": "route", "area": "political"}[q.FirstKind]
			if kindType != "" {
				found := false
				for _, typ := range first.Types {
					found = found || typ == kindType
				}
				if !found {
					t.Fatal("first result has wrong kind")
				}
			}
			for _, s := range results.Suggestions {
				req, _ := http.NewRequest("GET", base+"/v1/places/"+s.Prediction.ID, nil)
				req.Header.Set("X-Goog-FieldMask", "id,displayName,location,formattedAddress,types,attributions")
				resp, e := client.Do(req)
				if e != nil {
					t.Fatal(e)
				}
				var detail struct {
					ID   string `json:"id"`
					Name struct {
						Text string `json:"text"`
					} `json:"displayName"`
					Location struct {
						Lat *float64 `json:"latitude"`
						Lng *float64 `json:"longitude"`
					} `json:"location"`
				}
				if e = json.NewDecoder(resp.Body).Decode(&detail); e != nil {
					t.Fatal(e)
				}
				resp.Body.Close()
				if resp.StatusCode != 200 || resp.Header.Get("X-OpenMaps-Lookup-Snapshot") != health.LookupSnapshot.SHA256 || detail.ID != s.Prediction.ID || detail.Location.Lat == nil || detail.Location.Lng == nil {
					t.Fatal("details failed, missing location or changed lookup snapshot")
				}
				if *detail.Location.Lat < -90 || *detail.Location.Lat > 90 || *detail.Location.Lng < -180 || *detail.Location.Lng > 180 {
					t.Fatal("invalid location")
				}
				t.Log(fmt.Sprintf("%s %s (lat %.8f, lng %.8f)", detail.ID, detail.Name.Text, *detail.Location.Lat, *detail.Location.Lng))
			}
		})
	}
}
