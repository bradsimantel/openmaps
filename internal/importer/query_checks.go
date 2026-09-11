package importer

// QueryCheck is a deterministic autocomplete expectation used when comparing
// immutable DuckDB generations.
type QueryCheck struct {
	Input     string `json:"input"`
	FirstID   string `json:"first_id,omitempty"`
	FirstKind string `json:"first_kind,omitempty"`
	Empty     bool   `json:"empty,omitempty"`
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
