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
