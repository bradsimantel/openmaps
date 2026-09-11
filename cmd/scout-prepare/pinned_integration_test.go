//go:build integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"openmaps/internal/importer/scout"
	"openmaps/internal/routing"
)

func TestPinnedNationalHandoff(t *testing.T) {
	root := os.Getenv("OPENMAPS_SCOUT_ACQUISITION")
	if root == "" {
		t.Skip("set OPENMAPS_SCOUT_ACQUISITION to retained pinned inputs; no downloads")
	}
	p, err := scout.ReadAcquiredPlan(context.Background(), root, "national-aleutian-acquisition.json", scout.Budgets{Download: 16 << 30, Reserve: 32 << 30})
	if err != nil {
		t.Fatal(err)
	}
	// The repository qualification config is independent of the acquisition receipts.
	b, err := os.ReadFile("../../config/routing.json")
	if err != nil {
		t.Fatal(err)
	}
	var pinned routing.ScoutLock
	if err := json.Unmarshal(b, &pinned); err != nil {
		t.Fatal(err)
	}
	got := preparationLock(p)
	if got.Schema != pinned.Schema || got.PackageSchema != pinned.PackageSchema || got.Version != pinned.Version || got.Timestamp != pinned.Timestamp || len(got.Packages) != 647 {
		t.Fatal("qualification generation/selection changed")
	}
	// Plans may leave Dataset unset; actual tiles must still enforce their one generation.
	if got.Dataset != 0 && got.Dataset != pinned.Dataset {
		t.Fatal("dataset pin changed")
	}
	want := map[string]routing.ScoutPackage{}
	for _, q := range pinned.Packages {
		want[q.ID] = q
	}
	for _, q := range got.Packages {
		w, ok := want[q.ID]
		if !ok || q.Bytes != w.Bytes || q.MD5 != w.MD5 || q.SHA256 != w.SHA256 {
			t.Fatal("package pin changed", q.ID)
		}
		if _, err := os.Stat(filepath.Join(root, "packages", q.ID+".tar.bz2")); err != nil {
			t.Fatal(err)
		}
		delete(want, q.ID)
	}
	if len(want) != 0 {
		t.Fatal("qualification packages omitted")
	}
}
