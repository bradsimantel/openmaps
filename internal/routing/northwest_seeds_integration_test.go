//go:build integration

package routing

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"os"
	"strings"
	"testing"
)

// Selects source-adjacent endpoints by independently chosen city/road geography,
// before measuring any route. Output is a review artifact, never test expectations.
func TestNorthwestSourceSeeds(t *testing.T) {
	path, out := os.Getenv("OPENMAPS_NORTHWEST_DB"), os.Getenv("OPENMAPS_NORTHWEST_SEEDS")
	if path == "" || out == "" {
		t.Skip("set Northwest DB and a new seed output")
	}
	ctx := context.Background()
	type seed struct {
		Name, Road string
		Near       Point
	}
	seeds := []seed{
		{"Seattle", "4th Avenue", Point{-122.3321, 47.6062}},
		{"Spokane", "West Riverside Avenue", Point{-117.425, 47.658}},
		{"Boise", "West Front Street", Point{-116.203, 43.615}},
		{"Coeur d'Alene", "East Sherman Avenue", Point{-116.783, 47.673}},
		{"Yakima", "East Yakima Avenue", Point{-120.5, 46.603}},
		{"Wenatchee", "North Wenatchee Avenue", Point{-120.312, 47.427}},
		{"Bellingham", "West Holly Street", Point{-122.481, 48.751}},
		{"Lewiston", "Main Street", Point{-117.017, 46.418}},
		{"McCall", "East Lake Street", Point{-116.098, 44.91}},
		{"Idaho Falls", "West Broadway Street", Point{-112.041, 43.492}},
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manifest, _, _, err := readManifest(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	tags := map[int64]map[string]string{}
	for _, c := range manifest.Chunks {
		if c.Kind == "sources" {
			values, err := readChunk[Source](ctx, db, c)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range values {
				if v.Kind != "way" {
					continue
				}
				wanted := false
				for _, seed := range seeds {
					wanted = wanted || bytes.Contains(v.Raw, []byte(seed.Road))
				}
				if !wanted {
					continue
				}
				var w struct{ Tags map[string]string }
				if err = json.Unmarshal(v.Raw, &w); err != nil {
					t.Fatal(err)
				}
				tags[v.ID] = w.Tags
			}
		}
	}
	segments := []Segment{}
	wantedNodes := map[int64]bool{}
	for _, c := range manifest.Chunks {
		if c.Kind == "segments" {
			values, err := readChunk[Segment](ctx, db, c)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range values {
				if v.Snap && tags[v.Way] != nil {
					segments = append(segments, v)
					wantedNodes[v.From] = true
					wantedNodes[v.To] = true
				}
			}
		}
	}
	points := map[int64]Point{}
	for _, c := range manifest.Chunks {
		if c.Kind == "nodes" {
			values, err := readChunk[Node](ctx, db, c)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range values {
				if wantedNodes[v.ID] {
					points[v.ID] = v.Point
				}
			}
		}
	}
	type selected struct {
		Name         string
		Point        Point
		Way          int64
		Segment      string
		From, To     int64
		Tags         map[string]string
		NearDistance float64
	}
	results := []selected{}
	for _, seed := range seeds {
		best := selected{Name: seed.Name, NearDistance: math.Inf(1)}
		for _, seg := range segments {
			if !seg.Snap || tags[seg.Way]["name"] != seed.Road {
				continue
			}
			a, b := points[seg.From], points[seg.To]
			p := Point{(a[0] + b[0]) / 2, (a[1] + b[1]) / 2}
			distance := Distance(p, seed.Near)
			if distance < best.NearDistance {
				best = selected{seed.Name, p, seg.Way, seg.ID, seg.From, seg.To, tags[seg.Way], distance}
			}
		}
		if best.NearDistance > 3000 {
			t.Fatalf("no supported source segment near %s: %.0f m", seed.Name, best.NearDistance)
		}
		t.Logf("SEED %s distance=%.1f point=%v way=%d", best.Name, best.NearDistance, best.Point, best.Way)
		results = append(results, best)
	}
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(results); err != nil {
		t.Fatal(err)
	}
}

// Retain primary source decisions for the independently identified I-90 corridor.
// This investigation does not change closures or synthesize costs for absent edges.
func TestNorthwestI90Sources(t *testing.T) {
	path, out := os.Getenv("OPENMAPS_NORTHWEST_DB"), os.Getenv("OPENMAPS_I90_REPORT")
	if path == "" || out == "" {
		t.Skip("set Northwest DB and a new I-90 report")
	}
	ctx := context.Background()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, _, _, err := readManifest(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	sources := []Source{}
	nodes := map[int64]bool{}
	for _, c := range m.Chunks {
		if c.Kind == "sources" {
			values, err := readChunk[Source](ctx, db, c)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range values {
				if v.Kind != "way" || !bytes.Contains(v.Raw, []byte("I 90")) {
					continue
				}
				var w struct {
					Nodes []int64
					Tags  map[string]string
				}
				if err := json.Unmarshal(v.Raw, &w); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(w.Tags["ref"], "I 90") {
					continue
				}
				sources = append(sources, v)
				for _, n := range w.Nodes {
					nodes[n] = true
				}
			}
		}
	}
	for _, c := range m.Chunks {
		if c.Kind == "sources" {
			values, err := readChunk[Source](ctx, db, c)
			if err != nil {
				t.Fatal(err)
			}
			for _, v := range values {
				if v.Kind == "node" && nodes[v.ID] {
					sources = append(sources, v)
				}
			}
		}
	}
	f, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(sources); err != nil {
		t.Fatal(err)
	}
	t.Logf("I90 source records=%d referenced nodes=%d", len(sources), len(nodes))
}
