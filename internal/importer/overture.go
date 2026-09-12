package importer

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"

	"openmaps/internal/places"
)

type feature struct {
	Type     string `json:"type"`
	Geometry struct {
		Type        string     `json:"type"`
		Coordinates [2]float64 `json:"coordinates"`
	} `json:"geometry"`
	Properties json.RawMessage `json:"properties"`
}
type overtureProperties struct {
	ID    string `json:"id"`
	Names struct {
		Primary string          `json:"primary"`
		Common  json.RawMessage `json:"common"`
	} `json:"names"`
	Number        string `json:"number"`
	Street        string `json:"street"`
	Postcode      string `json:"postcode"`
	Country       string `json:"country"`
	AddressLevels []struct {
		Value string `json:"value"`
	} `json:"address_levels"`
	Addresses []struct {
		Freeform string `json:"freeform"`
		Locality string `json:"locality"`
		Postcode string `json:"postcode"`
		Region   string `json:"region"`
		Country  string `json:"country"`
	} `json:"addresses"`
	Websites        []string `json:"websites"`
	OperatingStatus string   `json:"operating_status"`
	Subtype         string   `json:"subtype"`
	Parent          string   `json:"parent_division_id"`
}
type overtureFeature struct {
	feature
	Props overtureProperties
	Raw   json.RawMessage
}

func readOverture(path string) ([]overtureFeature, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	var doc struct {
		Type     string            `json:"type"`
		Features []json.RawMessage `json:"features"`
	}
	if e = json.NewDecoder(f).Decode(&doc); e != nil {
		return nil, e
	}
	if doc.Type != "FeatureCollection" {
		return nil, fmt.Errorf("expected FeatureCollection")
	}
	out := make([]overtureFeature, 0, len(doc.Features))
	seen := map[string]bool{}
	for _, raw := range doc.Features {
		var f overtureFeature
		f.Raw = raw
		if e = json.Unmarshal(raw, &f.feature); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(f.Properties, &f.Props); e != nil {
			return nil, e
		}
		p := f.Geometry.Coordinates
		if f.Type != "Feature" || f.Geometry.Type != "Point" || f.Props.ID == "" || seen[f.Props.ID] || !validLocation(places.Location{Lat: p[1], Lng: p[0]}) {
			return nil, fmt.Errorf("invalid or duplicate Overture point: %s", f.Props.ID)
		}
		seen[f.Props.ID] = true
		out = append(out, f)
	}
	return out, nil
}
func rawValue(v any) json.RawMessage {
	b, e := json.Marshal(v)
	if e != nil {
		panic(e)
	}
	return b
}
func joinPresent(values ...string) string {
	out := []string{}
	for _, s := range values {
		if s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, ", ")
}
func overtureRecord(f overtureFeature, kind, release string) (Record, error) {
	p := f.Props
	r := Record{Source: "overture:" + kind, SourceID: p.ID, Release: release, Priority: 100, Raw: f.Raw,
		Attributes: map[string]json.RawMessage{"location": rawValue(places.Location{Lat: f.Geometry.Coordinates[1], Lng: f.Geometry.Coordinates[0]})},
		Paths:      map[string]string{"location": "/geometry"}, Attributions: []places.Attribution{{Provider: "Overture Maps", URI: "https://docs.overturemaps.org/attribution/"}}}
	name := p.Names.Primary
	switch kind {
	case "address":
		r.Kind = "address"
		name = strings.TrimSpace(p.Number + " " + p.Street)
		parts := []string{name}
		for _, level := range p.AddressLevels {
			parts = append(parts, level.Value)
		}
		parts = append(parts, p.Postcode, p.Country)
		r.Attributes["address"] = rawValue(joinPresent(parts...))
		r.Paths["name"] = "/properties/number + /properties/street"
		r.Paths["address"] = "/properties/number + /properties/street + /properties/address_levels + /properties/postcode + /properties/country"
	case "place", "division":
		aliases := []string{}
		if len(p.Names.Common) > 0 && string(p.Names.Common) != "null" {
			var pairs [][]string
			if e := json.Unmarshal(p.Names.Common, &pairs); e != nil {
				return r, fmt.Errorf("common names %s: %w", p.ID, e)
			}
			for _, pair := range pairs {
				if len(pair) != 2 {
					return r, fmt.Errorf("invalid name pair")
				}
				if pair[1] != "" {
					aliases = append(aliases, pair[1])
				}
			}
		}
		sort.Strings(aliases)
		aliases = unique(aliases)
		r.Attributes["aliases"] = rawValue(aliases)
		r.Paths["aliases"] = "/properties/names/common"
		r.Paths["name"] = "/properties/names/primary"
		if kind == "place" {
			r.Kind = "business"
			if len(p.Addresses) > 0 {
				a := p.Addresses[0]
				s := joinPresent(a.Freeform, a.Locality, a.Region, a.Postcode, a.Country)
				if s != "" {
					r.Attributes["address"] = rawValue(s)
					r.Paths["address"] = "/properties/addresses/0"
				}
			}
			if len(p.Websites) > 0 && validWebsite(p.Websites[0]) {
				r.Attributes["website"] = rawValue(p.Websites[0])
				r.Paths["website"] = "/properties/websites/0"
			}
			r.Attributes["closed"] = rawValue(p.OperatingStatus == "permanently_closed")
			r.Paths["closed"] = "/properties/operating_status"
		} else {
			r.Kind = "area"
			r.Attributes["subtype"] = rawValue(p.Subtype)
			r.Paths["subtype"] = "/properties/subtype"
		}
	default:
		return r, fmt.Errorf("unsupported Overture kind: %s", kind)
	}
	if name == "" {
		return r, fmt.Errorf("missing display name: %s", p.ID)
	}
	r.Attributes["name"] = rawValue(name)
	return r, nil
}
func validWebsite(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Host != "" && (u.Scheme == "http" || u.Scheme == "https")
}
func unique(s []string) []string {
	out := []string{}
	for _, v := range s {
		if len(out) == 0 || out[len(out)-1] != v {
			out = append(out, v)
		}
	}
	return out
}
