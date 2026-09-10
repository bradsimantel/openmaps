package routing

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPreparedScoutLifetimeAndIntegrity(t *testing.T) {
	lock := syntheticScoutLock(t)
	root := t.TempDir()
	out := filepath.Join(root, "prepared")
	budget := ScoutBudgets{CompressedBytes: 1 << 20, ExpandedBytes: 1 << 20, ReserveBytes: 32 << 30}
	if err := diskReserve(root, 1<<20, budget.ReserveBytes); err != nil {
		t.Skip("fixture requires declared preparation disk reserve: ", err)
	}
	if err := PrepareScoutPackages(context.Background(), "testdata/scout", out, lock, budget); err != nil {
		t.Fatal(err)
	}
	if err := PrepareScoutTurns(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	original, path := fixture(t, true, true)
	a, _ := original.Reader.Edge(path[0])
	b, _ := original.Reader.Edge(path[2])
	sa, _ := original.Reader.Shape(a)
	sb, _ := original.Reader.Shape(b)
	from, to := clip(sa.Points, .5, .5)[0], clip(sb.Points, .5, .5)[0]
	want, err := original.Route(context.Background(), from, to, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s, err := OpenPreparedRouter(context.Background(), out, pageSize)
		if err != nil {
			t.Fatal(err)
		}
		r := s.Reader
		got, err := s.Route(context.Background(), from, to, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got.Steps, want.Steps) || got.Seconds != want.Seconds || !reflect.DeepEqual(got.Geometry, want.Geometry) {
			t.Fatal("prepared path differs from hand-built reference")
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := r.Edge(path[0]); err == nil {
			t.Fatal("read after close")
		}
	}
	if err := PrepareScoutPackages(context.Background(), "testdata/scout", out, lock, budget); err == nil {
		t.Fatal("overwrote publication")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := OpenPreparedScout(ctx, out, pageSize); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled open: %v", err)
	}
	f, err := os.OpenFile(filepath.Join(out, "tiles.bin"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{255}, 300); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if r, err := OpenPreparedScout(context.Background(), out, pageSize); err == nil {
		r.Close()
		t.Fatal("accepted payload corruption")
	}
}

func TestPreparedScoutBudgetAndFailurePublication(t *testing.T) {
	for _, name := range []string{"compressed", "expanded", "cancelled", "generation", "digest"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			out := filepath.Join(root, "prepared")
			lock := syntheticScoutLock(t)
			budget := ScoutBudgets{CompressedBytes: 1 << 20, ExpandedBytes: 1 << 20, ReserveBytes: 32 << 30}
			if err := diskReserve(root, 1<<20, budget.ReserveBytes); err != nil {
				t.Skip("fixture disk reserve unavailable")
			}
			ctx := context.Background()
			switch name {
			case "compressed":
				budget.CompressedBytes = 1
			case "expanded":
				budget.ExpandedBytes = 100
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "generation":
				lock.Timestamp = "different generation"
			case "digest":
				lock.Packages[0].SHA256 = "0123456789012345678901234567890123456789012345678901234567890123"
			}
			if err := PrepareScoutPackages(ctx, "testdata/scout", out, lock, budget); err == nil {
				t.Fatal("bad input accepted")
			}
			if _, err := os.Stat(filepath.Join(out, "receipt.json")); !os.IsNotExist(err) {
				t.Fatal("failed candidate published")
			}
		})
	}
}

// The pathname can change after opening; later indexes must match the receipt
// belonging to the already-owned file, not merely agree with each other on disk.
func TestPreparedReaderRejectsReboundIndex(t *testing.T) {
	ctx := context.Background()
	dir := syntheticPrepared(t)
	s, err := OpenPreparedRouter(ctx, dir, pageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Reader.Close()
	graph, err := os.ReadFile(filepath.Join(dir, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	changed := append([]byte("\n"), graph...)
	if err := os.WriteFile(filepath.Join(dir, "receipt.json"), changed, 0600); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "potential.json"))
	if err != nil {
		t.Fatal(err)
	}
	var pin potentialReceipt
	if err := json.Unmarshal(b, &pin); err != nil {
		t.Fatal(err)
	}
	pin.GraphReceiptSHA256 = hexSum(changed)
	b, err = json.Marshal(pin)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "potential.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.EnablePotential(dir); err == nil {
		t.Fatal("index for replaced receipt attached to old reader")
	}

}

func TestPreparedExplicitPinsAndCompressionCorruption(t *testing.T) {
	for _, kind := range []string{"tile-sha", "tile-size", "missing-tile", "duplicate-pin", "bzip-crc"} {
		t.Run(kind, func(t *testing.T) {
			lock := syntheticScoutLock(t)
			root := t.TempDir()
			budget := ScoutBudgets{CompressedBytes: 1 << 20, ExpandedBytes: 1 << 20, ReserveBytes: 32 << 30}
			for i, p := range lock.Packages {
				b, e := os.ReadFile(filepath.Join("testdata/scout", p.ID+".tar.bz2"))
				if e != nil {
					t.Fatal(e)
				}
				if kind == "bzip-crc" && i == 0 {
					b[len(b)-2] ^= 128
					lock.Packages[i].SHA256 = hexSum(b)
				}
				if e = os.WriteFile(filepath.Join(root, p.ID+".tar.bz2"), b, 0600); e != nil {
					t.Fatal(e)
				}
			}
			switch kind {
			case "tile-sha":
				lock.Packages[0].Tiles[0].SHA256 = hexSum([]byte("wrong"))
			case "tile-size":
				lock.Packages[0].Tiles[0].Bytes++
			case "missing-tile":
				lock.Packages[0].Tiles[0].Name = "valhalla/tiles/2/000/000.gph.gz"
			case "duplicate-pin":
				lock.Packages[0].Tiles = append(lock.Packages[0].Tiles, lock.Packages[0].Tiles[0])
			}
			out := filepath.Join(root, "prepared")
			if e := PrepareScoutPackages(context.Background(), root, out, lock, budget); e == nil {
				t.Fatal("invalid pins or compression accepted")
			}
			if _, e := os.Stat(filepath.Join(out, "receipt.json")); !os.IsNotExist(e) {
				t.Fatal("failed preparation published")
			}
		})
	}
}
