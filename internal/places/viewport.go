package places

import (
	"fmt"
	"math"
)

// Viewport is a closed WGS84 latitude/longitude rectangle. South/West is the
// low corner and North/East is the high corner. West greater than East means
// the rectangle crosses the antimeridian.
type Viewport struct {
	South float64
	West  float64
	North float64
	East  float64
}

// Validate applies the Google viewport rules used by the supported Places and
// Geocoding request subsets. Lines and a single point are valid closed regions.
func (v Viewport) Validate() error {
	values := []float64{v.South, v.West, v.North, v.East}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("viewport coordinates must be finite")
		}
	}
	if v.South < -90 || v.South > 90 || v.North < -90 || v.North > 90 {
		return fmt.Errorf("viewport latitude must be in [-90,90]")
	}
	if v.West < -180 || v.West > 180 || v.East < -180 || v.East > 180 {
		return fmt.Errorf("viewport longitude must be in [-180,180]")
	}
	if v.South > v.North {
		return fmt.Errorf("viewport south latitude must not exceed north latitude")
	}
	if v.West == 180 && v.East == -180 {
		return fmt.Errorf("viewport longitude range is empty")
	}
	return nil
}

func (v Viewport) Contains(point Location) bool {
	if point.Lat < v.South || point.Lat > v.North {
		return false
	}
	if v.West <= v.East {
		return point.Lng >= v.West && point.Lng <= v.East
	}
	return point.Lng >= v.West || point.Lng <= v.East
}

// Center returns the midpoint in [latitude,longitude] terms. The longitude
// midpoint follows the short interval through the antimeridian when crossed.
func (v Viewport) Center() Location {
	lng := (v.West + v.East) / 2
	if v.West > v.East {
		lng = (v.West + v.East + 360) / 2
		if lng > 180 {
			lng -= 360
		}
	}
	return Location{Lat: (v.South + v.North) / 2, Lng: lng}
}

// DistanceMeters returns spherical straight-line distance using the IUGG mean
// Earth radius. Locations are always latitude then longitude at this boundary.
func DistanceMeters(a, b Location) float64 {
	const earthRadius = 6371008.8
	rad := math.Pi / 180
	x := math.Pow(math.Sin((b.Lat-a.Lat)*rad/2), 2) + math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Pow(math.Sin((b.Lng-a.Lng)*rad/2), 2)
	return 2 * earthRadius * math.Asin(math.Sqrt(math.Min(1, math.Max(0, x))))
}
