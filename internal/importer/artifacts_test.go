package importer

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestReadManifestRequiresBundleChecksum(t *testing.T) {
	path := filepath.Join(t.TempDir(), "places-geocoding.json")
	manifest := Manifest{
		Schema:          1,
		Region:          "test",
		BBox:            [4]float64{-71.33, 41.47, -71.29, 41.51},
		CoordinateOrder: "longitude,latitude",
		BundleSHA256:    strings.Repeat("a", 64),
	}
	if err := WriteJSON(path, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(path); err != nil {
		t.Fatal(err)
	}

	manifest.BundleSHA256 = ""
	if err := WriteJSON(path, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(path); err == nil {
		t.Fatal("accepted Places/geocoding config without normalized bundle checksum")
	}
}
