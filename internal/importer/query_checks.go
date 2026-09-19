package importer

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// QueryCheck is a deterministic autocomplete expectation used when comparing
// immutable DuckDB generations. Category and Intent make national expectations
// reviewable; ID, kind and name are independently optional assertions.
type QueryCheck struct {
	Category  string                    `json:"category,omitempty"`
	Intent    string                    `json:"intent,omitempty"`
	Input     string                    `json:"input"`
	FirstID   string                    `json:"first_id,omitempty"`
	FirstKind string                    `json:"first_kind,omitempty"`
	FirstName string                    `json:"first_name,omitempty"`
	Near      *QueryLocationExpectation `json:"near,omitempty"`
	Empty     bool                      `json:"empty,omitempty"`
}

type QueryLocationExpectation struct {
	Lat          float64 `json:"lat"`
	Lng          float64 `json:"lng"`
	RadiusMeters float64 `json:"radius_meters"`
}

// ReadQueryChecks decodes a fresh expectation slice. Decoding into a slice
// pre-populated with regional defaults can retain omitted fields by index.
func ReadQueryChecks(path string) ([]QueryCheck, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	checks := []QueryCheck{}
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&checks); err != nil {
		return nil, err
	}
	if err = decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("expected exactly one query-check array")
	}
	if len(checks) == 0 {
		return nil, fmt.Errorf("query-check array must not be empty")
	}
	seen := map[string]bool{}
	for _, check := range checks {
		input := strings.TrimSpace(check.Input)
		hasFirstExpectation := check.FirstID != "" || check.FirstKind != "" || check.FirstName != "" || check.Near != nil
		if input == "" || seen[input] {
			return nil, fmt.Errorf("query input is empty or duplicated: %q", input)
		}
		seen[input] = true
		if check.Empty == hasFirstExpectation {
			return nil, fmt.Errorf("query must have either first-result expectations or empty=true: %s", input)
		}
		if check.Near != nil && (check.Near.Lat < -90 || check.Near.Lat > 90 || check.Near.Lng < -180 || check.Near.Lng > 180 || check.Near.RadiusMeters <= 0) {
			return nil, fmt.Errorf("query has invalid location expectation: %s", input)
		}
	}
	return checks, nil
}

// NewportPlacesQueryChecks returns the maintained Places autocomplete smoke
// checks used for refresh comparisons and live integration verification.
func NewportPlacesQueryChecks() []QueryCheck {
	return []QueryCheck{
		{Input: "White Horse", FirstID: "om_a5e3dc7692e4d3b90b71b94fba66ec5b", FirstKind: "business"},
		{Input: "50 Bellevue", FirstID: "om_f88c096070879478ee02e036d3e67480", FirstKind: "address"},
		{Input: "Thames", FirstID: "om_0953764bc3686bc97c659471619e9dfa", FirstKind: "street"},
		{Input: "Newport", FirstKind: "area"},
		{Input: "Redwood Library", FirstID: "om_6393fc0fc62fcba159e53e572c6cbbbd", FirstKind: "business"},
		{Input: "26 Marlborough", FirstKind: "address"},
		{Input: "zzxnoresult", Empty: true},
		{Input: "Pearl Car Wash", FirstID: "om_ef8f5f0dc3fcde9f13af6cbdce15c271", FirstKind: "business"},
	}
}
