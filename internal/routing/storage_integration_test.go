//go:build integration && (darwin || linux)

package routing

import (
	"context"
	"database/sql"
	"encoding/binary"
	"math"
	"math/rand/v2"
	"os"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// TestCoordinateStorageStrategies compares the same fixed-width coordinate
// accesses. This isolates page-access overhead, not an end-to-end mmap router.
func TestCoordinateStorageStrategies(t *testing.T) {
	path := os.Getenv("OPENMAPS_PERF_DB")
	out := os.Getenv("OPENMAPS_STORAGE_FILE")
	if path == "" || out == "" {
		t.Skip("set OPENMAPS_PERF_DB and OPENMAPS_STORAGE_FILE")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	m, _, _, err := readManifest(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	points := []Point{}
	for _, c := range m.Chunks {
		if c.Kind == "nodes" {
			ns, err := readChunk[Node](ctx, db, c)
			if err != nil {
				t.Fatal(err)
			}
			for _, n := range ns {
				points = append(points, n.Point)
			}
		}
	}
	f, err := os.OpenFile(out, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	raw := make([]byte, 16*len(points))
	for i, p := range points {
		binary.LittleEndian.PutUint64(raw[i*16:], math.Float64bits(p[0]))
		binary.LittleEndian.PutUint64(raw[i*16+8:], math.Float64bits(p[1]))
	}
	if _, err = f.Write(raw); err != nil {
		t.Fatal(err)
	}
	raw = nil
	mapped, err := syscall.Mmap(int(f.Fd()), 0, len(points)*16, syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Munmap(mapped)
	t.Logf("coordinates=%d array_bytes=%d mapped_virtual_bytes=%d bounded_cache_bytes=%d", len(points), len(points)*16, len(mapped), 8*65536)
	for _, pattern := range []string{"sequential", "scattered"} {
		rng := rand.New(rand.NewPCG(10, 9))
		ids := make([]int, 100000)
		for i := range ids {
			ids[i] = i % len(points)
			if pattern == "scattered" {
				ids[i] = rng.IntN(len(points))
			}
		}
		type page struct {
			id  int
			raw []byte
		}
		cache := make([]page, 8)
		for i := range cache {
			cache[i] = page{-1, make([]byte, 65536)}
		}
		misses := 0
		getPage := func(i int) float64 {
			number := i * 16 / 65536
			slot := number % len(cache)
			p := &cache[slot]
			if p.id != number {
				n, e := f.ReadAt(p.raw, int64(number*65536))
				if n == 0 || e != nil && n < 16 {
					t.Fatal(e)
				}
				p.id = number
				misses++
			}
			return math.Float64frombits(binary.LittleEndian.Uint64(p.raw[i*16%65536:]))
		}
		for _, mode := range []string{"array", "mmap", "bounded-readat"} {
			for pass := 1; pass <= 2; pass++ {
				start := time.Now()
				sum := 0.0
				for _, i := range ids {
					switch mode {
					case "array":
						sum += points[i][0]
					case "mmap":
						sum += math.Float64frombits(binary.LittleEndian.Uint64(mapped[i*16:]))
					default:
						sum += getPage(i)
					}
				}
				t.Logf("pattern=%s mode=%s pass=%d ns_per_access=%.1f checksum=%.6f cache_misses=%d", pattern, mode, pass, float64(time.Since(start).Nanoseconds())/float64(len(ids)), sum, misses)
			}
		}
	}
	runtime.KeepAlive(points)
}
