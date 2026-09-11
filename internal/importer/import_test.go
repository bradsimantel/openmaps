package importer_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"openmaps/internal/importer"
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
)

func fixture(t *testing.T) importer.Bundle {
	t.Helper()
	raw, err := os.ReadFile("testdata/small.json")
	if err != nil {
		t.Fatal(err)
	}
	var b importer.Bundle
	if err = json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	return b
}
func build(t *testing.T, b importer.Bundle) (string, *placeduckdb.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "lookup")
	if err := placeduckdb.Build(context.Background(), path, b); err != nil {
		t.Fatal(err)
	}
	s, err := placeduckdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return path, s
}
func TestRebuildStableIDsAndSourceEnrichment(t *testing.T) {
	ctx := context.Background()
	b := fixture(t)
	_, first := build(t, b)
	old, err := first.Details(ctx, importer.PublicID("fixture:place:tavern"))
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(b.Records)
	for i := range b.Records {
		b.Records[i].Release = "fixture-v2"
	}
	supplement := b.Records[len(b.Records)-1]
	supplement.Source = "city:directory"
	supplement.SourceID = "42"
	supplement.Priority = 200
	supplement.Attributes = map[string]json.RawMessage{"name": json.RawMessage(`"White Horse Public House"`)}
	supplement.Paths = map[string]string{"name": "/official_name"}
	b.Records = append(b.Records, supplement)
	b.Identities[supplement.Key()] = "fixture:place:tavern"
	_, second := build(t, b)
	updated, err := second.Details(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != old.ID || updated.Name != "White Horse Public House" || updated.Website != old.Website || updated.Location != old.Location {
		t.Fatalf("identity/enrichment: %+v", updated)
	}
	evidence, err := second.Evidence(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	winner := ""
	for _, provenance := range evidence.Attributes {
		if provenance.Attribute == "name" {
			winner = provenance.SourceKey
		}
	}
	if winner != supplement.Key() || len(evidence.Sources) != 2 {
		t.Fatalf("winner=%q sources=%d", winner, len(evidence.Sources))
	}
	// Source replacement retains the original anchor even after it leaves the input.
	b.Records = slices.DeleteFunc(b.Records, func(r importer.Record) bool { return r.Key() == "fixture:place:tavern" })
	b.Relationships = []importer.Relationship{}
	supplement.Attributes["location"] = json.RawMessage(`{"lat":41.49,"lng":-71.31}`)
	supplement.Paths["location"] = "/point"
	b.Records[len(b.Records)-1] = supplement
	_, third := build(t, b)
	if _, err = third.Details(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
}
func TestOrderIndependentResults(t *testing.T) {
	b := fixture(t)
	_, a := build(t, b)
	slices.Reverse(b.Records)
	_, c := build(t, b)
	for _, query := range []string{"White", "Marl", "26 Marl", "Newport", "10 Shared"} {
		x, e := a.Autocomplete(context.Background(), query)
		if e != nil {
			t.Fatal(e)
		}
		y, e := c.Autocomplete(context.Background(), query)
		if e != nil || !reflect.DeepEqual(x, y) {
			t.Fatalf("%s unstable: %v", query, e)
		}
	}
}
func TestInvalidImportNeverPublishes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*importer.Bundle)
	}{
		{"duplicate source", func(b *importer.Bundle) { b.Records = append(b.Records, b.Records[0]) }},
		{"dangling relationship", func(b *importer.Bundle) { b.Relationships[0].To = "absent" }},
		{"wrong relationship kind", func(b *importer.Bundle) { b.Relationships[0].To = "fixture:segment:1" }},
		{"cross kind merge", func(b *importer.Bundle) { b.Identities["fixture:address:26"] = "fixture:place:tavern" }},
		{"missing longitude", func(b *importer.Bundle) { b.Records[0].Attributes["location"] = json.RawMessage(`{"lat":41}`) }},
		{"invalid latitude", func(b *importer.Bundle) { b.Records[0].Attributes["location"] = json.RawMessage(`{"lat":91,"lng":0}`) }},
		{"invalid longitude", func(b *importer.Bundle) { b.Records[0].Attributes["location"] = json.RawMessage(`{"lat":0,"lng":181}`) }},
		{"missing provenance", func(b *importer.Bundle) { delete(b.Records[0].Paths, "name") }},
		{"unsafe website", func(b *importer.Bundle) {
			b.Records[0].Attributes["website"] = json.RawMessage(`"javascript:alert(1)"`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := fixture(t)
			tc.change(&b)
			path := filepath.Join(t.TempDir(), "bad")
			if err := placeduckdb.Build(context.Background(), path, b); err == nil {
				t.Fatal("expected rejection")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("failed import left a generation: %v", err)
			}
		})
	}
}
func TestRefusesOverwrite(t *testing.T) {
	path, s := build(t, fixture(t))
	s.Close()
	if err := placeduckdb.Build(context.Background(), path, fixture(t)); err == nil {
		t.Fatal("overwrote generation")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("removed existing generation", err)
	}
}
