package duckdb

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"openmaps/internal/importer"
	"openmaps/internal/places"
)

func TestStructuredAutocompleteContext(t *testing.T) {
	bundle := importer.Bundle{
		Schema:     1,
		Manifest:   json.RawMessage(`{"region":"context fixture","bbox":[-130,20,-60,50],"release":"fixture-v1"}`),
		Identities: map[string]string{},
	}
	add := func(source, id, kind, name, subtype string, location places.Location, address string) {
		attributes := map[string]json.RawMessage{
			"name":     encodedRaw(name),
			"location": encodedRaw(location),
		}
		paths := map[string]string{"name": "/fixture/name", "location": "/fixture/location"}
		if subtype != "" {
			attributes["subtype"] = encodedRaw(subtype)
			paths["subtype"] = "/fixture/subtype"
		}
		if address != "" {
			attributes["address"] = encodedRaw(address)
			paths["address"] = "/fixture/address"
		}
		bundle.Records = append(bundle.Records, importer.Record{
			Source: source, SourceID: id, Release: "fixture-v1", Priority: 100, Kind: kind,
			Attributes: attributes, Paths: paths, Raw: json.RawMessage(`{"fixture":true}`),
			Attributions: []places.Attribution{{Provider: "Synthetic test data", URI: "https://example.org/fixtures"}},
		})
	}
	add("fixture:division", "oregon", "area", "Oregon", "region", places.Location{Lat: 44, Lng: -120.5}, "")
	add("fixture:division", "maine", "area", "Maine", "region", places.Location{Lat: 45.2, Lng: -69.2}, "")
	add("fixture:division", "missouri", "area", "Missouri", "region", places.Location{Lat: 38.5, Lng: -92.5}, "")
	add("fixture:division", "multnomah", "area", "Multnomah County", "county", places.Location{Lat: 45.5, Lng: -122.5}, "")
	add("fixture:division", "cumberland", "area", "Cumberland County", "county", places.Location{Lat: 43.8, Lng: -70.3}, "")
	add("fixture:division", "portland-or", "area", "Portland", "locality", places.Location{Lat: 45.52, Lng: -122.67}, "")
	add("fixture:division", "portland-or-neighborhood", "area", "Portland", "neighborhood", places.Location{Lat: 45.51, Lng: -122.66}, "")
	add("fixture:division", "portland-me", "area", "Portland", "locality", places.Location{Lat: 43.66, Lng: -70.26}, "")
	add("fixture:division", "saint-louis-mo", "area", "Saint Louis", "locality", places.Location{Lat: 38.63, Lng: -90.20}, "")
	add("fixture:segment", "main-or", "street", "Main Street", "", places.Location{Lat: 45.53, Lng: -122.68}, "")
	add("fixture:segment", "main-me", "street", "Main Street", "", places.Location{Lat: 43.65, Lng: -70.25}, "")
	add("fixture:segment", "remote-me", "street", "Remote Street", "", places.Location{Lat: 43.65, Lng: -70.25}, "")
	pagedIDs := make([]string, contextStreetCandidatePageSize+5)
	localPaged := 0
	for i := range pagedIDs {
		pagedIDs[i] = "paged-" + strconv.Itoa(i)
		if importer.PublicID("fixture:segment:"+pagedIDs[i]) > importer.PublicID("fixture:segment:"+pagedIDs[localPaged]) {
			localPaged = i
		}
	}
	for i, id := range pagedIDs {
		location := places.Location{Lat: 43.65, Lng: -70.25}
		if i == localPaged {
			location = places.Location{Lat: 45.53, Lng: -122.68}
		}
		add("fixture:segment", id, "street", "Paged Street", "", location, "")
	}
	add("fixture:place", "portland-or-words", "business", "Portland OR", "", places.Location{Lat: 45.54, Lng: -122.69}, "Portland, OR, US")
	add("fixture:place", "main-portland-words", "business", "Main Street Portland OR", "", places.Location{Lat: 45.54, Lng: -122.69}, "Portland, OR, US")
	add("fixture:place", "coffee-roaster", "business", "Coffee Roaster", "", places.Location{Lat: 45.54, Lng: -122.69}, "Portland, OR, US")
	add("fixture:place", "coffeemaker-roaster", "business", "Coffeemaker Roaster", "", places.Location{Lat: 45.54, Lng: -122.69}, "Portland, OR, US")
	parcelIDs := make([]string, 10)
	localParcel := 0
	nearParcel := 0
	for i := range parcelIDs {
		parcelIDs[i] = "parcel-" + strconv.Itoa(i)
		if importer.PublicID("fixture:place:"+parcelIDs[i]) > importer.PublicID("fixture:place:"+parcelIDs[localParcel]) {
			nearParcel = localParcel
			localParcel = i
		} else if i != localParcel && (nearParcel == localParcel || importer.PublicID("fixture:place:"+parcelIDs[i]) > importer.PublicID("fixture:place:"+parcelIDs[nearParcel])) {
			nearParcel = i
		}
	}
	for i, id := range parcelIDs {
		location := places.Location{Lat: 45.54, Lng: -122.69}
		address := "10 Test Street, Portland, OR, 97201, US"
		if i == localParcel {
			location = places.Location{Lat: 43.66, Lng: -70.26}
			address = "10 Test Street, Portland, ME, 04101, US"
		} else if i == nearParcel {
			location = places.Location{Lat: 43.664, Lng: -70.264}
			address = "12 Test Street, Portland, ME, 04101, US"
		}
		add("fixture:place", id, "business", "Parcel Store", "", location, address)
	}
	bundle.Records = append(bundle.Records, importer.Record{
		Source: "overture:address", SourceID: "portland-me-context", Release: "fixture-v1", Priority: 100, Kind: "address",
		Attributes: map[string]json.RawMessage{
			"name":     encodedRaw("1 Context Street"),
			"address":  encodedRaw("1 Context Street, ME, Cumberland, Portland, 04101, US"),
			"location": encodedRaw(places.Location{Lat: 43.661, Lng: -70.261}),
		},
		Paths: map[string]string{"name": "/properties/name", "address": "/properties/address", "location": "/geometry"},
		Raw: encodedRaw(map[string]any{"properties": map[string]any{
			"number": "1", "street": "Context Street", "country": "US", "postcode": "04101",
			"address_levels": []any{map[string]any{"value": "ME"}, map[string]any{"value": "Cumberland, Portland"}},
		}}),
		Attributions: []places.Attribution{{Provider: "Synthetic test data", URI: "https://example.org/fixtures"}},
	})
	ambiguousIDs := make([]string, contextAreaLocatorThreshold+5)
	localAmbiguous := 0
	for i := range ambiguousIDs {
		ambiguousIDs[i] = "crowded-" + strconv.Itoa(i)
		if importer.PublicID("fixture:division:"+ambiguousIDs[i]) > importer.PublicID("fixture:division:"+ambiguousIDs[localAmbiguous]) {
			localAmbiguous = i
		}
	}
	for i, id := range ambiguousIDs {
		location := places.Location{Lat: 45.2, Lng: -69.2}
		if i == localAmbiguous {
			location = places.Location{Lat: 44, Lng: -120.5}
		}
		add("fixture:division", id, "area", "Crowded", "locality", location, "")
	}
	bundle.Relationships = []importer.Relationship{
		{From: "fixture:division:multnomah", To: "fixture:division:oregon", Kind: "parent_area", Evidence: "fixture hierarchy"},
		{From: "fixture:division:cumberland", To: "fixture:division:maine", Kind: "parent_area", Evidence: "fixture hierarchy"},
		{From: "fixture:division:portland-or", To: "fixture:division:multnomah", Kind: "parent_area", Evidence: "fixture hierarchy"},
		{From: "fixture:division:portland-or-neighborhood", To: "fixture:division:portland-or", Kind: "parent_area", Evidence: "fixture hierarchy"},
		{From: "fixture:division:portland-me", To: "fixture:division:cumberland", Kind: "parent_area", Evidence: "fixture hierarchy"},
		{From: "fixture:division:saint-louis-mo", To: "fixture:division:missouri", Kind: "parent_area", Evidence: "fixture hierarchy"},
	}
	for i, id := range ambiguousIDs {
		parent := "fixture:division:maine"
		if i == localAmbiguous {
			parent = "fixture:division:oregon"
		}
		bundle.Relationships = append(bundle.Relationships, importer.Relationship{
			From: "fixture:division:" + id, To: parent, Kind: "parent_area", Evidence: "fixture hierarchy",
		})
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

	portlandOR := importer.PublicID("fixture:division:portland-or")
	portlandME := importer.PublicID("fixture:division:portland-me")
	mainOR := importer.PublicID("fixture:segment:main-or")
	mainME := importer.PublicID("fixture:segment:main-me")
	saintLouis := importer.PublicID("fixture:division:saint-louis-mo")
	pagedOR := importer.PublicID("fixture:segment:" + pagedIDs[localPaged])
	crowdedOR := importer.PublicID("fixture:division:" + ambiguousIDs[localAmbiguous])
	for _, tc := range []struct {
		query, firstID, kind string
	}{
		{"Portland, OR", portlandOR, "area"},
		{"Portland, Oregon", portlandOR, "area"},
		{"Portland, ME", portlandME, "area"},
		{"Main St, Portland, OR", mainOR, "street"},
		{"Main Street, Portland, Maine", mainME, "street"},
		{"St. Louis, MO", saintLouis, "area"},
		{"Paged Street, Portland, OR", pagedOR, "street"},
		{"Crowded, OR", crowdedOR, "area"},
	} {
		t.Run(tc.query, func(t *testing.T) {
			results, queryErr := store.Autocomplete(context.Background(), tc.query)
			if queryErr != nil {
				t.Fatal(queryErr)
			}
			if len(results) == 0 || results[0].ID != tc.firstID || results[0].Kind != tc.kind {
				t.Fatalf("first result: %+v want id=%s kind=%s", results, tc.firstID, tc.kind)
			}
		})
	}
	detail, err := store.Details(context.Background(), saintLouis)
	if err != nil || detail.Name != "St. Louis" {
		t.Fatalf("canonical locality display name: %+v err=%v", detail, err)
	}
	results, err := store.Autocomplete(context.Background(), "Missing Street, Portland, OR")
	if err != nil || len(results) != 0 {
		t.Fatalf("contextual miss returned unrelated entities: %+v err=%v", results, err)
	}
	results, err = store.Autocomplete(context.Background(), "Remote Street, Portland, OR")
	if err != nil || len(results) != 0 {
		t.Fatalf("distant contextual street returned: %+v err=%v", results, err)
	}
	results, err = store.Autocomplete(context.Background(), "Portland")
	if err != nil || len(results) == 0 || results[0].Kind != "area" {
		t.Fatalf("unstructured type preference regressed: %+v err=%v", results, err)
	}
	results, err = store.Autocomplete(context.Background(), "Crowded")
	if err != nil || len(results) != 5 || results[0].Kind != "area" {
		t.Fatalf("locator-first unstructured candidates: %+v err=%v", results, err)
	}
	results, err = store.Autocomplete(context.Background(), "coffee roast")
	coffeeID := importer.PublicID("fixture:place:coffee-roaster")
	coffeemakerID := importer.PublicID("fixture:place:coffeemaker-roaster")
	if err != nil || len(results) == 0 || results[0].ID != coffeeID {
		t.Fatalf("completed-token exact match: %+v err=%v", results, err)
	}
	for _, result := range results {
		if result.ID == coffeemakerID {
			t.Fatalf("completed token broadened to a prefix: %+v", results)
		}
	}

	// A missing viewport preserves the established ordering exactly.
	baseline, err := store.Autocomplete(context.Background(), "Main Street")
	if err != nil {
		t.Fatal(err)
	}
	withoutBias, err := store.AutocompleteWithBias(context.Background(), "Main Street", nil)
	if err != nil || !reflect.DeepEqual(withoutBias, baseline) {
		t.Fatalf("nil viewport changed results: got %+v want %+v err=%v", withoutBias, baseline, err)
	}
	oregonView := places.Viewport{South: 45.4, West: -122.8, North: 45.7, East: -122.5}
	maineView := places.Viewport{South: 43.5, West: -70.4, North: 43.8, East: -70.1}
	for name, tc := range map[string]struct {
		view places.Viewport
		want string
	}{
		"Oregon": {oregonView, mainOR},
		"Maine":  {maineView, mainME},
	} {
		t.Run("viewport "+name, func(t *testing.T) {
			biased, queryErr := store.AutocompleteWithBias(context.Background(), "Main Street", &tc.view)
			if queryErr != nil || len(biased) == 0 || biased[0].ID != tc.want {
				t.Fatalf("biased Main Street: %+v want %s err=%v", biased, tc.want, queryErr)
			}
		})
	}
	portlandBiased, err := store.AutocompleteWithBias(context.Background(), "Portland", &maineView)
	if err != nil || len(portlandBiased) < 2 || portlandBiased[0].ID != portlandME {
		t.Fatalf("nearby otherwise-similar result was not promoted: %+v err=%v", portlandBiased, err)
	}
	hawaiiView := places.Viewport{South: 20, West: -158, North: 22, East: -156}
	outside, err := store.AutocompleteWithBias(context.Background(), "Remote Street", &hawaiiView)
	if err != nil || len(outside) == 0 || outside[0].ID != importer.PublicID("fixture:segment:remote-me") {
		t.Fatalf("soft bias excluded outside result: %+v err=%v", outside, err)
	}
	localParcelID := importer.PublicID("fixture:place:" + parcelIDs[localParcel])
	nearParcelID := importer.PublicID("fixture:place:" + parcelIDs[nearParcel])
	parcelBaseline, err := store.Autocomplete(context.Background(), "Parcel Store")
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range parcelBaseline {
		if result.ID == localParcelID || result.ID == nearParcelID {
			t.Fatalf("fixture does not exercise pre-limit candidate promotion: %+v", parcelBaseline)
		}
	}
	parcelBiased, err := store.AutocompleteWithBias(context.Background(), "Parcel Store", &maineView)
	if err != nil || len(parcelBiased) != 5 || parcelBiased[0].ID != localParcelID {
		t.Fatalf("viewport context did not introduce nearby candidate: %+v err=%v", parcelBiased, err)
	}
	outsideCount := 0
	for _, result := range parcelBiased {
		if !maineView.Contains(result.Location) {
			outsideCount++
		}
	}
	if outsideCount == 0 {
		t.Fatalf("viewport context acted as a restriction: %+v", parcelBiased)
	}
	tinyMaineView := places.Viewport{South: 43.6649, West: -70.2651, North: 43.6651, East: -70.2649}
	tinyBiased, err := store.AutocompleteWithBias(context.Background(), "Parcel Store", &tinyMaineView)
	if err != nil || len(tinyBiased) != 5 || tinyBiased[0].ID != nearParcelID {
		t.Fatalf("empty tiny viewport did not rank the nearest local result: %+v err=%v", tinyBiased, err)
	}
	for _, result := range tinyBiased {
		if tinyMaineView.Contains(result.Location) {
			t.Fatalf("tiny viewport fixture unexpectedly contains a result: %+v", tinyBiased)
		}
	}
}

func encodedRaw(value any) json.RawMessage {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}
