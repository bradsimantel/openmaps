// Package places owns lookup and autocomplete over provider-independent entities.
package places

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
	_ "modernc.org/sqlite"
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
type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Set("mode", "ro")
	q.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	var version string
	if err = db.QueryRow("SELECT value FROM metadata WHERE key='schema_version'").Scan(&version); err != nil || version != "1" {
		db.Close()
		return nil, fmt.Errorf("database missing or unsupported schema: %v", err)
	}
	return &Store{db: db}, nil
}
func (s *Store) Close() error { return s.db.Close() }

// Normalize makes search punctuation-, case-, and accent-insensitive. Only
// letters/numbers survive; user input can never inject FTS operators.
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

const columns = "e.id,e.kind,e.name,e.address,e.website,e.subtype,e.lat,e.lng,e.attributions"

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Entity, error) {
	var e Entity
	var attrs string
	err := row.Scan(&e.ID, &e.Kind, &e.Name, &e.Address, &e.Website, &e.Subtype, &e.Location.Lat, &e.Location.Lng, &attrs)
	if err != nil {
		return e, err
	}
	err = json.Unmarshal([]byte(attrs), &e.Attributions)
	return e, err
}
func (s *Store) Details(ctx context.Context, id string) (Entity, error) {
	return scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM entities e WHERE e.id=?", id))
}

func (s *Store) Autocomplete(ctx context.Context, input string) ([]Entity, error) {
	normalized := Normalize(input)
	out := []Entity{}
	if normalized == "" {
		return out, nil
	}
	tokens := strings.Fields(normalized)
	for i, t := range tokens {
		tokens[i] = "\"" + t + "\"*"
	}
	// Exact name and name-prefix matches precede address-context matches. FTS
	// ranks remaining matches; IDs break ties deterministically across rebuilds.
	rows, err := s.db.QueryContext(ctx, "SELECT "+columns+` FROM entity_fts
 JOIN entities e ON e.rowid=entity_fts.rowid WHERE entity_fts MATCH ? AND e.closed=0
 ORDER BY CASE WHEN e.normalized_name=? THEN 0 WHEN e.normalized_name LIKE ? THEN 1 ELSE 2 END,
 CASE WHEN e.kind='area' THEN 0 WHEN e.kind='street' THEN 1 WHEN e.kind='business' THEN 2 ELSE 3 END,
 bm25(entity_fts,10.0,1.0,5.0),e.id`, strings.Join(tokens, " AND "), normalized, normalized+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seenStreets := map[string]bool{}
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, err
		}
		// Segments retain individual stable identities; collapse repeated street
		// labels only in suggestions, using the first ranked segment as representative.
		if e.Kind == "street" {
			k := Normalize(e.Name)
			if seenStreets[k] {
				continue
			}
			seenStreets[k] = true
		}
		out = append(out, e)
		if len(out) == 5 {
			break
		}
	}
	return out, rows.Err()
}
