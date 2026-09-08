package api

import (
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"openmaps/internal/geocoding"
	"openmaps/internal/places"
)

func geocodeError(w http.ResponseWriter, code int, status, message, outcome string) {
	write(w, code, object{"status": status, "results": []any{}, "error_message": message, "openmaps": object{"outcome": outcome}})
}
func (h Handler) geocode(w http.ResponseWriter, r *http.Request) {
	invalid := func(message string) {
		geocodeError(w, 200, "INVALID_REQUEST", message, "unsupported_or_invalid_request")
	}
	if r.Method != "GET" {
		w.Header().Set("Allow", "GET")
		geocodeError(w, 405, "INVALID_REQUEST", "Method must be GET", "unsupported_or_invalid_request")
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		invalid("Malformed query string")
		return
	}
	for k, v := range q {
		if len(v) != 1 {
			invalid("Duplicate query parameter: " + k)
			return
		}
		switch k {
		case "address", "latlng", "key":
		case "language":
			if v[0] != "en" && v[0] != "en-US" {
				invalid("Only language=en or en-US is supported")
				return
			}
		default:
			invalid("Unsupported query parameter: " + k)
			return
		}
	}
	if _, ok := r.Header["X-Goog-Fieldmask"]; ok {
		invalid("Geocoding field masks are unsupported")
		return
	}
	if q.Has("address") == q.Has("latlng") {
		invalid("Supply exactly one of address or latlng")
		return
	}
	var probe [1]byte
	if n, e := r.Body.Read(probe[:]); n != 0 || (e != nil && e != io.EOF) {
		invalid("GET request body must be empty")
		return
	}
	if h.Geocoding == nil {
		geocodeError(w, 503, "UNKNOWN_ERROR", "Geocoding unavailable", "unavailable")
		return
	}
	var response geocoding.Response
	reverse := q.Has("latlng")
	if reverse {
		parts := strings.Split(q.Get("latlng"), ",")
		if len(parts) != 2 {
			invalid("latlng requires latitude,longitude")
			return
		}
		lat, e1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		lng, e2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if e1 != nil || e2 != nil {
			invalid("latlng requires numeric latitude,longitude")
			return
		}
		response, err = h.Geocoding.Reverse(r.Context(), places.Location{Lat: lat, Lng: lng})
	} else {
		response, err = h.Geocoding.Forward(r.Context(), q.Get("address"))
	}
	if err != nil {
		if response.Outcome == "invalid_input" || response.Outcome == "unsupported_input" {
			geocodeError(w, 200, "INVALID_REQUEST", err.Error(), response.Outcome)
			return
		}
		log.Printf("geocoding: %v", err)
		geocodeError(w, 500, "UNKNOWN_ERROR", "Geocoding unavailable", "unavailable")
		return
	}
	results := []any{}
	for _, r := range response.Results {
		p := r.Entity
		extension := object{"precision": "source_address_point", "unit_precision": "unknown", "attributions": p.Attributions}
		if r.DistanceMeters != nil {
			extension["distance_meters"] = *r.DistanceMeters
		}
		if r.Partial {
			extension["context_note"] = "Newport is the preview scope; source address locality is missing and was not verified"
		}
		formatted := p.Address
		if formatted == "" {
			formatted = p.Name
		}
		result := object{"place_id": p.ID, "formatted_address": formatted, "types": []string{"street_address"}, "address_components": geocodingComponents(r.Components), "geometry": object{"location": p.Location, "location_type": "APPROXIMATE"}, "openmaps": extension}
		if r.Partial {
			result["partial_match"] = true
		}
		results = append(results, result)
	}
	status := "OK"
	if len(results) == 0 {
		status = "ZERO_RESULTS"
	}
	extension := object{"outcome": response.Outcome, "candidate_count": len(results)}
	if reverse {
		extension["reverse_limit_meters"] = geocoding.ReverseLimitMeters
	}
	write(w, 200, object{"status": status, "results": results, "openmaps": extension})
}

func geocodingComponents(c places.AddressComponents) []any {
	out := []any{}
	add := func(value, long string, types ...string) {
		if value != "" {
			out = append(out, object{"long_name": long, "short_name": value, "types": types})
		}
	}
	add(c.Number, c.Number, "street_number")
	add(c.Street, c.Street, "route")
	add(c.Locality, c.Locality, "locality", "political")
	region, country := c.Region, c.Country
	if c.Country == "US" {
		country = "United States"
		if c.Region == "RI" {
			region = "Rhode Island"
		}
	}
	add(c.Region, region, "administrative_area_level_1", "political")
	add(c.Country, country, "country", "political")
	add(c.Postcode, c.Postcode, "postal_code")
	return out
}
