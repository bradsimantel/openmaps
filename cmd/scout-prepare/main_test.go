package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openmaps/internal/importer/scout"
	"openmaps/internal/routing"
)

func TestRepositoryAcquisitionPlanHasExactSchema(t *testing.T) {
	p, err := scout.ReadPlan("../../config/routing", "scout-national-acquisition.json")
	if err != nil {
		t.Fatal(err)
	}
	if p.Schema != 1 || p.PackageSchema != "2" || p.Version != "3.4.0" || len(p.Packages) != 647 {
		t.Fatalf("unexpected repository acquisition plan: schema=%d package_schema=%q version=%q packages=%d", p.Schema, p.PackageSchema, p.Version, len(p.Packages))
	}
}

// Verify the actual graph publication boundary, not just a field-by-field copy.
func TestPreparationHandoffEnforcesPins(t *testing.T) {
	for _, kind := range []string{"valid", "tile-sha", "tile-size", "missing-tile", "duplicate-tile", "package-sha", "dataset", "generation"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			free, err := scout.FreeDisk(root)
			if err != nil {
				t.Fatal(err)
			}
			if free < 32<<30+1<<20 {
				t.Skip("declared disk reserve unavailable")
			}
			source := "../../internal/routing/testdata/scout"
			b, err := os.ReadFile(filepath.Join(source, "lock.json"))
			if err != nil {
				t.Fatal(err)
			}
			var p scout.Plan
			if err := json.Unmarshal(b, &p); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "tile-sha":
				p.Packages[0].Tiles[0].SHA256 = strings.Repeat("0", 64)
			case "tile-size":
				p.Packages[0].Tiles[0].Bytes++
			case "missing-tile":
				p.Packages[0].Tiles[0].Name = "valhalla/tiles/2/000/824/433.gph.gz"
			case "duplicate-tile":
				p.Packages[0].Tiles = append(p.Packages[0].Tiles, p.Packages[0].Tiles[0])
			case "package-sha":
				p.Packages[0].SHA256 = strings.Repeat("0", 64)
			case "dataset":
				p.Dataset = 1 << 63
			case "generation":
				p.Timestamp = "different"
			}
			out := filepath.Join(root, "graph")
			err = routing.PrepareScoutPackages(context.Background(), source, out, preparationLock(p), routing.ScoutBudgets{CompressedBytes: 1 << 20, ExpandedBytes: 1 << 20, ReserveBytes: 32 << 30})
			if kind != "valid" {
				if err == nil {
					t.Fatal("ignored acquisition constraint")
				}
				if _, e := os.Stat(filepath.Join(out, "receipt.json")); !os.IsNotExist(e) {
					t.Fatal("failed graph published", e)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			reader, err := routing.OpenPreparedScout(context.Background(), out, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if len(reader.TileIDs()) != 2 {
				t.Fatal("lost pinned graph members")
			}
		})
	}
}
