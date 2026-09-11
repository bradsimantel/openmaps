// Package geocoding owns provider-independent address parsing and distance
// semantics. Import adapters decode retained source components.
package geocoding

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"

	"openmaps/internal/places"
)

const ReverseLimitMeters = 100.0
const earthRadius = 6371008.8

type Result struct {
	Entity         places.Entity
	Components     places.AddressComponents
	DistanceMeters *float64
	Partial        bool
}
type Response struct {
	Results []Result
	Outcome string
}

var numberRE = regexp.MustCompile(`^([0-9]+)([a-zA-Z]?)\s+([\pL].*)$`)
var unitRE = regexp.MustCompile(`(?i)(#|\b(apt|apartment|unit|suite|ste|floor|fl|room)\b)`)

func addressKey(input string) (string, bool) {
	m := numberRE.FindStringSubmatch(strings.TrimSpace(input))
	if m == nil {
		return "", false
	}
	number := strings.TrimLeft(m[1], "0")
	if number == "" {
		number = "0"
	}
	street := places.Normalize(m[3])
	if street == "" {
		return "", false
	}
	return number + strings.ToLower(m[2]) + " " + street, true
}
func empty(outcome string) Response { return Response{Results: []Result{}, Outcome: outcome} }

// AddressIndex derives the exact serving key and retained label context for a
// supported source address. Compact serving artifacts use this same function
// so they cannot broaden the maintained geocoding grammar during import.
func AddressIndex(name, formatted string) (key, context string, ok bool) {
	key, ok = addressKey(name)
	if !ok || unitRE.MatchString(name) {
		return "", "", false
	}
	tail, ok := strings.CutPrefix(formatted, name)
	if !ok && formatted != "" {
		return "", "", false
	}
	return key, places.Normalize(tail), true
}

// ForwardQuery is the validated exact-address portion of a forward request.
type ForwardQuery struct {
	Key, Context string
	Partial      bool
}

// ParseForward applies the public input grammar without consulting a store.
func ParseForward(input string) (ForwardQuery, Response, error) {
	var query ForwardQuery
	if !utf8.ValidString(input) || utf8.RuneCountInString(input) > 200 || strings.TrimSpace(input) == "" {
		return query, empty("invalid_input"), fmt.Errorf("address must contain 1–200 Unicode characters")
	}
	if unitRE.MatchString(input) {
		return query, empty("unsupported_input"), fmt.Errorf("unit, suite, floor and apartment lookup is unsupported; units are missing in this dataset")
	}
	parts := strings.Split(input, ",")
	key, ok := addressKey(parts[0])
	if !ok {
		return query, empty("unsupported_input"), fmt.Errorf("use a simple house number and complete street name; ranges, fractions, businesses, streets alone and localities alone are unsupported")
	}
	contextText := places.Normalize(strings.Join(parts[1:], " "))
	contextText = " " + contextText + " "
	contextText = strings.ReplaceAll(contextText, " rhode island ", " ri ")
	contextText = strings.ReplaceAll(contextText, " united states ", " us ")
	contextText = strings.ReplaceAll(contextText, " usa ", " us ")
	contextText = strings.TrimSpace(contextText)
	partial := false
	if contextText == "newport" || strings.HasPrefix(contextText, "newport ") {
		contextText = strings.TrimSpace(strings.TrimPrefix(contextText, "newport"))
		partial = true
	}
	return ForwardQuery{Key: key, Context: contextText, Partial: partial}, empty("no_match"), nil
}

// ContextMatches requires query context tokens to appear in order in retained
// source context; it never drops an unknown locality, postcode, or country.
func ContextMatches(stored, requested string) bool {
	remaining := strings.Fields(stored)
	for _, want := range strings.Fields(requested) {
		found := -1
		for j, have := range remaining {
			if have == want {
				found = j
				break
			}
		}
		if found < 0 {
			return false
		}
		remaining = remaining[found+1:]
	}
	return true
}

// DistanceMeters uses WGS84 latitude/longitude and the IUGG mean Earth radius.
// This is spherical straight-line distance, not walking distance or containment.
func DistanceMeters(a, b places.Location) float64 {
	rad := math.Pi / 180
	x := math.Pow(math.Sin((b.Lat-a.Lat)*rad/2), 2) + math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Pow(math.Sin((b.Lng-a.Lng)*rad/2), 2)
	return 2 * earthRadius * math.Asin(math.Sqrt(math.Min(1, math.Max(0, x))))
}
