package opensearch

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"openmaps/internal/places"
)

const candidateLimit = 50

type DetailsStore interface {
	Details(context.Context, string) (places.Entity, error)
}

// Store uses OpenSearch only for candidate IDs. Every public result is
// materialized through the verified generation's existing Details path.
type Store struct {
	client  *Client
	details DetailsStore
}

func NewStore(client *Client, details DetailsStore) (*Store, error) {
	if client == nil || details == nil {
		return nil, fmt.Errorf("OpenSearch client and Details store are required")
	}
	return &Store{client: client, details: details}, nil
}

func (s *Store) Details(ctx context.Context, id string) (places.Entity, error) {
	return s.details.Details(ctx, id)
}

func (s *Store) Autocomplete(ctx context.Context, input string) ([]places.Entity, error) {
	return s.AutocompleteWithBias(ctx, input, nil)
}

func (s *Store) AutocompleteWithBias(ctx context.Context, input string, bias *places.Viewport) ([]places.Entity, error) {
	normalized := places.Normalize(input)
	if normalized == "" {
		return []places.Entity{}, nil
	}
	var (
		hits []searchHit
		err  error
	)
	preferStreet := looksLikeStreet(normalized)
	if interpreted, ok := places.ParseAutocompleteContext(input); ok {
		hits, err = s.structured(ctx, interpreted, bias)
	} else {
		// Exact primary-name retrieval is both the strongest semantic signal and
		// much cheaper than analyzed prefix scoring for high-frequency names.
		// Biased non-street queries retain the analyzed path so aliases such as
		// "The UPS Store" can compete on distance.
		if bias == nil || preferStreet {
			if bias != nil {
				// Fetch a small local pool first, then score-merge the same exact-name
				// candidates globally. The radius accelerates the local phase; the
				// global phase keeps the viewport a ranking bias, never a filter.
				local := searchRequest{ExactName: normalized, Bias: bias, Size: candidateLimit, PreferStreet: preferStreet}
				local.RadiusM = max(50_000, viewportScale(*bias)*2)
				var global []searchHit
				localHits, localErr := s.search(ctx, local)
				if localErr == nil {
					global, localErr = s.search(ctx, searchRequest{ExactName: normalized, Size: candidateLimit, PreferStreet: preferStreet})
				}
				hits, err = mergeRankedHits(localHits, global), localErr
			} else {
				hits, err = s.search(ctx, searchRequest{ExactName: normalized, Size: candidateLimit, PreferStreet: preferStreet})
			}
		}
		if err == nil && len(hits) < 5 {
			var broader []searchHit
			broader, err = s.search(ctx, searchRequest{Text: normalized, Bias: bias, Size: candidateLimit, PreferStreet: preferStreet})
			hits = mergeHits(hits, broader)
		}
	}
	if err != nil {
		return nil, err
	}
	if len(hits) < 5 && utf8.RuneCountInString(normalized) >= 4 {
		fallback, fallbackErr := s.search(ctx, searchRequest{Text: normalized, Bias: bias, Size: candidateLimit, Fuzzy: true})
		if fallbackErr != nil {
			return nil, fallbackErr
		}
		hits = mergeHits(hits, fallback)
	}
	results := make([]places.Entity, 0, 5)
	for _, hit := range hits {
		entity, detailsErr := s.details.Details(ctx, hit.ID)
		if detailsErr != nil {
			return nil, fmt.Errorf("candidate %s details: %w", hit.ID, detailsErr)
		}
		results = append(results, entity)
		if len(results) == 5 {
			break
		}
	}
	return results, nil
}

func looksLikeStreet(normalized string) bool {
	tokens := strings.Fields(normalized)
	if len(tokens) == 0 {
		return false
	}
	switch tokens[len(tokens)-1] {
	case "street", "st", "avenue", "ave", "boulevard", "blvd", "road", "rd", "highway", "hwy", "lane", "ln", "drive", "dr", "way", "broadway":
		return true
	default:
		return false
	}
}

func (s *Store) structured(ctx context.Context, query places.AutocompleteContext, bias *places.Viewport) ([]searchHit, error) {
	regions, err := s.search(ctx, searchRequest{ExactName: query.Region, Kinds: []string{"area"}, Subtypes: []string{"region"}, Size: 5})
	if err != nil || len(regions) == 0 {
		return regions, err
	}
	regionBias := pointBias(regions[0].Source.Location, 900_000)
	localityName := query.Name
	if query.Locality != "" {
		localityName = query.Locality
	}
	localities, err := s.search(ctx, searchRequest{ExactName: localityName, Kinds: []string{"area"}, Subtypes: []string{"locality", "county", "macrocounty"}, Point: regionBias, Size: 10})
	if err != nil || len(localities) == 0 || query.Locality == "" {
		return localities, err
	}
	localityBias := pointBias(localities[0].Source.Location, 100_000)
	return s.search(ctx, searchRequest{ExactName: query.Name, Kinds: []string{"street"}, Point: localityBias, Bias: bias, Size: candidateLimit})
}

type searchRequest struct {
	Text, ExactName string
	Kinds, Subtypes []string
	Bias            *places.Viewport
	Point           *geoBias
	Size            int
	Fuzzy           bool
	PreferStreet    bool
	RadiusM         int
}

type geoBias struct {
	Lat, Lon float64
	ScaleM   int
}

func pointBias(point GeoPoint, scale int) *geoBias {
	return &geoBias{Lat: point.Lat, Lon: point.Lon, ScaleM: scale}
}

type searchHit struct {
	ID     string
	Score  float64
	Source Document
}

func (s *Store) search(ctx context.Context, request searchRequest) ([]searchHit, error) {
	if request.Size <= 0 || request.Size > candidateLimit {
		return nil, fmt.Errorf("candidate size outside bounded range")
	}
	body := buildQuery(request)
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	var result struct {
		Hits struct {
			Hits []struct {
				ID     string   `json:"_id"`
				Score  *float64 `json:"_score"`
				Fields struct {
					Location []string `json:"location"`
				} `json:"fields"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if _, err = s.client.request(ctx, http.MethodPost, "/"+url.PathEscape(s.client.Index)+"/_search", "application/json", encoded, &result); err != nil {
		return nil, err
	}
	hits := make([]searchHit, 0, len(result.Hits.Hits))
	for _, hit := range result.Hits.Hits {
		location, locationErr := parseDocValueLocation(hit.Fields.Location)
		if hit.ID == "" || locationErr != nil {
			return nil, fmt.Errorf("OpenSearch hit has invalid ID/location: %q", hit.ID)
		}
		score := 0.0
		if hit.Score != nil {
			score = *hit.Score
		}
		hits = append(hits, searchHit{ID: hit.ID, Score: score, Source: Document{ID: hit.ID, Location: location}})
	}
	return hits, nil
}

func parseDocValueLocation(values []string) (GeoPoint, error) {
	if len(values) != 1 {
		return GeoPoint{}, fmt.Errorf("location values=%d", len(values))
	}
	parts := strings.Split(values[0], ",")
	if len(parts) != 2 {
		return GeoPoint{}, fmt.Errorf("invalid location %q", values[0])
	}
	lat, latErr := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	lon, lonErr := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if latErr != nil || lonErr != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return GeoPoint{}, fmt.Errorf("invalid location %q", values[0])
	}
	return GeoPoint{Lat: lat, Lon: lon}, nil
}

func buildQuery(request searchRequest) map[string]any {
	filters := []any{map[string]any{"term": map[string]any{"closed": false}}}
	if len(request.Kinds) == 1 {
		filters = append(filters, map[string]any{"term": map[string]any{"kind": request.Kinds[0]}})
	} else if len(request.Kinds) > 1 {
		filters = append(filters, map[string]any{"terms": map[string]any{"kind": request.Kinds}})
	}
	if len(request.Subtypes) == 1 {
		filters = append(filters, map[string]any{"term": map[string]any{"subtype": request.Subtypes[0]}})
	} else if len(request.Subtypes) > 1 {
		filters = append(filters, map[string]any{"terms": map[string]any{"subtype": request.Subtypes}})
	}
	if request.RadiusM > 0 && request.Bias != nil {
		center := request.Bias.Center()
		filters = append(filters, map[string]any{"geo_distance": map[string]any{
			"distance": fmt.Sprintf("%dm", request.RadiusM), "location": map[string]float64{"lat": center.Lat, "lon": center.Lng},
		}})
	}
	must := []any{}
	should := []any{}
	if request.ExactName != "" {
		normalized := places.Normalize(request.ExactName)
		must = append(must, map[string]any{"term": map[string]any{"normalized_name": normalized}})
		should = append(should, map[string]any{"term": map[string]any{"normalized_name": map[string]any{"value": normalized, "boost": 120}}})
	} else if request.Fuzzy {
		must = append(must, map[string]any{"multi_match": map[string]any{
			"query": request.Text, "fields": []string{"name^6", "search_text^2"},
			"fuzziness": "AUTO", "prefix_length": 2, "max_expansions": 20, "operator": "and",
		}})
	} else {
		tokens := strings.Fields(request.Text)
		if len(tokens) > 1 {
			must = append(must, map[string]any{"multi_match": map[string]any{
				"query": strings.Join(tokens[:len(tokens)-1], " "), "type": "cross_fields", "operator": "and",
				"fields": []string{"name^6", "search_text^2"},
			}})
		}
		last := tokens[len(tokens)-1]
		must = append(must, map[string]any{"multi_match": map[string]any{
			"query": last, "type": "bool_prefix", "operator": "and",
			"fields": []string{"name^6", "search_text^2", "search_text._2gram^2", "search_text._3gram^2"},
		}})
		should = append(should,
			map[string]any{"term": map[string]any{"normalized_name": map[string]any{"value": request.Text, "boost": 120}}},
			map[string]any{"prefix": map[string]any{"normalized_name": map[string]any{"value": request.Text, "boost": 25}}},
		)
	}
	base := map[string]any{"bool": map[string]any{"filter": filters, "must": must, "should": should}}
	functions := []any{
		map[string]any{"filter": map[string]any{"term": map[string]any{"kind": "area"}}, "weight": 10},
		map[string]any{"filter": map[string]any{"term": map[string]any{"subtype": "region"}}, "weight": 80},
		map[string]any{"field_value_factor": map[string]any{"field": "area_prominence", "factor": 1.5, "missing": 0}},
		map[string]any{"field_value_factor": map[string]any{"field": "settlement_tier", "factor": 6, "missing": 0}},
		map[string]any{"field_value_factor": map[string]any{"field": "destination_class", "factor": 8, "missing": 0}},
		map[string]any{"field_value_factor": map[string]any{"field": "specificity", "factor": 1, "missing": 0}},
		map[string]any{"field_value_factor": map[string]any{"field": "confidence_tier", "factor": 2, "missing": 0}},
	}
	if request.PreferStreet {
		functions = append(functions, map[string]any{"filter": map[string]any{"term": map[string]any{"kind": "street"}}, "weight": 80})
	}
	if request.Point != nil {
		functions = append(functions, geoFunction(request.Point.Lat, request.Point.Lon, request.Point.ScaleM, 300))
	}
	if request.Bias != nil {
		center := request.Bias.Center()
		scale := viewportScale(*request.Bias)
		functions = append(functions, geoFunction(center.Lat, center.Lng, scale, 300))
	}
	query := map[string]any{"function_score": map[string]any{
		"query": base, "functions": functions, "score_mode": "sum", "boost_mode": "sum", "max_boost": 500,
	}}
	return map[string]any{
		"size": request.Size, "track_total_hits": false, "timeout": "1900ms", "query": query,
		// Sorting on _id requires an expensive field-data build at national scale.
		// _doc is a stable tie-breaker for this immutable single-shard index.
		"sort":            []any{map[string]any{"_score": "desc"}, map[string]any{"_doc": "asc"}},
		"_source":         false,
		"docvalue_fields": []string{"location"},
	}
}

func geoFunction(lat, lon float64, scaleMeters, weight int) map[string]any {
	return map[string]any{
		"gauss": map[string]any{"location": map[string]any{
			"origin": map[string]float64{"lat": lat, "lon": lon}, "scale": fmt.Sprintf("%dm", scaleMeters), "offset": "0m", "decay": 0.5,
		}}, "weight": weight,
	}
}

func viewportScale(v places.Viewport) int {
	center := v.Center()
	latM := math.Abs(v.North-v.South) * 111_195
	lngSpan := v.East - v.West
	if lngSpan < 0 {
		lngSpan += 360
	}
	lngM := math.Abs(lngSpan) * 111_195 * math.Max(0.05, math.Abs(math.Cos(center.Lat*math.Pi/180)))
	scale := int(math.Max(latM, lngM) / 2)
	if scale < 2_000 {
		scale = 2_000
	}
	if scale > 1_000_000 {
		scale = 1_000_000
	}
	return scale
}

func mergeHits(primary, fallback []searchHit) []searchHit {
	seen := map[string]bool{}
	out := make([]searchHit, 0, len(primary)+len(fallback))
	for _, group := range [][]searchHit{primary, fallback} {
		for _, hit := range group {
			if !seen[hit.ID] {
				seen[hit.ID] = true
				out = append(out, hit)
			}
		}
	}
	// Fallback hits never move ahead of strict hits; sort only equal-score runs
	// to keep a deterministic ID tie-breaker when a mock or future server omits sort.
	for start := 0; start < len(primary); {
		end := start + 1
		for end < len(primary) && out[end].Score == out[start].Score {
			end++
		}
		sort.SliceStable(out[start:end], func(i, j int) bool { return out[start+i].ID < out[start+j].ID })
		start = end
	}
	return out
}

// mergeRankedHits combines independently scored local and global pools. A hit
// seen locally keeps its geo-boosted score, while a sufficiently strong global
// hit can still outrank it. This makes the radius an acceleration device rather
// than a public-result boundary.
func mergeRankedHits(local, global []searchHit) []searchHit {
	byID := make(map[string]searchHit, len(local)+len(global))
	for _, group := range [][]searchHit{local, global} {
		for _, hit := range group {
			if existing, ok := byID[hit.ID]; !ok || hit.Score > existing.Score {
				byID[hit.ID] = hit
			}
		}
	}
	merged := make([]searchHit, 0, len(byID))
	for _, hit := range byID {
		merged = append(merged, hit)
	}
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].Score != merged[j].Score {
			return merged[i].Score > merged[j].Score
		}
		return merged[i].ID < merged[j].ID
	})
	return merged
}
