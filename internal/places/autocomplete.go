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
	Name     string
	Locality string
	Region   string
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
	if name == "" || !containsLetter(parts[0]) {
		return AutocompleteContext{}, false
	}
	context := AutocompleteContext{Name: name, Region: region}
	if len(parts) == 3 {
		context.Locality = Normalize(parts[1])
		if context.Locality == "" || !containsLetter(parts[1]) {
			return AutocompleteContext{}, false
		}
	}
	return context, true
}

func containsLetter(value string) bool {
	for _, r := range value {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func usRegion(value string) (string, bool) {
	normalized := Normalize(value)
	region, ok := usRegions[normalized]
	if !ok {
		region, ok = usRegions[strings.ReplaceAll(normalized, " ", "")]
	}
	return region, ok
}

var usRegions = func() map[string]string {
	regions := map[string]string{}
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
		regions[Normalize(code)] = normalized
		regions[normalized] = normalized
	}
	return regions
}()
