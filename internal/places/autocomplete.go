package places

import (
	"strings"
	"unicode"
)

// AutocompleteContext is the structured geographic meaning of a supported
// comma-separated autocomplete input. Name is a locality for area queries and
// a street for street queries. Region is the normalized full US state or
// federal-district name used by the source hierarchy.
type AutocompleteContext struct {
	Name       string
	Locality   string
	Region     string
	RegionCode string
}

// ParseAutocompleteContext recognizes "city, state" and
// "street, city, state" without treating arbitrary comma-separated text as
// geographic context. Unrecognized inputs remain ordinary token searches.
func ParseAutocompleteContext(input string) (AutocompleteContext, bool) {
	parts := strings.Split(input, ",")
	if len(parts) != 2 && len(parts) != 3 {
		return AutocompleteContext{}, false
	}
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if parts[i] == "" {
			return AutocompleteContext{}, false
		}
	}
	region, ok := usRegion(parts[len(parts)-1])
	if !ok {
		return AutocompleteContext{}, false
	}
	name := Normalize(parts[0])
	if len(parts) == 2 {
		name = normalizeLocalityContext(parts[0])
	}
	if name == "" || !containsLetter(parts[0]) {
		return AutocompleteContext{}, false
	}
	context := AutocompleteContext{Name: name, Region: region.Name, RegionCode: region.Code}
	if len(parts) == 3 {
		context.Locality = normalizeLocalityContext(parts[1])
		if context.Locality == "" || !containsLetter(parts[1]) {
			return AutocompleteContext{}, false
		}
	}
	return context, true
}

func normalizeLocalityContext(value string) string {
	normalized := Normalize(value)
	words := strings.Fields(strings.ToLower(value))
	if len(words) > 1 && strings.Trim(words[0], ".") == "st" && strings.HasPrefix(normalized, "street ") {
		return "saint " + strings.TrimPrefix(normalized, "street ")
	}
	return normalized
}

func containsLetter(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

type usRegionContext struct{ Name, Code string }

func usRegion(value string) (usRegionContext, bool) {
	normalized := Normalize(value)
	region, ok := usRegions[normalized]
	if !ok {
		region, ok = usRegions[strings.ReplaceAll(normalized, " ", "")]
	}
	return region, ok
}

var usRegions = func() map[string]usRegionContext {
	regions := map[string]usRegionContext{}
	for code, name := range map[string]string{
		"AL": "Alabama", "AK": "Alaska", "AZ": "Arizona", "AR": "Arkansas",
		"CA": "California", "CO": "Colorado", "CT": "Connecticut", "DE": "Delaware",
		"DC": "District of Columbia", "FL": "Florida", "GA": "Georgia", "HI": "Hawaii",
		"ID": "Idaho", "IL": "Illinois", "IN": "Indiana", "IA": "Iowa",
		"KS": "Kansas", "KY": "Kentucky", "LA": "Louisiana", "ME": "Maine",
		"MD": "Maryland", "MA": "Massachusetts", "MI": "Michigan", "MN": "Minnesota",
		"MS": "Mississippi", "MO": "Missouri", "MT": "Montana", "NE": "Nebraska",
		"NV": "Nevada", "NH": "New Hampshire", "NJ": "New Jersey", "NM": "New Mexico",
		"NY": "New York", "NC": "North Carolina", "ND": "North Dakota", "OH": "Ohio",
		"OK": "Oklahoma", "OR": "Oregon", "PA": "Pennsylvania", "RI": "Rhode Island",
		"SC": "South Carolina", "SD": "South Dakota", "TN": "Tennessee", "TX": "Texas",
		"UT": "Utah", "VT": "Vermont", "VA": "Virginia", "WA": "Washington",
		"WV": "West Virginia", "WI": "Wisconsin", "WY": "Wyoming",
	} {
		normalized := Normalize(name)
		region := usRegionContext{Name: normalized, Code: code}
		regions[Normalize(code)] = region
		regions[normalized] = region
	}
	return regions
}()
