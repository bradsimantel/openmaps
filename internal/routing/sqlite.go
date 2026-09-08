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

// graphReader allows validation of an unpublished snapshot transaction.
type graphReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Load reads an optional independently versioned graph. Absence is supported for
// retained schema-1 snapshots; partial/corrupt routing data is never treated as absence.
func Load(ctx context.Context, db graphReader) (*Store, *Summary, error) {
	var exists int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='routing_graph'").Scan(&exists); err != nil {
		return nil, nil, err
	}
	if exists == 0 {
		var chunks int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name='routing_chunks'").Scan(&chunks); err != nil {
			return nil, nil, err
		}
		if chunks != 0 {
			return nil, nil, fmt.Errorf("routing chunks without manifest")
		}
		return nil, nil, nil
	}
	done := loadPhase(ctx, "decoding")
	d, m, sum, err := readGraphData(ctx, db, false)
	done()
	if err != nil {
		return nil, nil, err
	}
	visit := func(f func(Source) error) error {
		if m.Layout == "" {
			for _, v := range d.Sources {
				if err := f(v); err != nil {
					return err
				}
			}
			return nil
		}
		for _, c := range m.Chunks {
			if c.Kind == "sources" {
				values, err := readChunk[Source](ctx, db, c)
				if err != nil {
					return err
				}
				for _, v := range values {
					if err := f(v); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	done = loadPhase(ctx, "provenance validation")
	err = validateProvenance(d, visit)
	done()
	if err != nil {
		return nil, nil, err
	}
	store, err := newStoreContext(ctx, d, true)
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	store.graphSHA = sum
	store.sourceLayout, store.sourcePreprocessing = m.Layout, m.Preprocessing
	return store, &Summary{Metadata: d.Metadata, SHA256: sum, Layout: m.Layout, Preprocessing: m.Preprocessing}, nil
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

func validateProvenance(d Data, visit func(func(Source) error) error) error {
	if len(d.Metadata.SourceSHA256) != 64 || d.Metadata.Release == "" || d.Metadata.URL == "" || d.Metadata.Attribution == "" {
		return fmt.Errorf("missing routing provenance")
	}
	required := map[string]map[int64]bool{"way": {}, "node": {}, "relation": {}}
	for _, v := range d.Costs {
		required["way"][v.Way] = false
	}
	for _, v := range d.Segments {
		required["way"][v.Way] = false
	}
	for _, v := range d.Guards {
		required["way"][v.Way] = false
	}
	for _, v := range d.Bans {
		required["relation"][v.Relation] = false
	}
	accessWays := map[int64]AccessWay{}
	accessAreas := map[int64]AccessArea{}
	entrances := map[int64]bool{}
	for _, v := range d.Access.Ways {
		accessWays[v.Way] = v
		required["way"][v.Way] = false
	}
	for _, v := range d.Access.Areas {
		accessAreas[v.Way] = v
		required["way"][v.Way] = false
	}
	for _, v := range d.Access.Entrances {
		entrances[v.Node] = true
		required["node"][v.Node] = false
	}
	seen := map[string]map[int64]bool{"way": {}, "node": {}, "relation": {}}
	err := visit(func(source Source) error {
		ids, ok := seen[source.Kind]
		if !ok || source.ID <= 0 || source.Version < 0 || !json.Valid(source.Raw) || string(source.Raw) == "null" || source.Decision == "" || ids[source.ID] {
			return fmt.Errorf("invalid routing source reference")
		}
		ids[source.ID] = true
		if _, ok := required[source.Kind][source.ID]; ok {
			required[source.Kind][source.ID] = true
		}
		if source.Kind == "way" {
			w, wok := accessWays[source.ID]
			a, aok := accessAreas[source.ID]
			if wok || aok {
				var original struct {
					Nodes []int64
					Tags  map[string]string
				}
				if err := json.Unmarshal(source.Raw, &original); err != nil {
					return err
				}
				if wok && (w.Name != original.Tags["name"] || w.Driveway && (original.Tags["service"] != "driveway" || original.Tags["highway"] != "service") || len(w.Nodes) > 0 && !slices.Equal(w.Nodes, original.Nodes)) {
					return fmt.Errorf("access way differs from source")
				}
				if aok && (!slices.Equal(a.Nodes, original.Nodes) || a.Number != original.Tags["addr:housenumber"] || a.Street != original.Tags["addr:street"] || a.Parking != (original.Tags["amenity"] == "parking")) {
					return fmt.Errorf("access area differs from source")
				}
			}
		}
		if source.Kind == "node" && entrances[source.ID] {
			var original struct{ Tags map[string]string }
			if err := json.Unmarshal(source.Raw, &original); err != nil {
				return err
			}
			if original.Tags["amenity"] != "parking_entrance" {
				return fmt.Errorf("parking entrance differs from source")
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for kind, ids := range required {
		for id, ok := range ids {
			if !ok {
				return fmt.Errorf("routing %s %d without source", kind, id)
			}
		}
	}
	return nil
}

// OpenRuntime supports lookup-only snapshots, but requires explicit offline
// preparation for routing. Legacy reconstruction is a caller-selected mode.
func OpenRuntime(ctx context.Context, path, preparedDir string, legacy bool, cacheDir string) (*Store, error) {
	if legacy {
		if cacheDir != "" {
			return OpenMapped(ctx, path, cacheDir)
		}
		return Open(ctx, path)
	}
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
	var count int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('routing_graph','routing_chunks')").Scan(&count); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	if preparedDir == "" {
		return nil, fmt.Errorf("routing snapshot requires -routing-prepared; run cmd/routing-prepare offline or explicitly select -routing-legacy-load")
	}
	return OpenPrepared(ctx, path, preparedDir)
}
