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
	if manifest.Schema != 2 || manifest.Streaming.BatchRows != 16384 || len(manifest.Scopes) != 4 {
		t.Fatalf("unexpected streaming config: %+v", manifest)
	}
	if manifest.Streaming.SourceWorkers != 4 || manifest.Streaming.DatabaseThreads != 4 || manifest.Streaming.CatalogThreads != 4 || manifest.Streaming.ProgressSeconds != 30 {
		t.Fatalf("unexpected national concurrency controls: %+v", manifest.Streaming)
	}
	if manifest.Streaming.PreparationMemoryLimit != "4GB" || manifest.Streaming.MemoryLimit != "16GB" || manifest.Streaming.CatalogMemoryLimit != "32GB" || manifest.Streaming.GoMemoryLimitMiB != 4096 {
		t.Fatalf("unexpected national memory controls: %+v", manifest.Streaming)
	}
	gate, err := ReadManifest(filepath.Join("..", "..", "config", "places-geocoding-us-gate.json"))
	if err != nil {
		t.Fatal(err)
	}
	if gate.Streaming.BatchRows != manifest.Streaming.BatchRows || gate.Streaming.SourceWorkers != manifest.Streaming.SourceWorkers || gate.Streaming.DatabaseThreads != manifest.Streaming.DatabaseThreads || gate.Streaming.CatalogThreads != manifest.Streaming.CatalogThreads || gate.Streaming.ProgressSeconds != manifest.Streaming.ProgressSeconds || gate.Streaming.PreparationMemoryLimit != manifest.Streaming.PreparationMemoryLimit || gate.Streaming.MemoryLimit != manifest.Streaming.MemoryLimit || gate.Streaming.CatalogMemoryLimit != manifest.Streaming.CatalogMemoryLimit || gate.Streaming.FreeDiskFloorGiB != manifest.Streaming.FreeDiskFloorGiB || gate.Streaming.GoMemoryLimitMiB != manifest.Streaming.GoMemoryLimitMiB {
		t.Fatalf("gate does not exercise national resource controls: gate=%+v national=%+v", gate.Streaming, manifest.Streaming)
	}
	if gate.Streaming.ExpectedDataSHA256 != "3b4f68333e1508d33f1c4610dfc630e5735871a0f6f0b5475e95a7462c56c359" {
		t.Fatalf("gate does not pin the qualified retained-data checksum: %+v", gate.Streaming)
	}
	if !insideScopes([2]float64{173.18, 52.88}, manifest.Scopes) {
		t.Fatal("national scope omits eastern-hemisphere Alaska")
	}
	manifest.Scopes[1].BBox = manifest.Scopes[0].BBox
	invalid := filepath.Join(t.TempDir(), "overlap.json")
	if err = WriteJSON(invalid, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadManifest(invalid); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatal("accepted overlapping streaming scopes", err)
	}

	manifest.Scopes[1].BBox = [4]float64{-178, 45, -130, 72}
	manifest.Streaming.SourceWorkers = -1
	if err = WriteJSON(invalid, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err = ReadManifest(invalid); err == nil || !strings.Contains(err.Error(), "streaming manifest") {
		t.Fatal("accepted negative source workers", err)
	}

	manifest.Streaming.SourceWorkers = 0
	manifest.Streaming.DatabaseThreads = 0
	manifest.Streaming.CatalogThreads = 0
	manifest.Streaming.ProgressSeconds = 0
	manifest.Streaming.PreparationMemoryLimit = ""
	if err = WriteJSON(invalid, manifest); err != nil {
		t.Fatal(err)
	}
	compatible, err := ReadManifest(invalid)
	if err != nil {
		t.Fatal("rejected a schema-2 manifest that predates concurrency controls", err)
	}
	if compatible.Streaming.SourceWorkers != 1 || compatible.Streaming.DatabaseThreads != 1 || compatible.Streaming.CatalogThreads != 1 || compatible.Streaming.ProgressSeconds != 30 || compatible.Streaming.PreparationMemoryLimit != compatible.Streaming.MemoryLimit {
		t.Fatalf("legacy concurrency defaults changed: %+v", compatible.Streaming)
	}
}
