//go:build integration

package qualification

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedNationalCoverage(t *testing.T) {
	root := os.Getenv("OPENMAPS_SCOUT_ACQUISITION")
	if root == "" {
		t.Skip("set OPENMAPS_SCOUT_ACQUISITION to retained pinned data")
	}
	result, e := Coverage(filepath.Join(root, "cb_2025_us_state_500k.zip"), filepath.Join(root, "national-aleutian-audit.json"), filepath.Join(root, "national-complete-all.jsonl"))
	if e != nil {
		t.Fatal(e)
	}
	if len(result["route_states"].([]string)) != 51 || len(result["missing_reference_tiles_touching_states"].([]any)) != 0 || len(result["geometry_examples_inside_states"].([]any)) != 0 || len(result["missing_route_states"].([]string)) != 0 || len(result["route_failures"].([]any)) != 0 {
		t.Fatal(result)
	}
}
