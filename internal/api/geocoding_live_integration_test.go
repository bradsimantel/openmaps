//go:build integration

package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

func TestLiveGeocoding(t *testing.T) {
	base := os.Getenv("OPENMAPS_URL")
	if base == "" {
		t.Fatal("set OPENMAPS_URL to the local deployment")
	}
	var suite struct {
		Cases []struct {
			Name, Address, Outcome string
			LatLng                 []float64
			IDs                    []string
		}
	}
	raw, err := os.ReadFile("../geocoding/testdata/newport.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &suite); err != nil {
		t.Fatal(err)
	}
	client := http.Client{Timeout: 10 * time.Second}
	fingerprint := ""
	for _, tc := range suite.Cases {
		q := url.Values{}
		q.Set("address", tc.Address)
		if len(tc.LatLng) > 0 {
			q.Del("address")
			q.Set("latlng", fmt.Sprintf("%.15f,%.15f", tc.LatLng[0], tc.LatLng[1]))
		}
		response, err := client.Get(base + "/maps/api/geocode/json?" + q.Encode())
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		hash := response.Header.Get("X-OpenMaps-Dataset")
		if fingerprint == "" {
			fingerprint = hash
		}
		if hash == "" || hash != fingerprint {
			t.Fatal("missing or mixed deployment fingerprint")
		}
		var body struct {
			Openmaps struct{ Outcome string }
			Results  []struct {
				ID string `json:"place_id"`
			}
		}
		if err = json.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 || body.Openmaps.Outcome != tc.Outcome || len(body.Results) != len(tc.IDs) {
			t.Fatalf("%s: %s", tc.Name, raw)
		}
		for i, r := range body.Results {
			if r.ID != tc.IDs[i] {
				t.Fatalf("%s: %s", tc.Name, raw)
			}
		}
	}
	t.Logf("%d live geocoding cases; dataset %s", len(suite.Cases), fingerprint)
}
