package places_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"openmaps/internal/importer"
	"openmaps/internal/places"
)

func TestRegionalSearch(t *testing.T) {
	raw, err := os.ReadFile("../importer/testdata/small.json")
	if err != nil {
		t.Fatal(err)
	}
	var b importer.Bundle
	if err = json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "places.sqlite")
	ctx := context.Background()
	if err = importer.Build(ctx, path, b); err != nil {
		t.Fatal(err)
	}
	s, err := places.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, tc := range []struct {
		query, first, kind string
		count              int
	}{
		{"white hor", "White Horse Tavern", "business", 1},
		{"26 Marlborough St", "26 Marlborough Street", "address", 3},
		{"Marlborough", "Marlborough Street", "street", 4},
		{"Newport", "Newport", "area", 3},
		{"CAFE", "Café Bellevue", "business", 1},
		{"Bellevue Coff", "Café Bellevue", "business", 1},
		{"10 Shared", "10 Shared Street", "address", 2},
		{"Dateline", "Dateline East", "business", 2},
		{`" OR * --`, "", "", 0},
		{"   ", "", "", 0},
		{"notinthisregion", "", "", 0},
	} {
		t.Run(tc.query, func(t *testing.T) {
			got, err := s.Autocomplete(ctx, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != tc.count {
				t.Fatalf("count: got %d want %d (%+v)", len(got), tc.count, got)
			}
			if len(got) > 0 && tc.query != "Dateline" && (got[0].Name != tc.first || got[0].Kind != tc.kind) {
				t.Fatalf("rank: %+v", got)
			}
			for _, p := range got {
				detail, err := s.Details(ctx, p.ID)
				if err != nil || detail.ID != p.ID || detail.Name != p.Name || detail.Location != p.Location {
					t.Fatalf("details mismatch %+v %v", detail, err)
				}
			}
		})
	}
	closed, err := s.Details(ctx, importer.PublicID("fixture:place:closed"))
	if err != nil || closed.Name != "White Horse Closed" {
		t.Fatal("closed place details lost", err)
	}
	for _, id := range []string{"east", "west"} {
		p, err := s.Details(ctx, importer.PublicID("fixture:place:"+id))
		if err != nil || p.Location.Lat != 0 || p.Location.Lng == 0 {
			t.Fatal("coordinate order or dateline corrupted", p, err)
		}
	}
}
