package duckdb

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"openmaps/internal/geocoding"
	"openmaps/internal/importer"
	"openmaps/internal/places"
)

// TestNationalContextColdQualification checks every contextual national golden
// query without allowing an earlier query to warm the primary-candidate cache.
// It is opt-in because it requires the separately built national artifact.
func TestNationalContextColdQualification(t *testing.T) {
	artifact := os.Getenv("OPENMAPS_NATIONAL_CONTEXT_ARTIFACT")
	checksPath := os.Getenv("OPENMAPS_NATIONAL_CONTEXT_CHECKS")
	if artifact == "" || checksPath == "" {
		t.Skip("set OPENMAPS_NATIONAL_CONTEXT_ARTIFACT and OPENMAPS_NATIONAL_CONTEXT_CHECKS")
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
	contextChecks := make([]importer.QueryCheck, 0, 25)
	for _, check := range checks {
		if check.Category == "city_state" || check.Category == "street_context" {
			contextChecks = append(contextChecks, check)
		}
	}
	if len(contextChecks) != 25 {
		t.Fatalf("context checks: got %d want 25", len(contextChecks))
	}
	// Put the previously slowest cold query first, then reverse lexical order,
	// so this run cannot accidentally depend on the checked-in suite's order.
	sort.Slice(contextChecks, func(i, j int) bool {
		if contextChecks[i].Input == "Pennsylvania Avenue, Washington, DC" {
			return true
		}
		if contextChecks[j].Input == "Pennsylvania Avenue, Washington, DC" {
			return false
		}
		return contextChecks[i].Input > contextChecks[j].Input
	})
	for _, check := range contextChecks {
		check := check
		t.Run(check.Category+"/"+check.Input, func(t *testing.T) {
			store.primaryCandidateCacheMu.Lock()
			store.primaryCandidateCache = map[string][]primaryCandidate{}
			store.primaryCandidateCacheMu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			started := time.Now()
			results, queryErr := store.Autocomplete(ctx, check.Input)
			elapsed := time.Since(started)
			if queryErr != nil {
				t.Fatalf("elapsed=%s: %v", elapsed, queryErr)
			}
			if len(results) == 0 {
				t.Fatalf("elapsed=%s: no results", elapsed)
			}
			first := results[0]
			if first.Kind != check.FirstKind || check.FirstName != "" && first.Name != check.FirstName {
				t.Fatalf("elapsed=%s: first=%+v", elapsed, first)
			}
			detail, detailErr := store.Details(ctx, first.ID)
			if detailErr != nil {
				t.Fatalf("elapsed=%s: %v", elapsed, detailErr)
			}
			if check.Near == nil {
				t.Logf("elapsed=%s id=%s", elapsed, first.ID)
				return
			}
			distance := geocoding.DistanceMeters(detail.Location, places.Location{Lat: check.Near.Lat, Lng: check.Near.Lng})
			if distance > check.Near.RadiusMeters {
				t.Fatalf("elapsed=%s: distance=%.0fm", elapsed, distance)
			}
			t.Logf("elapsed=%s distance=%.0fm id=%s", elapsed, distance, first.ID)
		})
	}
}
