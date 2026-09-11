// Package geocoding resolves address labels and nearby address points from the
// existing SQLite entities. Import adapters decode retained source components.
package geocoding

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"openmaps/internal/importer/addressdata"
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
type address struct {
	entity     places.Entity
	components places.AddressComponents
	context    string
}

// Store is an immutable, regional address index. Loading existing snapshots
// does not mutate their bytes or require a schema migration.
type Store struct {
	addresses []address
	byText    map[string][]int
	bounds    [4]float64 // west, south, east, north; preview rectangle, not a boundary
}

func Open(ctx context.Context, path string) (*Store, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var version, manifest string
	if err = db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='schema_version'").Scan(&version); err != nil || version != "1" {
		return nil, fmt.Errorf("geocoding requires schema 1: %v", err)
	}
	if err = db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='manifest'").Scan(&manifest); err != nil {
		return nil, err
	}
	var m struct {
		BBox []float64 `json:"bbox"`
	}
	if err = json.Unmarshal([]byte(manifest), &m); err != nil {
		return nil, err
	}
	if len(m.BBox) != 4 || !validPoint(places.Location{Lat: m.BBox[1], Lng: m.BBox[0]}) || !validPoint(places.Location{Lat: m.BBox[3], Lng: m.BBox[2]}) || m.BBox[0] >= m.BBox[2] || m.BBox[1] >= m.BBox[3] {
		return nil, fmt.Errorf("geocoding requires a valid non-dateline preview bbox")
	}
	s := &Store{byText: map[string][]int{}}
	copy(s.bounds[:], m.BBox)
	rows, err := db.QueryContext(ctx, `SELECT e.id,e.kind,e.name,e.address,e.lat,e.lng,e.attributions,
		COALESCE(s.source,''),COALESCE(s.raw,'') FROM entities e
		LEFT JOIN attribute_provenance p ON p.entity_id=e.id AND p.attribute='address'
		LEFT JOIN source_records s ON s.entity_id=p.entity_id AND s.source_key=p.source_key
		WHERE e.kind='address' ORDER BY e.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e places.Entity
		var attrs, source, raw string
		if err = rows.Scan(&e.ID, &e.Kind, &e.Name, &e.Address, &e.Location.Lat, &e.Location.Lng, &attrs, &source, &raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(attrs), &e.Attributions); err != nil {
			return nil, err
		}
		if !s.contains(e.Location) {
			continue
		}
		// Simple number + street labels only. Ranges, fractions and unit labels are
		// preserved in Places but excluded here, including from reverse lookup.
		key, ok := addressKey(e.Name)
		if !ok || unitRE.MatchString(e.Name) {
			continue
		}
		tail, ok := strings.CutPrefix(e.Address, e.Name)
		if !ok && e.Address != "" {
			// Cannot safely interpret conflicting labels.
			continue
		}
		components, err := addressdata.Components(source, raw, e.Name, e.Address)
		if err != nil {
			return nil, fmt.Errorf("address components %s: %w", e.ID, err)
		}
		s.byText[key] = append(s.byText[key], len(s.addresses))
		s.addresses = append(s.addresses, address{entity: e, components: components, context: places.Normalize(tail)})
	}
	return s, rows.Err()
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

// Forward requires the complete street label (abbreviations are expanded).
// Comma-separated context is checked against stored label context. Newport is
// accepted only as the preview's scope, never as an inferred address locality.
func (s *Store) Forward(ctx context.Context, input string) (Response, error) {
	query, out, err := ParseForward(input)
	if err != nil {
		return out, err
	}
	for _, i := range s.byText[query.Key] {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		a := s.addresses[i]
		if ContextMatches(a.context, query.Context) {
			out.Results = append(out.Results, Result{Entity: a.entity, Components: a.components, Partial: query.Partial})
		}
	}
	if len(out.Results) > 0 {
		out.Outcome = "matched"
	}
	if len(out.Results) > 1 {
		out.Outcome = "ambiguous"
	}
	return out, nil
}
func validPoint(p places.Location) bool {
	return !math.IsNaN(p.Lat) && !math.IsNaN(p.Lng) && !math.IsInf(p.Lat, 0) && !math.IsInf(p.Lng, 0) && p.Lat >= -90 && p.Lat <= 90 && p.Lng >= -180 && p.Lng <= 180
}
func (s *Store) contains(p places.Location) bool {
	return p.Lng >= s.bounds[0] && p.Lng <= s.bounds[2] && p.Lat >= s.bounds[1] && p.Lat <= s.bounds[3]
}

// DistanceMeters uses WGS84 latitude/longitude and the IUGG mean Earth radius.
// This is spherical straight-line distance, not walking distance or containment.
func DistanceMeters(a, b places.Location) float64 {
	rad := math.Pi / 180
	x := math.Pow(math.Sin((b.Lat-a.Lat)*rad/2), 2) + math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Pow(math.Sin((b.Lng-a.Lng)*rad/2), 2)
	return 2 * earthRadius * math.Asin(math.Sqrt(math.Min(1, math.Max(0, x))))
}
func (s *Store) Reverse(ctx context.Context, p places.Location) (Response, error) {
	if !validPoint(p) {
		return empty("invalid_input"), fmt.Errorf("latlng must be finite latitude [-90,90],longitude [-180,180], in that order")
	}
	if !s.contains(p) {
		return empty("outside_coverage"), nil
	}
	out := empty("no_nearby_address")
	// A full scan of ~8,500 immutable points is sufficient for this preview.
	// No database or spatial index changes are needed for the 100 m operation.
	nearest := math.Inf(1)
	for _, a := range s.addresses {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		d := DistanceMeters(p, a.entity.Location)
		if d <= ReverseLimitMeters {
			out.Results = append(out.Results, Result{Entity: a.entity, Components: a.components, DistanceMeters: &d})
			nearest = math.Min(nearest, d)
		}
	}
	// Retain all addresses within one millimetre of the nearest point. This
	// tolerance exposes coincident/tied identities rather than inventing a winner.
	kept := out.Results[:0]
	for _, r := range out.Results {
		if *r.DistanceMeters <= nearest+0.001 {
			kept = append(kept, r)
		}
	}
	out.Results = kept
	sort.Slice(out.Results, func(i, j int) bool {
		a, b := out.Results[i], out.Results[j]
		if *a.DistanceMeters != *b.DistanceMeters {
			return *a.DistanceMeters < *b.DistanceMeters
		}
		return a.Entity.ID < b.Entity.ID
	})
	if len(out.Results) > 0 {
		out.Outcome = "matched"
	}
	if len(out.Results) > 1 {
		out.Outcome = "ambiguous"
	}
	return out, nil
}
