// Package api translates supported Google Places REST v1 and Geocoding v3 subsets.
package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"openmaps/internal/geocoding"
	"openmaps/internal/places"
)

type Handler struct {
	Places    *places.Store
	Geocoding *geocoding.Store
}
type object = map[string]any

func write(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func failure(w http.ResponseWriter, code int, status, message string) {
	write(w, code, object{"error": object{"code": code, "status": status, "message": message}})
}
func invalid(w http.ResponseWriter, err error) { failure(w, 400, "INVALID_ARGUMENT", err.Error()) }
func method(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	failure(w, 405, "INVALID_ARGUMENT", "Method must be "+allowed)
}

var tokenRE = regexp.MustCompile(`^[A-Za-z0-9_-]{0,36}$`)

func options(language, token string) error {
	if language != "" && language != "en" && language != "en-US" {
		return fmt.Errorf("supported languageCode values: en, en-US")
	}
	if !tokenRE.MatchString(token) {
		return fmt.Errorf("invalid sessionToken: use at most 36 URL-safe ASCII characters")
	}
	return nil
}
func (h Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/maps/api/geocode/json" {
		h.geocode(w, r)
		return
	}
	if r.URL.Path == "/v1/places:autocomplete" {
		if r.Method != "POST" {
			method(w, "POST")
			return
		}
		h.autocomplete(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/v1/places/") {
		if r.Method != "GET" {
			method(w, "GET")
			return
		}
		h.details(w, r)
		return
	}
	failure(w, 404, "NOT_FOUND", "Unknown endpoint")
}

func parameters(r *http.Request, details bool) (string, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", err
	}
	for k, v := range q {
		if len(v) != 1 {
			return "", fmt.Errorf("duplicate query parameter: %s", k)
		}
		switch k {
		case "key", "fields", "$fields":
		case "languageCode", "sessionToken":
			if !details {
				return "", fmt.Errorf("%s belongs in the JSON body", k)
			}
		default:
			return "", fmt.Errorf("unsupported query parameter: %s", k)
		}
	}
	if details {
		if err := options(q.Get("languageCode"), q.Get("sessionToken")); err != nil {
			return "", err
		}
	}
	masks := []string{}
	if v, ok := r.Header["X-Goog-Fieldmask"]; ok {
		masks = append(masks, v...)
	}
	for _, k := range []string{"fields", "$fields"} {
		if v, ok := q[k]; ok {
			masks = append(masks, v...)
		}
	}
	if len(masks) > 1 {
		return "", fmt.Errorf("supply exactly one field mask source")
	}
	if len(masks) == 1 {
		if masks[0] == "" {
			return "", fmt.Errorf("field mask cannot be empty")
		}
		return masks[0], nil
	}
	if details {
		return "", fmt.Errorf("a field mask is required")
	}
	return "*", nil
}

func (h Handler) autocomplete(w http.ResponseWriter, r *http.Request) {
	mask, err := parameters(r, false)
	if err != nil {
		invalid(w, err)
		return
	}
	paths, err := parseMask(mask, autocompletePaths)
	if err != nil {
		invalid(w, err)
		return
	}
	var fields map[string]json.RawMessage
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
	if err = dec.Decode(&fields); err != nil {
		invalid(w, fmt.Errorf("invalid request JSON: %w", err))
		return
	}
	var req struct{ Input, Language, Token string }
	for key, value := range fields {
		var target *string
		switch key {
		case "input":
			target = &req.Input
		case "languageCode":
			target = &req.Language
		case "sessionToken":
			target = &req.Token
		default:
			invalid(w, fmt.Errorf("unsupported request field: %s", key))
			return
		}
		if err = json.Unmarshal(value, target); err != nil {
			invalid(w, fmt.Errorf("%s must be a string", key))
			return
		}
	}
	if err = dec.Decode(&struct{}{}); err != io.EOF {
		invalid(w, fmt.Errorf("expected exactly one JSON object"))
		return
	}
	if err = options(req.Language, req.Token); err != nil {
		invalid(w, err)
		return
	}
	if strings.TrimSpace(req.Input) == "" || !utf8.ValidString(req.Input) || utf8.RuneCountInString(req.Input) > 200 {
		invalid(w, fmt.Errorf("input must contain 1–200 Unicode characters"))
		return
	}
	result, err := h.Places.Autocomplete(r.Context(), req.Input)
	if err != nil {
		log.Printf("autocomplete: %v", err)
		failure(w, 500, "INTERNAL", "Search unavailable")
		return
	}
	suggestions := []any{}
	for _, p := range result {
		text := p.Name
		structured := object{"mainText": object{"text": p.Name}}
		if p.Address != "" && p.Address != p.Name {
			text = p.Name + ", " + p.Address
			if p.Kind == "address" {
				text = p.Address
			}
			structured["secondaryText"] = object{"text": p.Address}
		}
		suggestions = append(suggestions, object{"placePrediction": object{"place": "places/" + p.ID, "placeId": p.ID, "text": object{"text": text}, "structuredFormat": structured, "types": types(p)}})
	}
	write(w, 200, project(object{"suggestions": suggestions}, paths))
}
func types(p places.Entity) []string {
	switch p.Kind {
	case "business":
		return []string{"establishment", "point_of_interest"}
	case "address":
		return []string{"street_address"}
	case "street":
		return []string{"route"}
	case "area":
		if p.Subtype == "locality" {
			return []string{"locality", "political"}
		}
		return []string{"political"}
	}
	return []string{}
}
func (h Handler) details(w http.ResponseWriter, r *http.Request) {
	mask, err := parameters(r, true)
	if err != nil {
		invalid(w, err)
		return
	}
	paths, err := parseMask(mask, detailsPaths)
	if err != nil {
		invalid(w, err)
		return
	}
	var probe [1]byte
	if n, e := r.Body.Read(probe[:]); n != 0 || (e != nil && e != io.EOF) {
		invalid(w, fmt.Errorf("details request body must be empty"))
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/places/")
	if id == "" || strings.Contains(id, "/") {
		failure(w, 404, "NOT_FOUND", "Place not found")
		return
	}
	p, err := h.Places.Details(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		failure(w, 404, "NOT_FOUND", "Place not found")
		return
	}
	if err != nil {
		log.Printf("details: %v", err)
		failure(w, 500, "INTERNAL", "Details unavailable")
		return
	}
	attrs := []any{}
	for _, a := range p.Attributions {
		attrs = append(attrs, object{"provider": a.Provider, "providerUri": a.URI})
	}
	result := object{"id": p.ID, "name": "places/" + p.ID, "displayName": object{"text": p.Name}, "location": object{"latitude": p.Location.Lat, "longitude": p.Location.Lng}, "types": types(p), "attributions": attrs}
	if p.Address != "" {
		result["formattedAddress"] = p.Address
	}
	if p.Website != "" {
		result["websiteUri"] = p.Website
	}
	write(w, 200, project(result, paths))
}

var detailsPaths = []string{"id", "name", "displayName.text", "formattedAddress", "location.latitude", "location.longitude", "types", "websiteUri", "attributions.provider", "attributions.providerUri"}
var autocompletePaths = []string{"suggestions.placePrediction.place", "suggestions.placePrediction.placeId", "suggestions.placePrediction.text.text", "suggestions.placePrediction.structuredFormat.mainText.text", "suggestions.placePrediction.structuredFormat.secondaryText.text", "suggestions.placePrediction.types"}

func parseMask(mask string, supported []string) ([]string, error) {
	if mask == "*" {
		return nil, nil
	}
	paths := strings.Split(mask, ",")
	for _, p := range paths {
		valid := false
		if p == "" || strings.IndexFunc(p, unicode.IsSpace) >= 0 {
			return nil, fmt.Errorf("invalid field mask")
		}
		for _, s := range supported {
			if s == p || strings.HasPrefix(s, p+".") {
				valid = true
				break
			}
		}
		if !valid {
			return nil, fmt.Errorf("unsupported field mask path: %s", p)
		}
	}
	return paths, nil
}

// project applies masks recursively through both JSON objects and arrays.
func project(v any, paths []string) any {
	if len(paths) == 0 {
		return v
	}
	switch value := v.(type) {
	case []any:
		out := make([]any, 0, len(value))
		for _, item := range value {
			out = append(out, project(item, paths))
		}
		return out
	case object:
		out := object{}
		for k, child := range value {
			all := false
			tails := []string{}
			for _, p := range paths {
				if p == k {
					all = true
				}
				if strings.HasPrefix(p, k+".") {
					tails = append(tails, strings.TrimPrefix(p, k+"."))
				}
			}
			if all {
				out[k] = child
			} else if len(tails) > 0 {
				out[k] = project(child, tails)
			}
		}
		return out
	default:
		return v
	}
}
