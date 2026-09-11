package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/cases"
)

// Prepare is offline and verifies all inputs before parsing any source data.
// Scheduling or accepting a new release never implicitly changes its pins.
func Prepare(ctx context.Context, m Manifest, dataDir string, identities map[string]string) (Bundle, map[string]any, error) {
	b := Bundle{Schema: 1, Manifest: rawValue(m), Identities: identities, Records: []Record{}, Relationships: []Relationship{}, Rejections: []Rejection{}}
	fail := func(e error) (Bundle, map[string]any, error) { return b, nil, e }
	for _, input := range m.Inputs {
		if e := Verify(filepath.Join(dataDir, input.File), input.SHA256); e != nil {
			return fail(e)
		}
	}
	features := map[string][]overtureFeature{}
	releases := map[string]string{}
	var transportationInput *Input
	for _, input := range m.Inputs {
		kind := sourceKind(input.URL)
		if kind == "segment" {
			if transportationInput != nil {
				return fail(fmt.Errorf("multiple Overture Transportation segment inputs are not supported"))
			}
			inputCopy := input
			transportationInput = &inputCopy
			releases[kind] = input.Release
			continue
		}
		if kind == "" {
			return fail(fmt.Errorf("unsupported source: %s", input.URL))
		}
		if _, ok := features[kind]; ok {
			return fail(fmt.Errorf("duplicate Overture theme: %s", kind))
		}
		fs, e := readOverture(filepath.Join(dataDir, input.File))
		if e != nil {
			return fail(e)
		}
		features[kind] = fs
		releases[kind] = input.Release
	}
	if len(features) != 3 || transportationInput == nil {
		return fail(fmt.Errorf("Overture places, addresses, divisions and transportation segments are required"))
	}
	sourceFeatures := map[string]overtureFeature{}
	for _, kind := range []string{"place", "address"} {
		for _, f := range features[kind] {
			if !inside(f.Geometry.Coordinates, m.BBox) {
				b.Rejections = append(b.Rejections, Rejection{SourceKey: "overture:" + kind + ":" + f.Props.ID, Reason: "outside_manifest_bounds", Raw: f.Raw})
				continue
			}
			r, e := overtureRecord(f, kind, releases[kind])
			if e != nil {
				return fail(e)
			}
			b.Records = append(b.Records, r)
			sourceFeatures[r.Key()] = f
		}
	}
	divisions := map[string]overtureFeature{}
	selected := map[string]bool{}
	pending := []string{}
	for _, f := range features["division"] {
		divisions[f.Props.ID] = f
		if inside(f.Geometry.Coordinates, m.BBox) {
			selected[f.Props.ID] = true
			pending = append(pending, f.Props.ID)
		}
	}
	for len(pending) > 0 {
		id := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		parent := divisions[id].Props.Parent
		if _, ok := divisions[parent]; ok && !selected[parent] {
			selected[parent] = true
			pending = append(pending, parent)
		}
	}
	for id := range selected {
		r, e := overtureRecord(divisions[id], "division", releases["division"])
		if e != nil {
			return fail(e)
		}
		b.Records = append(b.Records, r)
		parent := divisions[id].Props.Parent
		if selected[parent] {
			b.Relationships = append(b.Relationships, Relationship{From: r.Key(), To: "overture:division:" + parent, Kind: "parent_area", Evidence: "Overture parent_division_id"})
		}
	}
	for id, division := range divisions {
		if !selected[id] {
			b.Rejections = append(b.Rejections, Rejection{SourceKey: "overture:division:" + id, Reason: "outside_manifest_hierarchy", Raw: division.Raw})
		}
	}
	if e := ctx.Err(); e != nil {
		return fail(e)
	}
	streets, transportationRejections, transportation, e := readTransportation(ctx, filepath.Join(dataDir, transportationInput.File), transportationInput.Release, m.BBox)
	if e != nil {
		return fail(e)
	}
	b.Records = append(b.Records, streets...)
	b.Rejections = append(b.Rejections, transportationRejections...)
	addresses := map[string][]Record{}
	for _, r := range b.Records {
		if r.Kind == "address" {
			name := recordName(r)
			k := normalizeAddress(name)
			addresses[k] = append(addresses[k], r)
		}
	}
	linked, ambiguous := 0, 0
	for _, r := range b.Records {
		if r.Kind != "business" {
			continue
		}
		f := sourceFeatures[r.Key()]
		if len(f.Props.Addresses) == 0 {
			continue
		}
		a := f.Props.Addresses[0]
		candidates := []Record{}
		for _, candidate := range addresses[normalizeAddress(a.Freeform)] {
			af := sourceFeatures[candidate.Key()]
			if postcodePrefix(a.Postcode) == postcodePrefix(af.Props.Postcode) && distance(f.Geometry.Coordinates, af.Geometry.Coordinates) <= 50 {
				candidates = append(candidates, candidate)
			}
		}
		if len(candidates) == 1 {
			linked++
			b.Relationships = append(b.Relationships, Relationship{From: r.Key(), To: candidates[0].Key(), Kind: "address", Evidence: "Exact normalized number/street + postcode; unique within 50 metres"})
		} else if len(candidates) > 1 {
			ambiguous++
		}
	}
	sort.Slice(b.Records, func(i, j int) bool { return b.Records[i].Key() < b.Records[j].Key() })
	sort.Slice(b.Relationships, func(i, j int) bool {
		a, c := b.Relationships[i], b.Relationships[j]
		if a.From != c.From {
			return a.From < c.From
		}
		if a.Kind != c.Kind {
			return a.Kind < c.Kind
		}
		return a.To < c.To
	})
	sort.Slice(b.Rejections, func(i, j int) bool {
		if b.Rejections[i].SourceKey != b.Rejections[j].SourceKey {
			return b.Rejections[i].SourceKey < b.Rejections[j].SourceKey
		}
		return b.Rejections[i].Reason < b.Rejections[j].Reason
	})
	counts := map[string]int{}
	rejectedCounts := map[string]int{}
	streetNames := map[string]bool{}
	rawCounts := map[string]int{}
	for kind, fs := range features {
		rawCounts[kind] = len(fs)
	}
	for _, r := range b.Records {
		counts[r.Kind]++
		if r.Kind == "street" {
			streetNames[recordName(r)] = true
		}
	}
	for _, rejection := range b.Rejections {
		rejectedCounts[rejection.Reason]++
	}
	rawCounts["segment"] = transportation.Segments
	audit := map[string]any{"raw_counts": rawCounts, "imported_counts": counts, "rejected_counts": rejectedCounts, "linked_business_addresses": linked, "ambiguous_business_addresses": ambiguous, "transportation_selection": transportation, "unique_street_names": len(streetNames)}
	for _, kind := range []string{"place", "address"} {
		labels := map[string]int{}
		completeness := map[string]int{}
		fields := []string{"number", "street", "postcode", "unit"}
		if kind == "place" {
			fields = []string{"websites", "phones", "operating_status"}
		}
		for _, field := range fields {
			completeness[field] = 0
		}
		for _, f := range features[kind] {
			var props map[string]any
			if e = json.Unmarshal(f.Properties, &props); e != nil {
				return fail(e)
			}
			for _, field := range fields {
				if nonempty(props[field]) {
					completeness[field]++
				}
			}
			var label string
			if kind == "place" {
				address := ""
				if len(f.Props.Addresses) > 0 {
					address = f.Props.Addresses[0].Freeform
				}
				label = normalizeAddress(f.Props.Names.Primary) + "\x00" + normalizeAddress(address)
			} else {
				label = normalizeAddress(f.Props.Number+" "+f.Props.Street) + "\x00" + fmt.Sprint(props["unit"])
			}
			labels[label]++
		}
		groups, excess := 0, 0
		for _, n := range labels {
			if n > 1 {
				groups++
				excess += n - 1
			}
		}
		audit[kind+"_completeness"] = completeness
		audit[kind+"_duplicate_label_groups"] = groups
		audit[kind+"_duplicate_label_excess"] = excess
	}
	return b, audit, ctx.Err()
}
func sourceKind(url string) string {
	for _, kind := range []string{"place", "address", "division", "segment"} {
		if strings.Contains(url, "/type="+kind+"/") {
			return kind
		}
	}
	return ""
}
func recordName(r Record) string {
	var name string
	_ = json.Unmarshal(r.Attributes["name"], &name)
	return name
}
func postcodePrefix(s string) string {
	r := []rune(s)
	if len(r) > 5 {
		r = r[:5]
	}
	return string(r)
}
func nonempty(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	case bool:
		return x
	default:
		return true
	}
}

// Matching preserves accents; it expands only the launch region's documented
// street abbreviations. It deliberately does not strip units or guess ranges.
func normalizeAddress(s string) string {
	words := strings.FieldsFunc(cases.Fold().String(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) && r != '_' })
	abbreviations := map[string]string{"st": "street", "ave": "avenue", "rd": "road", "blvd": "boulevard", "ln": "lane", "dr": "drive", "ct": "court", "pl": "place"}
	for i, w := range words {
		if expanded, ok := abbreviations[w]; ok {
			words[i] = expanded
		}
	}
	return strings.Join(words, " ")
}
func distance(a, b [2]float64) float64 {
	x1, y1, x2, y2 := a[0]*math.Pi/180, a[1]*math.Pi/180, b[0]*math.Pi/180, b[1]*math.Pi/180
	h := math.Pow(math.Sin((y2-y1)/2), 2) + math.Cos(y1)*math.Cos(y2)*math.Pow(math.Sin((x2-x1)/2), 2)
	return 6371008.8 * 2 * math.Asin(math.Min(1, math.Sqrt(h)))
}
