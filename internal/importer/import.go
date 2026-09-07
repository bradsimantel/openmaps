// Package importer builds SQLite from normalized records produced by concrete
// source adapters. It never translates Google wire types.
package importer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"os"
	"sort"
	"strings"

	"openmaps/internal/places"
)

//go:embed schema.sql
var schema string

type Record struct {
	Source       string                     `json:"source"`
	SourceID     string                     `json:"source_id"`
	Release      string                     `json:"release"`
	Kind         string                     `json:"kind"`
	Priority     int                        `json:"priority"`
	Attributes   map[string]json.RawMessage `json:"attributes"`
	Paths        map[string]string          `json:"paths"`
	Raw          json.RawMessage            `json:"raw"`
	Attributions []places.Attribution       `json:"attributions"`
}

func (r Record) Key() string { return r.Source + ":" + r.SourceID }

type Relationship struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Kind     string `json:"kind"`
	Evidence string `json:"evidence"`
}
type Bundle struct {
	Schema        int               `json:"schema"`
	Manifest      json.RawMessage   `json:"manifest"`
	Identities    map[string]string `json:"identities"`
	Records       []Record          `json:"records"`
	Relationships []Relationship    `json:"relationships"`
}

// PublicID bootstraps identity from an immutable, source-qualified anchor. It
// does not depend on attributes, release, import order, or SQLite row IDs.
func PublicID(anchor string) string {
	h := sha256.Sum256([]byte("openmaps:entity:v1:" + anchor))
	return "om_" + hex.EncodeToString(h[:16])
}
func serialized(v any) string { b, _ := json.Marshal(v); return string(b) }
func validLocation(p places.Location) bool {
	return !math.IsNaN(p.Lat) && !math.IsNaN(p.Lng) && !math.IsInf(p.Lat, 0) && !math.IsInf(p.Lng, 0) && p.Lat >= -90 && p.Lat <= 90 && p.Lng >= -180 && p.Lng <= 180
}

// Build creates a new database, refusing to overwrite anything. The command
// writes a temporary sibling and publishes only after this validation succeeds.
func Build(ctx context.Context, path string, b Bundle) (err error) {
	if b.Schema != 1 || len(b.Records) == 0 || !json.Valid(b.Manifest) || string(b.Manifest) == "null" {
		return fmt.Errorf("unsupported or empty bundle")
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	file.Close()
	defer func() {
		if err != nil {
			os.Remove(path)
		}
	}()
	u := url.URL{Scheme: "file", Path: path}
	q := u.Query()
	q.Add("_pragma", "foreign_keys(1)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err = db.ExecContext(ctx, schema); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO metadata VALUES('manifest',?)", string(b.Manifest)); err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO metadata VALUES('identities',?)", serialized(b.Identities)); err != nil {
		return err
	}
	groups := map[string][]Record{}
	ids := map[string]string{}
	kinds := map[string]string{}
	for _, r := range b.Records {
		if r.Source == "" || r.SourceID == "" || r.Release == "" || len(r.Raw) == 0 || len(r.Attributions) == 0 {
			return fmt.Errorf("incomplete provenance: %s", r.Key())
		}
		if _, ok := ids[r.Key()]; ok {
			return fmt.Errorf("duplicate source key: %s", r.Key())
		}
		anchor := r.Key()
		if target, ok := b.Identities[anchor]; ok {
			if target == "" {
				return fmt.Errorf("empty identity anchor")
			}
			if next, ok := b.Identities[target]; ok && next != target {
				return fmt.Errorf("identity chains are not allowed")
			}
			anchor = target
		}
		id := PublicID(anchor)
		ids[r.Key()] = id
		if kind, ok := kinds[id]; ok && kind != r.Kind {
			return fmt.Errorf("cross-kind identity merge: %s", anchor)
		}
		kinds[id] = r.Kind
		groups[id] = append(groups[id], r)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, id := range keys {
		rs := groups[id]
		sort.Slice(rs, func(i, j int) bool {
			if rs[i].Priority != rs[j].Priority {
				return rs[i].Priority > rs[j].Priority
			}
			return rs[i].Key() < rs[j].Key()
		})
		values := map[string]json.RawMessage{}
		winners := map[string]Record{}
		attrs := []places.Attribution{}
		attrSeen := map[string]bool{}
		for _, r := range rs {
			for k, v := range r.Attributes {
				switch k {
				case "name", "address", "website", "subtype", "location", "closed", "aliases":
				default:
					return fmt.Errorf("unknown normalized attribute: %s", k)
				}
				if string(v) == "null" || string(v) == `""` || string(v) == "[]" {
					continue
				}
				if r.Paths[k] == "" {
					return fmt.Errorf("missing attribute provenance: %s %s", r.Key(), k)
				}
				if _, ok := values[k]; !ok {
					values[k] = v
					winners[k] = r
				}
			}
			for _, a := range r.Attributions {
				k := serialized(a)
				if !attrSeen[k] {
					attrs = append(attrs, a)
					attrSeen[k] = true
				}
			}
		}
		var name, address, website, subtype string
		var p places.Location
		var closed bool
		var aliases []string
		for k, dst := range map[string]any{"name": &name, "address": &address, "website": &website, "subtype": &subtype, "location": &p, "closed": &closed, "aliases": &aliases} {
			if v, ok := values[k]; ok {
				if err = json.Unmarshal(v, dst); err != nil {
					return fmt.Errorf("attribute %s: %w", k, err)
				}
			}
		}
		if strings.TrimSpace(name) == "" || values["location"] == nil || !validLocation(p) {
			return fmt.Errorf("invalid name/location: %s", id)
		}
		var coordinates struct {
			Lat *float64 `json:"lat"`
			Lng *float64 `json:"lng"`
		}
		if err = json.Unmarshal(values["location"], &coordinates); err != nil || coordinates.Lat == nil || coordinates.Lng == nil {
			return fmt.Errorf("location requires both lat and lng: %s", id)
		}
		if website != "" {
			u, e := url.Parse(website)
			if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return fmt.Errorf("invalid website: %s", id)
			}
		}
		result, e := tx.Exec(`INSERT INTO entities(id,kind,name,normalized_name,address,website,subtype,lat,lng,closed,attributions) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, id, rs[0].Kind, name, places.Normalize(name), address, website, subtype, p.Lat, p.Lng, closed, serialized(attrs))
		if e != nil {
			return e
		}
		rowid, e := result.LastInsertId()
		if e != nil {
			return e
		}
		if _, err = tx.Exec("INSERT INTO entity_fts(rowid,name,address,aliases) VALUES(?,?,?,?)", rowid, places.Normalize(name), places.Normalize(address), places.Normalize(strings.Join(aliases, " "))); err != nil {
			return err
		}
		for _, r := range rs {
			if _, err = tx.Exec("INSERT INTO source_records VALUES(?,?,?,?,?,?,?,?,?)", r.Key(), id, r.Source, r.SourceID, r.Release, r.Priority, serialized(r.Attributes), serialized(r.Paths), string(r.Raw)); err != nil {
				return err
			}
		}
		for k, r := range winners {
			if _, err = tx.Exec("INSERT INTO attribute_provenance VALUES(?,?,?,?)", id, k, r.Key(), r.Paths[k]); err != nil {
				return err
			}
		}
	}
	for _, r := range b.Relationships {
		from, to := ids[r.From], ids[r.To]
		if from == "" || to == "" || from == to {
			return fmt.Errorf("invalid relationship: %+v", r)
		}
		if r.Kind == "address" && (kinds[from] != "business" || kinds[to] != "address") {
			return fmt.Errorf("invalid address relationship")
		}
		if r.Kind == "parent_area" && (kinds[from] != "area" || kinds[to] != "area") {
			return fmt.Errorf("invalid area relationship")
		}
		if _, err = tx.Exec("INSERT OR IGNORE INTO relationships VALUES(?,?,?,?)", from, to, r.Kind, r.Evidence); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	var check string
	if err = db.QueryRow("PRAGMA integrity_check").Scan(&check); err != nil {
		return err
	}
	if check != "ok" {
		return fmt.Errorf("integrity: %s", check)
	}
	rows, err := db.Query("PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		return fmt.Errorf("foreign key check failed")
	}
	return rows.Err()
}
