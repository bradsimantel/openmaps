// routing-valhalla is an offline feasibility harness, not a server or importer.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"openmaps/internal/routing/valhallatiles"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func point(value string) (valhallatiles.Point, error) {
	v := strings.Split(value, ",")
	if len(v) != 2 {
		return valhallatiles.Point{}, fmt.Errorf("expected longitude,latitude")
	}
	var p valhallatiles.Point
	for i := range p {
		x, err := strconv.ParseFloat(v[i], 64)
		if err != nil {
			return p, err
		}
		p[i] = x
	}
	return p, nil
}
func run() error {
	archive := flag.String("tiles", "data/valhalla-feasibility/bremen.tar", "pinned uncompressed tar")
	lockPath := flag.String("lock", "imports/valhalla-bremen.lock.json", "pinned source lock")
	cache := flag.Int64("cache-mib", 16, "retained tile-payload budget (maximum 64 MiB)")
	from := flag.String("from", "8.745062,53.083827", "origin longitude,latitude")
	to := flag.String("to", "8.765082,53.085226", "destination longitude,latitude")
	repeat := flag.Int("repeat", 1, "route repetitions (1..100)")
	labels := flag.Int("max-labels", 100000, "per-route label budget")
	audit := flag.Bool("audit", false, "scan full small sample and verify references/geometry")
	timeout := flag.Duration("timeout", 30*time.Second, "per-route timeout")
	flag.Parse()
	if *repeat < 1 || *repeat > 100 || *cache < 1 || *cache > 64 {
		return fmt.Errorf("invalid repetition/cache budget")
	}
	data, err := os.ReadFile(*lockPath)
	if err != nil {
		return err
	}
	var lock struct {
		SHA256 string `json:"sha256"`
	}
	if err = json.Unmarshal(data, &lock); err != nil {
		return err
	}
	start := time.Now()
	r, err := valhallatiles.Open(*archive, lock.SHA256, *cache<<20)
	if err != nil {
		return err
	}
	defer r.Close()
	router, err := valhallatiles.NewRouter(r)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if *audit {
		report, err := router.Audit()
		if err != nil {
			return err
		}
		return enc.Encode(report)
	}
	loadMS := float64(time.Since(start).Microseconds()) / 1000
	a, err := point(*from)
	if err != nil {
		return err
	}
	b, err := point(*to)
	if err != nil {
		return err
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	var route valhallatiles.Result
	times := make([]float64, 0, *repeat)
	for i := 0; i < *repeat; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		start = time.Now()
		route, err = router.Route(ctx, a, b, *labels)
		cancel()
		if err != nil {
			return err
		}
		times = append(times, float64(time.Since(start).Microseconds())/1000)
	}
	runtime.ReadMemStats(&after)
	allocation := after.TotalAlloc - before.TotalAlloc
	heapBeforeGC := after.HeapAlloc
	runtime.GC()
	runtime.ReadMemStats(&after)
	return enc.Encode(struct {
		LoadMilliseconds                                                     float64
		RouteMilliseconds                                                    []float64
		TotalAllocatedBytes, HeapBeforeGCBytes, HeapAfterGCBytes, GoSysBytes uint64
		Cache                                                                valhallatiles.CacheStats
		Route                                                                valhallatiles.Result
	}{loadMS, times, allocation, heapBeforeGC, after.HeapAlloc, after.Sys, r.Stats, route})
}
