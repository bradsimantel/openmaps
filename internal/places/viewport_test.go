package places_test

import (
	"math"
	"testing"

	"openmaps/internal/places"
)

func TestViewportValidationAndAntimeridian(t *testing.T) {
	for _, tc := range []struct {
		name string
		view places.Viewport
		ok   bool
	}{
		{"ordinary", places.Viewport{South: 40, West: -75, North: 41, East: -73}, true},
		{"point", places.Viewport{South: 40, West: -75, North: 40, East: -75}, true},
		{"latitude line", places.Viewport{South: 40, West: -75, North: 40, East: -73}, true},
		{"crosses antimeridian", places.Viewport{South: -10, West: 170, North: 10, East: -170}, true},
		{"all longitudes", places.Viewport{South: -10, West: -180, North: 10, East: 180}, true},
		{"empty longitude", places.Viewport{South: -10, West: 180, North: 10, East: -180}, false},
		{"inverted latitude", places.Viewport{South: 41, West: -75, North: 40, East: -73}, false},
		{"latitude range", places.Viewport{South: -91, West: -75, North: 40, East: -73}, false},
		{"longitude range", places.Viewport{South: 40, West: -181, North: 41, East: -73}, false},
		{"nonfinite", places.Viewport{South: math.NaN(), West: -75, North: 41, East: -73}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.view.Validate() == nil; got != tc.ok {
				t.Fatalf("Validate() success=%t want %t", got, tc.ok)
			}
		})
	}
	crossing := places.Viewport{South: -10, West: 170, North: 10, East: -170}
	if !crossing.Contains(places.Location{Lat: 0, Lng: 179}) || !crossing.Contains(places.Location{Lat: 0, Lng: -179}) || crossing.Contains(places.Location{Lat: 0, Lng: 0}) {
		t.Fatal("antimeridian containment is incorrect")
	}
	if center := crossing.Center(); center.Lat != 0 || math.Abs(math.Abs(center.Lng)-180) > 1e-9 {
		t.Fatalf("antimeridian center: %+v", center)
	}
}
