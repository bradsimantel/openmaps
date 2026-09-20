package importer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadQueryChecksDoesNotInheritDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queries.json")
	if err := os.WriteFile(path, []byte(`[{"input":"New York","first_kind":"area"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	checks, err := ReadQueryChecks(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || checks[0].Input != "New York" || checks[0].FirstKind != "area" || checks[0].FirstID != "" || checks[0].Empty {
		t.Fatalf("unexpected checks: %+v", checks)
	}
}

func TestReadQueryChecksRejectsTrailingJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queries.json")
	if err := os.WriteFile(path, []byte(`[] []`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadQueryChecks(path); err == nil {
		t.Fatal("accepted trailing JSON")
	}
}

func TestReadQueryChecksRejectsEmptySet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queries.json")
	if err := os.WriteFile(path, []byte(`[]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadQueryChecks(path); err == nil {
		t.Fatal("accepted empty query-check set")
	}
}

func TestReadQueryChecksAcceptsSameInputWithDifferentViewports(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queries.json")
	raw := `[
{"input":"Main Street","location_bias":{"south":40,"west":-75,"north":41,"east":-74},"first_kind":"street","min_results":1,"first_inside_bias":true},
{"input":"Main Street","location_bias":{"south":33,"west":-119,"north":35,"east":-117},"first_kind":"street","outside_result_required":true,"distance_ordered_from_bias_center":true}
]`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	checks, err := ReadQueryChecks(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 2 || checks[0].LocationBias == nil || checks[1].LocationBias == nil || !checks[0].FirstInsideBias || !checks[1].OutsideResultRequired || !checks[1].DistanceOrderedFromBiasCenter {
		t.Fatalf("unexpected viewport checks: %+v", checks)
	}
}

func TestReadQueryChecksRejectsInvalidViewportAssertions(t *testing.T) {
	for name, raw := range map[string]string{
		"missing bias":     `[{"input":"Main Street","first_kind":"street","first_inside_bias":true}]`,
		"invalid bias":     `[{"input":"Main Street","location_bias":{"south":91,"west":0,"north":91,"east":1},"first_kind":"street"}]`,
		"contradictory":    `[{"input":"Main Street","location_bias":{"south":40,"west":-75,"north":41,"east":-74},"first_kind":"street","first_inside_bias":true,"all_results_outside_bias":true}]`,
		"too many results": `[{"input":"Main Street","first_kind":"street","min_results":6}]`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "queries.json")
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := ReadQueryChecks(path); err == nil {
				t.Fatal("accepted invalid viewport query check")
			}
		})
	}
}

func TestNationalQueryChecksCoverMaintainedCategories(t *testing.T) {
	checks, err := ReadQueryChecks(filepath.Join("..", "..", "config", "us-query-checks.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"city": 19, "state": 6, "city_state": 16, "zip": 10, "zip_context": 5, "street": 10, "street_context": 9, "landmark": 19, "business_context": 3, "negative": 2}
	got := map[string]int{}
	for _, check := range checks {
		if check.Category == "" || check.Intent == "" {
			t.Fatalf("national query lacks review metadata: %+v", check)
		}
		got[check.Category]++
	}
	if len(checks) != 99 || len(got) != len(want) {
		t.Fatalf("national coverage shape: %d checks in %v", len(checks), got)
	}
	for category, count := range want {
		if got[category] != count {
			t.Fatalf("national category %s = %d, want %d", category, got[category], count)
		}
	}
}

func TestNationalViewportQueryChecksCoverMaintainedCategories(t *testing.T) {
	checks, err := ReadQueryChecks(filepath.Join("..", "..", "config", "us-viewport-query-checks.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int{"viewport_business": 4, "viewport_street": 2}
	got := map[string]int{}
	for _, check := range checks {
		if check.Category == "" || check.Intent == "" || check.LocationBias == nil {
			t.Fatalf("viewport query lacks review metadata or bias: %+v", check)
		}
		got[check.Category]++
	}
	if len(checks) != 6 || len(got) != len(want) {
		t.Fatalf("viewport coverage shape: %d checks in %v", len(checks), got)
	}
	for category, count := range want {
		if got[category] != count {
			t.Fatalf("viewport category %s = %d, want %d", category, got[category], count)
		}
	}
}
