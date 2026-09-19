package duckdb

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"openmaps/internal/importer"
	"openmaps/internal/places"
)

func TestUnstructuredAreaProminenceAndTypeRanking(t *testing.T) {
	bundle := importer.Bundle{
		Schema:        1,
		Manifest:      json.RawMessage(`{"region":"prominence fixture","bbox":[-130,20,-60,50],"release":"fixture-v1"}`),
		Identities:    map[string]string{},
		Relationships: []importer.Relationship{},
	}
	add := func(source, sourceID, kind, name, subtype, class string, prominence *int) {
		attributes := map[string]json.RawMessage{
			"name":     encodedRaw(name),
			"location": encodedRaw(places.Location{Lat: 40, Lng: -100}),
		}
		paths := map[string]string{"name": "/fixture/name", "location": "/fixture/location"}
		if subtype != "" {
			attributes["subtype"] = encodedRaw(subtype)
			paths["subtype"] = "/fixture/subtype"
		}
		raw := json.RawMessage(`{"fixture":true}`)
		if source == "overture:division" {
			raw = encodedRaw(map[string]any{"properties": map[string]any{
				"class": class, "cartography": map[string]any{"prominence": prominence},
			}})
		}
		bundle.Records = append(bundle.Records, importer.Record{
			Source: source, SourceID: sourceID, Release: "fixture-v1", Priority: 100, Kind: kind,
			Attributes: attributes, Paths: paths, Raw: raw,
			Attributions: []places.Attribution{{Provider: "Synthetic test data", URI: "https://example.org/fixtures"}},
		})
	}
	low, high, medium := 25, 85, 50
	add("overture:division", "springfield-small", "area", "Springfield", "locality", "city", &low)
	add("overture:division", "springfield-large", "area", "Springfield", "locality", "city", &high)
	add("overture:division", "springfield-region", "area", "Springfield", "region", "", nil)
	add("fixture:segment", "springfield-street", "street", "Springfield", "", "", nil)
	add("fixture:place", "springfield-business", "business", "Springfield", "", "", nil)

	add("overture:division", "california-region", "area", "California", "region", "", nil)
	add("overture:division", "california-town", "area", "California", "locality", "town", &medium)
	add("fixture:segment", "california-street", "street", "California", "", "", nil)
	add("fixture:place", "california-business", "business", "California", "", "", nil)

	add("overture:division", "riverton-locality", "area", "Riverton", "locality", "town", &medium)
	add("overture:division", "riverton-county", "area", "Riverton", "county", "", nil)
	add("overture:division", "riverton-neighborhood", "area", "Riverton", "neighborhood", "", nil)

	tie := 70
	add("overture:division", "echo-a", "area", "Echo", "locality", "city", &tie)
	add("overture:division", "echo-b", "area", "Echo", "locality", "city", &tie)

	path := filepath.Join(t.TempDir(), "lookup")
	if err := Build(context.Background(), path, bundle); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	echoA := importer.PublicID("overture:division:echo-a")
	echoB := importer.PublicID("overture:division:echo-b")
	if echoB < echoA {
		echoA = echoB
	}
	for _, tc := range []struct {
		query, wantID, wantSubtype string
	}{
		{"Springfield", importer.PublicID("overture:division:springfield-large"), "locality"},
		{"California", importer.PublicID("overture:division:california-region"), "region"},
		{"Riverton", importer.PublicID("overture:division:riverton-locality"), "locality"},
		{"Echo", echoA, "locality"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			for range 2 {
				results, queryErr := store.Autocomplete(context.Background(), tc.query)
				if queryErr != nil {
					t.Fatal(queryErr)
				}
				if len(results) == 0 || results[0].ID != tc.wantID || results[0].Kind != "area" || results[0].Subtype != tc.wantSubtype {
					t.Fatalf("first result: %+v want id=%s subtype=%s", results, tc.wantID, tc.wantSubtype)
				}
			}
		})
	}
}
