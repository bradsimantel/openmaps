// Package places owns provider-independent place types and text normalization.
package places

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

type Location struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}
type Attribution struct {
	Provider string `json:"provider"`
	URI      string `json:"providerUri"`
}
type Entity struct {
	ID           string
	Kind         string
	Name         string
	Address      string
	Website      string
	Subtype      string
	Location     Location
	Attributions []Attribution
}

// AddressComponents contains only available structured source values. Empty
// fields are unknown; a region or country may be a code rather than a full name.
type AddressComponents struct {
	Number, Street, Locality, Region, Postcode, Country string
}

// Normalize makes search punctuation-, case-, and accent-insensitive. Only
// letters and numbers survive as query tokens.
func Normalize(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	words := strings.Fields(b.String())
	for i, w := range words {
		switch w {
		case "st":
			words[i] = "street"
		case "ave":
			words[i] = "avenue"
		case "rd":
			words[i] = "road"
		case "blvd":
			words[i] = "boulevard"
		case "ln":
			words[i] = "lane"
		case "dr":
			words[i] = "drive"
		case "ct":
			words[i] = "court"
		case "pl":
			words[i] = "place"
		}
	}
	return strings.Join(words, " ")
}
