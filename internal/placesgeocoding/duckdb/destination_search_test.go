package duckdb

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"

	"openmaps/internal/importer"
	"openmaps/internal/places"
)

func TestExactDestinationTypeAndEvidenceRanking(t *testing.T) {
	bundle := importer.Bundle{
		Schema:        1,
		Manifest:      json.RawMessage(`{"region":"destination fixture","bbox":[-130,20,-60,50],"release":"fixture-v1"}`),
		Identities:    map[string]string{},
		Relationships: []importer.Relationship{},
		Rejections:    []importer.Rejection{},
	}
	add := func(record importer.Record) {
		record.Release = "fixture-v1"
		if record.Priority == 0 {
			record.Priority = 100
		}
		record.Attributions = []places.Attribution{{Provider: "Synthetic test data", URI: "https://example.org/fixtures"}}
		bundle.Records = append(bundle.Records, record)
	}
	addArea := func(id, name, subtype, class string, prominence int) {
		properties := map[string]any{"class": class, "cartography": map[string]any{}}
		if prominence > 0 {
			properties["cartography"] = map[string]any{"prominence": prominence}
		}
		add(importer.Record{
			Source: "overture:division", SourceID: id, Kind: "area",
			Attributes: map[string]json.RawMessage{
				"name": encodedRaw(name), "subtype": encodedRaw(subtype),
				"location": encodedRaw(places.Location{Lat: 40, Lng: -100}),
			},
			Paths: map[string]string{"name": "/properties/names/primary", "subtype": "/properties/subtype", "location": "/geometry"},
			Raw:   encodedRaw(map[string]any{"properties": properties}),
		})
	}
	addStreet := func(id, name string) {
		add(importer.Record{
			Source: "fixture:segment", SourceID: id, Kind: "street",
			Attributes: map[string]json.RawMessage{
				"name": encodedRaw(name), "location": encodedRaw(places.Location{Lat: 40, Lng: -100}),
			},
			Paths: map[string]string{"name": "/fixture/name", "location": "/fixture/location"}, Raw: json.RawMessage(`{"fixture":true}`),
		})
	}
	addPlace := func(id, name, basic string, hierarchy []string, confidence float64, priority int) importer.Record {
		record := importer.Record{
			Source: "overture:place", SourceID: id, Kind: "business", Priority: priority,
			Attributes: map[string]json.RawMessage{
				"name": encodedRaw(name), "location": encodedRaw(places.Location{Lat: 40, Lng: -100}), "closed": encodedRaw(false),
			},
			Paths: map[string]string{"name": "/properties/names/primary", "location": "/geometry", "closed": "/properties/operating_status"},
			Raw: encodedRaw(map[string]any{"properties": map[string]any{
				"basic_category": basic, "confidence": confidence,
				"taxonomy": map[string]any{"hierarchy": hierarchy, "primary": hierarchy[len(hierarchy)-1]},
			}}),
		}
		add(record)
		return record
	}
	addGeneric := func(id, name string) {
		add(importer.Record{
			Source: "fixture:place", SourceID: id, Kind: "business",
			Attributes: map[string]json.RawMessage{
				"name": encodedRaw(name), "location": encodedRaw(places.Location{Lat: 40, Lng: -100}), "closed": encodedRaw(false),
			},
			Paths: map[string]string{"name": "/fixture/name", "location": "/fixture/location", "closed": "/fixture/closed"}, Raw: json.RawMessage(`{"fixture":true}`),
		})
	}

	addArea("memorial-neighborhood", "Memorial", "neighborhood", "", 0)
	addStreet("memorial-street", "Memorial")
	addGeneric("memorial-shop", "Memorial")
	addPlace("memorial-monument", "Memorial", "monument", []string{"cultural_and_historic", "historic_site", "monument"}, 0.97, 100)

	addPlace("island-beach", "Museum Island", "beach", []string{"geographic_entities", "land", "beach"}, 0.97, 100)
	addPlace("island-museum", "Museum Island", "museum", []string{"arts_and_entertainment", "museum"}, 0.80, 100)

	addPlace("field-stadium", "Example Field", "stadium_arena", []string{"arts_and_entertainment", "stadium_arena"}, 0.80, 100)
	addPlace("field-baseball", "Example Field", "stadium_arena", []string{"arts_and_entertainment", "stadium_arena", "stadium", "baseball_stadium"}, 0.97, 100)

	addStreet("adventure-street", "Adventure World")
	addGeneric("adventure-restaurant", "Adventure World")
	addPlace("adventure-park", "Adventure World", "amusement_park", []string{"arts_and_entertainment", "amusement_attraction", "amusement_park"}, 0.80, 100)

	addStreet("bridge-street", "Example Bridge")
	addPlace("bridge-place", "Example Bridge", "bridge", []string{"geographic_entities", "built_feature", "bridge"}, 0.97, 100)

	addArea("square-area", "Example Square", "microhood", "", 0)
	addPlace("square-place", "Example Square", "public_plaza", []string{"geographic_entities", "public_plaza"}, 0.97, 100)

	addArea("citymark", "Citymark", "locality", "city", 80)
	addPlace("citymark-attraction", "Citymark", "museum", []string{"arts_and_entertainment", "museum"}, 0.97, 100)
	addArea("examplestate", "Examplestate", "region", "", 0)
	addPlace("examplestate-attraction", "Examplestate", "museum", []string{"arts_and_entertainment", "museum"}, 0.97, 100)

	tieA := addPlace("tie-a", "Tie Museum", "museum", []string{"arts_and_entertainment", "museum"}, 0.80, 100)
	tieB := addPlace("tie-b", "Tie Museum", "museum", []string{"arts_and_entertainment", "museum"}, 0.80, 100)

	canonical := addPlace("multi-canonical", "Multi Source Site", "historic_site", []string{"cultural_and_historic", "historic_site"}, 0.97, 200)
	supplement := addPlace("multi-supplement", "Multi Source Site", "restaurant", []string{"food_and_drink", "restaurant"}, 0.97, 100)
	bundle.Identities[supplement.Key()] = canonical.Key()

	crowdedWant := ""
	for i := range exactDestinationEvidenceLimit + 1 {
		id := "crowded-" + strconv.Itoa(i)
		record := addPlace(id, "Crowded Site", "museum", []string{"arts_and_entertainment", "museum"}, 0.80, 100)
		// If the bounded guard regresses, parsing this retained source evidence
		// fails rather than letting the test silently exercise only ordering.
		bundle.Records[len(bundle.Records)-1].Raw = json.RawMessage(`{"properties":{"confidence":2}}`)
		publicID := importer.PublicID(record.Key())
		if crowdedWant == "" || publicID < crowdedWant {
			crowdedWant = publicID
		}
	}

	path := filepath.Join(t.TempDir(), "lookup")
	if err := Build(context.Background(), path, bundle); err != nil {
		t.Fatal(err)
	}
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	tieWant := importer.PublicID(tieA.Key())
	if other := importer.PublicID(tieB.Key()); other < tieWant {
		tieWant = other
	}
	for _, tc := range []struct {
		query, wantID, wantKind string
	}{
		{"Memorial", importer.PublicID("overture:place:memorial-monument"), "business"},
		{"Museum Island", importer.PublicID("overture:place:island-museum"), "business"},
		{"Example Field", importer.PublicID("overture:place:field-baseball"), "business"},
		{"Adventure World", importer.PublicID("overture:place:adventure-park"), "business"},
		{"Example Bridge", importer.PublicID("overture:place:bridge-place"), "business"},
		{"Example Square", importer.PublicID("overture:division:square-area"), "area"},
		{"Citymark", importer.PublicID("overture:division:citymark"), "area"},
		{"Examplestate", importer.PublicID("overture:division:examplestate"), "area"},
		{"Tie Museum", tieWant, "business"},
		{"Multi Source Site", importer.PublicID(canonical.Key()), "business"},
		{"Crowded Site", crowdedWant, "business"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			for range 2 {
				results, queryErr := store.Autocomplete(context.Background(), tc.query)
				if queryErr != nil {
					t.Fatal(queryErr)
				}
				if len(results) == 0 || results[0].ID != tc.wantID || results[0].Kind != tc.wantKind {
					t.Fatalf("first result: %+v want id=%s kind=%s", results, tc.wantID, tc.wantKind)
				}
			}
		})
	}
}
