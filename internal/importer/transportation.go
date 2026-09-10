package importer

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"

	"openmaps/internal/places"
)

type transportationFeature struct {
	Type     string `json:"type"`
	Geometry struct {
		Type        string       `json:"type"`
		Coordinates [][2]float64 `json:"coordinates"`
	} `json:"geometry"`
	Properties json.RawMessage `json:"properties"`
	Props      transportationProperties
	Raw        json.RawMessage
}

type transportationProperties struct {
	ID      string `json:"id"`
	Subtype string `json:"subtype"`
	Names   struct {
		Primary string          `json:"primary"`
		Common  json.RawMessage `json:"common"`
		Rules   []struct {
			Value   string `json:"value"`
			Variant string `json:"variant"`
		} `json:"rules"`
	} `json:"names"`
}

type transportationSelection struct {
	Segments        int `json:"segments"`
	NamedRoads      int `json:"named_road_segments"`
	UnnamedRoads    int `json:"unnamed_road_segments"`
	NonRoads        int `json:"non_road_segments"`
	OutsideGeometry int `json:"outside_geometry_segments"`
}

func readTransportation(ctx context.Context, path, release string, bounds [4]float64) ([]Record, transportationSelection, error) {
	var selection transportationSelection
	f, err := os.Open(path)
	if err != nil {
		return nil, selection, err
	}
	defer f.Close()
	var doc struct {
		Type     string            `json:"type"`
		Features []json.RawMessage `json:"features"`
	}
	if err = json.NewDecoder(f).Decode(&doc); err != nil {
		return nil, selection, err
	}
	if doc.Type != "FeatureCollection" {
		return nil, selection, fmt.Errorf("expected FeatureCollection")
	}
	records := make([]Record, 0, len(doc.Features))
	seen := map[string]bool{}
	for _, raw := range doc.Features {
		if err = ctx.Err(); err != nil {
			return nil, selection, err
		}
		var feature transportationFeature
		feature.Raw = raw
		if err = json.Unmarshal(raw, &feature); err != nil {
			return nil, selection, err
		}
		if err = json.Unmarshal(feature.Properties, &feature.Props); err != nil {
			return nil, selection, err
		}
		if feature.Type != "Feature" || feature.Geometry.Type != "LineString" || feature.Props.ID == "" || seen[feature.Props.ID] || !validLineString(feature.Geometry.Coordinates) {
			return nil, selection, fmt.Errorf("invalid or duplicate Overture transportation segment: %s", feature.Props.ID)
		}
		seen[feature.Props.ID] = true
		selection.Segments++
		if feature.Props.Subtype != "road" {
			selection.NonRoads++
			continue
		}
		if strings.TrimSpace(feature.Props.Names.Primary) == "" {
			selection.UnnamedRoads++
			continue
		}
		location, ok := representativeCoordinate(feature.Geometry.Coordinates, bounds)
		if !ok {
			selection.OutsideGeometry++
			continue
		}
		aliases, err := transportationAliases(feature.Props)
		if err != nil {
			return nil, selection, fmt.Errorf("transportation names %s: %w", feature.Props.ID, err)
		}
		selection.NamedRoads++
		records = append(records, Record{
			Source:   "overture:segment",
			SourceID: feature.Props.ID,
			Release:  release,
			Kind:     "street",
			Priority: 100,
			Attributes: map[string]json.RawMessage{
				"name":     rawValue(feature.Props.Names.Primary),
				"location": rawValue(places.Location{Lat: location[1], Lng: location[0]}),
				"aliases":  rawValue(aliases),
			},
			Paths: map[string]string{
				"name":     "/properties/names/primary",
				"location": "/geometry (length midpoint of centerline clipped to manifest bbox)",
				"aliases":  "/properties/names/common + /properties/names/rules/*/value",
			},
			Raw: feature.Raw,
			Attributions: []places.Attribution{{
				Provider: "© OpenStreetMap contributors, Overture Maps Foundation",
				URI:      "https://docs.overturemaps.org/attribution/",
			}},
		})
	}
	return records, selection, nil
}

func validLineString(points [][2]float64) bool {
	if len(points) < 2 {
		return false
	}
	for _, point := range points {
		if !validLocation(places.Location{Lat: point[1], Lng: point[0]}) {
			return false
		}
	}
	return true
}

func transportationAliases(properties transportationProperties) ([]string, error) {
	aliases := []string{}
	if len(properties.Names.Common) > 0 && string(properties.Names.Common) != "null" {
		var common [][]string
		if err := json.Unmarshal(properties.Names.Common, &common); err != nil {
			return nil, err
		}
		for _, pair := range common {
			if len(pair) != 2 {
				return nil, fmt.Errorf("invalid common-name pair")
			}
			if strings.TrimSpace(pair[1]) != "" {
				aliases = append(aliases, pair[1])
			}
		}
	}
	for _, rule := range properties.Names.Rules {
		if strings.TrimSpace(rule.Value) != "" {
			aliases = append(aliases, rule.Value)
		}
	}
	sort.Strings(aliases)
	aliases = unique(aliases)
	out := aliases[:0]
	for _, alias := range aliases {
		if alias != properties.Names.Primary {
			out = append(out, alias)
		}
	}
	return out, nil
}

// representativeCoordinate clips every centerline edge to the closed launch
// rectangle, then returns the distance midpoint across the retained portions.
// Linear interpolation is in WGS84 longitude/latitude; lengths use distance.
func representativeCoordinate(points [][2]float64, bounds [4]float64) ([2]float64, bool) {
	type portion struct {
		start, end [2]float64
		length     float64
	}
	portions := []portion{}
	total := 0.0
	for i := 1; i < len(points); i++ {
		start, end, ok := clipLine(points[i-1], points[i], bounds)
		if !ok {
			continue
		}
		length := distance(start, end)
		portions = append(portions, portion{start: start, end: end, length: length})
		total += length
	}
	if len(portions) == 0 {
		return [2]float64{}, false
	}
	if total == 0 {
		return portions[0].start, true
	}
	target := total / 2
	covered := 0.0
	for _, portion := range portions {
		if covered+portion.length >= target {
			fraction := (target - covered) / portion.length
			return [2]float64{
				portion.start[0] + fraction*(portion.end[0]-portion.start[0]),
				portion.start[1] + fraction*(portion.end[1]-portion.start[1]),
			}, true
		}
		covered += portion.length
	}
	return portions[len(portions)-1].end, true
}

// clipLine is Liang-Barsky clipping against a closed, non-dateline rectangle.
func clipLine(start, end [2]float64, bounds [4]float64) ([2]float64, [2]float64, bool) {
	dx, dy := end[0]-start[0], end[1]-start[1]
	low, high := 0.0, 1.0
	for _, edge := range [][2]float64{{-dx, start[0] - bounds[0]}, {dx, bounds[2] - start[0]}, {-dy, start[1] - bounds[1]}, {dy, bounds[3] - start[1]}} {
		p, q := edge[0], edge[1]
		if p == 0 {
			if q < 0 {
				return [2]float64{}, [2]float64{}, false
			}
			continue
		}
		ratio := q / p
		if p < 0 {
			low = math.Max(low, ratio)
		} else {
			high = math.Min(high, ratio)
		}
		if low > high {
			return [2]float64{}, [2]float64{}, false
		}
	}
	return [2]float64{start[0] + low*dx, start[1] + low*dy}, [2]float64{start[0] + high*dx, start[1] + high*dy}, true
}
