// Package addressdata projects structured addresses from retained import records.
// It keeps source-specific decoding outside the lookup and API domains.
package addressdata

import (
	"encoding/json"
	"strings"

	"openmaps/internal/places"
)

// Components reads the source selected by attribute_provenance for address.
// Unknown providers and conflicting source/entity labels yield no components:
// never combine parts from a losing source or parse a formatted address.
func Components(source, raw, name, formatted string) (places.AddressComponents, error) {
	var out places.AddressComponents
	if source != "overture:address" {
		return out, nil
	}
	var f struct {
		Properties struct {
			Number, Street, Postcode, Country string
			Levels                            []struct{ Value string } `json:"address_levels"`
		}
	}
	if err := json.Unmarshal([]byte(raw), &f); err != nil {
		return out, err
	}
	p := f.Properties
	label := strings.TrimSpace(p.Number + " " + p.Street)
	parts := []string{label}
	for _, level := range p.Levels {
		parts = append(parts, level.Value)
	}
	parts = append(parts, p.Postcode, p.Country)
	present := []string{}
	for _, part := range parts {
		if part != "" {
			present = append(present, part)
		}
	}
	if label != name || strings.Join(present, ", ") != formatted {
		return out, nil
	}
	out.Number, out.Street, out.Postcode, out.Country = p.Number, p.Street, p.Postcode, p.Country
	// Overture's address-level meanings are country-dependent. The documented
	// US layout is state, locality; do not apply it to other countries/layouts.
	if p.Country == "US" && len(p.Levels) == 2 {
		out.Region, out.Locality = p.Levels[0].Value, p.Levels[1].Value
	}
	return out, nil
}
