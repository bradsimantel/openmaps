package importer_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"openmaps/internal/importer"
	"openmaps/internal/places"
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
func build(t *testing.T, b importer.Bundle) (string, *places.Store) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.sqlite")
	if err := importer.Build(context.Background(), path, b); err != nil {
		t.Fatal(err)
	}
	s, err := places.Open(path)
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
	path, second := build(t, b)
	updated, err := second.Details(ctx, old.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != old.ID || updated.Name != "White Horse Public House" || updated.Website != old.Website || updated.Location != old.Location {
		t.Fatalf("identity/enrichment: %+v", updated)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var winner string
	if err = db.QueryRow("SELECT source_key FROM attribute_provenance WHERE entity_id=? AND attribute='name'", old.ID).Scan(&winner); err != nil || winner != supplement.Key() {
		t.Fatalf("winner %q: %v", winner, err)
	}
	var count int
	if err = db.QueryRow("SELECT count(*) FROM source_records WHERE entity_id=?", old.ID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("sources %d: %v", count, err)
	}
	// Source replacement retains the original anchor even after it leaves the input.
	b.Records = slices.DeleteFunc(b.Records, func(r importer.Record) bool { return r.Key() == "fixture:place:tavern" })
	b.Relationships = nil
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
		{"wrong relationship kind", func(b *importer.Bundle) { b.Relationships[0].To = "fixture:way:1" }},
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
			path := filepath.Join(t.TempDir(), "bad.sqlite")
			if err := importer.Build(context.Background(), path, b); err == nil {
				t.Fatal("expected rejection")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("failed import left a database: %v", err)
			}
		})
	}
}
func TestRefusesOverwrite(t *testing.T) {
	path, s := build(t, fixture(t))
	s.Close()
	if err := importer.Build(context.Background(), path, fixture(t)); err == nil {
		t.Fatal("overwrote database")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("removed existing database", err)
	}
}
