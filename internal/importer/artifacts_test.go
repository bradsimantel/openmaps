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

func TestReadStreamingManifestAndRejectOverlappingScopes(t *testing.T) {
	path := filepath.Join("..", "..", "config", "places-geocoding-us.json")
	manifest, err := ReadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Schema != 2 || manifest.Streaming.BatchRows != 2048 || len(manifest.Scopes) != 3 {
		t.Fatalf("unexpected streaming config: %+v", manifest)
	}
	manifest.Scopes[1].BBox = manifest.Scopes[0].BBox
	invalid := filepath.Join(t.TempDir(), "overlap.json")
	if err = WriteJSON(invalid, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadManifest(invalid); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatal("accepted overlapping streaming scopes", err)
	}
}
