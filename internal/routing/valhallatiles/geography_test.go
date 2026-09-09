package valhallatiles

import (
	"errors"
	"math"
	"testing"
)

func TestDatelineGeometry(t *testing.T) {
	shape := []Point{{179.999, 65}, {-179.999, 65}}
	for _, longitude := range []float64{180, -180} {
		q, gap, f := project(Point{longitude, 65}, shape)
		if math.Abs(math.Abs(q[0])-180) > 1e-10 || gap > .0001 || math.Abs(f-.5) > 1e-8 {
			t.Fatalf("dateline projection: %v %v %v", q, gap, f)
		}
		part := clip(shape, .25, .75)
		if len(part) != 2 || Distance(part[0], part[1]) > 100 || part[0][0] < 179 || part[1][0] > -179 {
			t.Fatal("partial shape took long way around world", part)
		}
	}
}
func TestDatelineAndPolarMissingTiles(t *testing.T) {
	r := &Reader{index: map[ID]entry{}}
	for _, p := range []Point{{180, 65}, {-180, 65}, {0, 90}, {0, -90}, {-156.5, 71.3}} {
		_, err := r.Candidates(p, 100)
		var missing *MissingTileError
		if !errors.As(err, &missing) {
			t.Fatalf("valid coordinates rejected as unsupported: %v %v", p, err)
		}
		if missing.Tile.Tile() < 0 || missing.Tile.Tile() >= 1036800 {
			t.Fatalf("invalid wrapped/clamped tile: %s", missing.Tile)
		}
	}
}
