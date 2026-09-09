package valhallatiles

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPreparedExtensionPreservesSourceAndImportsBoundary(t *testing.T) {
	for _, clone := range []bool{false, true} {
		if clone && runtime.GOOS != "darwin" {
			continue
		}
		t.Run(map[bool]string{false: "copy", true: "clone"}[clone], func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			base := filepath.Join(root, "base")
			out := filepath.Join(root, "extended")
			budget := ScoutBudgets{CompressedBytes: 1 << 20, ExpandedBytes: 1 << 20, ReserveBytes: 32 << 30}
			if err := diskReserve(root, 2<<20, budget.ReserveBytes); err != nil {
				t.Skip(err)
			}
			lock := syntheticScoutLock(t)
			partial := lock
			partial.Packages = lock.Packages[:1]
			if err := PrepareScoutPackages(ctx, "testdata/scout", base, partial, budget); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(filepath.Join(base, "tiles.bin"))
			if err != nil {
				t.Fatal(err)
			}
			if err := ExtendScoutPackages(ctx, "testdata/scout", out, base, clone, lock, budget); err != nil {
				t.Fatal(err)
			}
			after, err := os.ReadFile(filepath.Join(base, "tiles.bin"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("extension changed base bytes")
			}
			a, _ := os.Stat(filepath.Join(base, "tiles.bin"))
			b, _ := os.Stat(filepath.Join(out, "tiles.bin"))
			if os.SameFile(a, b) {
				t.Fatal("extension shares mutable inode")
			}
			r, err := OpenPreparedScout(ctx, out, pageSize)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if len(r.TileIDs()) != 2 {
				t.Fatal("boundary tile not imported")
			}
			full := filepath.Join(root, "independent")
			if err := PrepareScoutPackages(ctx, "testdata/scout", full, lock, budget); err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(full, "tiles.bin"))
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(out, "tiles.bin"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(want, got) {
				t.Fatal("extended graph differs from independent import")
			}
			raw, err := os.ReadFile(filepath.Join(out, "receipt.json"))
			if err != nil {
				t.Fatal(err)
			}
			var pin preparedScout
			if err := json.Unmarshal(raw, &pin); err != nil || pin.BaseReceiptSHA256 == "" {
				t.Fatal("missing extension provenance", err)
			}
			changed := lock
			changed.Packages = append([]ScoutPackage(nil), lock.Packages...)
			changed.Packages[0].Bytes++
			if err := ExtendScoutPackages(ctx, "testdata/scout", filepath.Join(root, "bad"), base, clone, changed, budget); err == nil {
				t.Fatal("changed base pin accepted")
			}
		})
	}
}
