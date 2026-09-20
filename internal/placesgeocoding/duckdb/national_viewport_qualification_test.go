package duckdb

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"openmaps/internal/importer"
	"openmaps/internal/places"
)

// TestNationalViewportQualification evaluates the maintained viewport relevance
// slice against a separately built national artifact. It is opt-in because the
// artifact is too large for the routine deterministic test suite.
func TestNationalViewportQualification(t *testing.T) {
	artifact := os.Getenv("OPENMAPS_NATIONAL_VIEWPORT_ARTIFACT")
	checksPath := os.Getenv("OPENMAPS_NATIONAL_VIEWPORT_CHECKS")
	if artifact == "" || checksPath == "" {
		t.Skip("set OPENMAPS_NATIONAL_VIEWPORT_ARTIFACT and OPENMAPS_NATIONAL_VIEWPORT_CHECKS")
	}
	raw, err := os.ReadFile(filepath.Join(artifact, ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	store, err := openVerified(artifact, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	checks, err := importer.ReadQueryChecks(checksPath)
	if err != nil {
		t.Fatal(err)
	}
	passed := 0
	for i, check := range checks {
		check := check
		if t.Run(fmt.Sprintf("%02d_%s/%s", i+1, check.Category, check.Input), func(t *testing.T) {
			if check.LocationBias == nil {
				t.Fatal("viewport qualification check is missing location_bias")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			started := time.Now()
			results, queryErr := autocompleteForCheck(ctx, store, check)
			if queryErr != nil {
				t.Fatalf("elapsed=%s: %v", time.Since(started), queryErr)
			}
			violations, evaluateErr := queryCheckViolations(ctx, store, check, results)
			if evaluateErr != nil {
				t.Fatalf("elapsed=%s: %v", time.Since(started), evaluateErr)
			}
			if len(violations) != 0 {
				t.Fatalf("elapsed=%s: %v", time.Since(started), violations)
			}
			first := places.Entity{}
			distance := 0.0
			if len(results) != 0 {
				first, queryErr = store.Details(ctx, results[0].ID)
				if queryErr != nil {
					t.Fatal(queryErr)
				}
				distance = places.DistanceMeters(check.LocationBias.Center(), first.Location)
			}
			t.Logf("elapsed=%s results=%d first=%s distance_from_center=%.0fm", time.Since(started), len(results), first.ID, distance)
		}) {
			passed++
		}
	}
	t.Logf("viewport relevance score=%d/%d", passed, len(checks))
}
