package routing

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	_ "modernc.org/sqlite"
	"net/url"
	"path/filepath"
	"slices"
)

// Load reads an optional independently versioned graph. Absence is supported for
// retained schema-1 snapshots; partial/corrupt routing data is never treated as absence.
func Load(ctx context.Context, db *sql.DB) (*Store, *Summary, error) {
	var exists int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='routing_graph'").Scan(&exists); err != nil {
		return nil, nil, err
	}
	if exists == 0 {
		return nil, nil, nil
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM routing_graph").Scan(&count); err != nil {
		return nil, nil, err
	}
	if count != 1 {
		return nil, nil, fmt.Errorf("routing graph must have one payload")
	}
	var raw []byte
	var sum string
	if err := db.QueryRowContext(ctx, "SELECT data,sha256 FROM routing_graph WHERE id=1").Scan(&raw, &sum); err != nil {
		return nil, nil, err
	}
	if Digest(raw) != sum {
		return nil, nil, fmt.Errorf("routing graph checksum mismatch")
	}
	var d Data
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, nil, err
	}
	if len(d.Metadata.SourceSHA256) != 64 || d.Metadata.Release == "" || d.Metadata.URL == "" || d.Metadata.Attribution == "" {
		return nil, nil, fmt.Errorf("missing routing provenance")
	}
	sources := map[string]map[int64]bool{"way": {}, "node": {}, "relation": {}}
	for _, source := range d.Sources {
		ids, ok := sources[source.Kind]
		if !ok || source.ID <= 0 || source.Version < 0 || !json.Valid(source.Raw) || string(source.Raw) == "null" || source.Decision == "" || ids[source.ID] {
			return nil, nil, fmt.Errorf("invalid routing source reference")
		}
		ids[source.ID] = true
	}
	for _, segment := range d.Segments {
		if !sources["way"][segment.Way] {
			return nil, nil, fmt.Errorf("routing segment without source way")
		}
	}
	for _, g := range d.Guards {
		if !sources["way"][g.Way] {
			return nil, nil, fmt.Errorf("snap guard without source way")
		}
	}
	for _, ban := range d.Bans {
		if !sources["relation"][ban.Relation] {
			return nil, nil, fmt.Errorf("routing ban without source relation")
		}
	}
	for _, w := range d.Access.Ways {
		if !sources["way"][w.Way] {
			return nil, nil, fmt.Errorf("access way without source")
		}
	}
	for _, a := range d.Access.Areas {
		if !sources["way"][a.Way] {
			return nil, nil, fmt.Errorf("access area without source")
		}
	}
	for _, e := range d.Access.Entrances {
		if !sources["node"][e.Node] {
			return nil, nil, fmt.Errorf("access entrance without source")
		}
	}
	// Association fields must agree with retained source tags and node identities.
	accessWays := map[int64]AccessWay{}
	accessAreas := map[int64]AccessArea{}
	entrances := map[int64]AccessEntrance{}
	for _, w := range d.Access.Ways {
		accessWays[w.Way] = w
	}
	for _, a := range d.Access.Areas {
		accessAreas[a.Way] = a
	}
	for _, e := range d.Access.Entrances {
		entrances[e.Node] = e
	}
	for _, source := range d.Sources {
		if source.Kind == "way" {
			w, wok := accessWays[source.ID]
			a, aok := accessAreas[source.ID]
			if !wok && !aok {
				continue
			}
			var original struct {
				Nodes []int64
				Tags  map[string]string
			}
			if err := json.Unmarshal(source.Raw, &original); err != nil {
				return nil, nil, err
			}
			if wok && (w.Name != original.Tags["name"] || w.Driveway && (original.Tags["service"] != "driveway" || original.Tags["highway"] != "service") || len(w.Nodes) > 0 && !slices.Equal(w.Nodes, original.Nodes)) {
				return nil, nil, fmt.Errorf("access way differs from source")
			}
			if aok && (!slices.Equal(a.Nodes, original.Nodes) || a.Number != original.Tags["addr:housenumber"] || a.Street != original.Tags["addr:street"] || a.Parking != (original.Tags["amenity"] == "parking")) {
				return nil, nil, fmt.Errorf("access area differs from source")
			}
		}
		if source.Kind == "node" {
			if _, ok := entrances[source.ID]; ok {
				var original struct{ Tags map[string]string }
				if err := json.Unmarshal(source.Raw, &original); err != nil {
					return nil, nil, err
				}
				if original.Tags["amenity"] != "parking_entrance" {
					return nil, nil, fmt.Errorf("parking entrance differs from source")
				}
			}
		}
	}
	store, err := New(d)
	if err != nil {
		return nil, nil, err
	}
	return store, &Summary{d.Metadata, sum}, nil
}
func Open(ctx context.Context, path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	defer db.Close()
	store, _, err := Load(ctx, db)
	return store, err
}
