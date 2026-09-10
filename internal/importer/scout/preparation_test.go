package scout

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func acquiredFixture(t *testing.T) (string, Plan) {
	t.Helper()
	root, c, p := planFixture(t)
	if err := c.Fetch(context.Background(), root, p, p.Budgets, nil); err != nil {
		t.Fatal(err)
	}
	return root, p
}
func savePlan(t *testing.T, root string, p Plan) {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "plan.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
}
func changeReceipt(t *testing.T, root string, change func(*Receipt)) {
	t.Helper()
	path := filepath.Join(root, "receipts", "1.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var r Receipt
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	change(&r)
	b, err = json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestAcquiredPlanPreservesPinsAndProvenance(t *testing.T) {
	for _, source := range []string{"plan", "receipt", "both"} {
		t.Run(source, func(t *testing.T) {
			root, p := acquiredFixture(t)
			p.Dataset = 183131145
			pins := []TilePin{{Name: "valhalla/tiles/2/000/824/434.gph.gz", Bytes: 391, SHA256: strings.Repeat("1", 64)}}
			if source != "receipt" {
				p.Packages[0].Tiles = pins
			}
			if source != "plan" {
				changeReceipt(t, root, func(r *Receipt) { r.Tiles = pins })
			}
			savePlan(t, root, p)
			before, err := os.ReadFile(filepath.Join(root, "receipts", "1.json"))
			if err != nil {
				t.Fatal(err)
			}
			got, err := ReadAcquiredPlan(context.Background(), root, "plan.json", p.Budgets)
			if err != nil {
				t.Fatal(err)
			}
			if got.Dataset != p.Dataset || !reflect.DeepEqual(got.MetadataSHA256, p.MetadataSHA256) || got.Provenance != p.Provenance || got.Attribution != p.Attribution || !reflect.DeepEqual(got.CatalogOmitted, p.CatalogOmitted) || !reflect.DeepEqual(got.Regions, p.Regions) {
				t.Fatal("lost generation, selection or provenance")
			}
			if !digest(got.Packages[0].SHA256, 32) || !reflect.DeepEqual(got.Packages[0].Tiles, pins) {
				t.Fatal("lost receipt hash or explicit tile constraints")
			}
			if err := VerifyRetained(root, got, p.Budgets); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(filepath.Join(root, "receipts", "1.json"))
			if err != nil || string(before) != string(after) {
				t.Fatal("handoff rewrote receipt", err)
			}
		})
	}
}
func TestAcquiredPlanRejectsInvalidInputs(t *testing.T) {
	for _, kind := range []string{"generation", "sha", "plan-sha", "id", "size", "md5", "url", "tile-conflict", "missing", "metadata", "budget", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			root, p := acquiredFixture(t)
			switch kind {
			case "generation":
				changeReceipt(t, root, func(r *Receipt) { r.Generation["catalog.json"] = strings.Repeat("0", 64) })
			case "sha":
				changeReceipt(t, root, func(r *Receipt) { r.SHA256 = "" })
			case "plan-sha":
				p.Packages[0].SHA256 = strings.Repeat("0", 64)
			case "id":
				changeReceipt(t, root, func(r *Receipt) { r.ID = "2" })
			case "size":
				changeReceipt(t, root, func(r *Receipt) { r.Bytes++ })
			case "md5":
				changeReceipt(t, root, func(r *Receipt) { r.MD5 = strings.Repeat("0", 32) })
			case "url":
				changeReceipt(t, root, func(r *Receipt) { r.URL = Base + Prefix + "2.tar.bz2" })
			case "tile-conflict":
				p.Packages[0].Tiles = []TilePin{{Name: "same", Bytes: 300, SHA256: strings.Repeat("1", 64)}}
				changeReceipt(t, root, func(r *Receipt) { r.Tiles = []TilePin{{Name: "same", Bytes: 300, SHA256: strings.Repeat("2", 64)}} })
			case "missing":
				if err := os.Remove(filepath.Join(root, "receipts", "1.json")); err != nil {
					t.Fatal(err)
				}
			case "metadata":
				if err := os.WriteFile(filepath.Join(root, "catalog.json"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "budget":
				p.Budgets.Download = 17 * GiB
			}
			savePlan(t, root, p)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "cancel" {
				cancel()
			}
			got, err := ReadAcquiredPlan(ctx, root, "plan.json", Budgets{16 * GiB, 32 * GiB})
			if err == nil || len(got.Packages) != 0 {
				t.Fatal("invalid acquisition returned graph inputs", got, err)
			}
			if kind == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
}
