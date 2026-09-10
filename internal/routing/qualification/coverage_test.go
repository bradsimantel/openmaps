package qualification

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHolesAndCrossings(t *testing.T) {
	p := Polygon{Box: Box{0, 0, 10, 10}, Rings: [][]Point{{{0, 0}, {10, 0}, {10, 10}, {0, 10}, {0, 0}}, {{3, 3}, {7, 3}, {7, 7}, {3, 7}, {3, 3}}}}
	if !Contains(p, Point{1, 1}) || Contains(p, Point{5, 5}) || Intersects(p, Box{4, 4, 6, 6}) || !Intersects(p, Box{2, 2, 4, 4}) || Intersects(p, Box{11, 0, 12, 1}) {
		t.Fatal("hole or disjoint rectangle")
	}
	p = Polygon{Box: Box{-5, -.1, 5, .1}, Rings: [][]Point{{{-5, -.1}, {5, -.1}, {5, .1}, {-5, .1}, {-5, -.1}}}}
	if !Intersects(p, Box{-1, -1, 1, 1}) || Intersects(p, Box{-1, 2, 1, 3}) {
		t.Fatal("edge crossing")
	}
}
func TestCorruptCensus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.zip")
	os.WriteFile(path, []byte("bad"), 0600)
	if _, e := Polygons(path); e == nil {
		t.Fatal("bad pin accepted")
	}
	for n := 0; n < 150; n++ {
		if _, e := parsePolygons(make([]byte, n), make([]byte, n)); e == nil {
			t.Fatal("bad extents accepted")
		}
	}
}
