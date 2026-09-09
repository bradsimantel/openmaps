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
	paged := flag.Bool("page-cache", false, "use pages for the original tar backend (matched cache experiment)")
	scout := flag.String("scout-packages", "", "directory of pinned OSM Scout packages (separate backend)")
	scratch := flag.String("scratch", "", "existing directory for temporary Scout spool")
	cold := flag.Bool("cold-cache", false, "clear application cache after preprocessing")
	archive := flag.String("tiles", "data/valhalla-feasibility/bremen.tar", "pinned uncompressed tar")
	lockPath := flag.String("lock", "imports/valhalla-bremen.lock.json", "pinned source lock")
	cache := flag.Int64("cache-mib", 16, "retained tile/page payload budget (maximum 64 MiB)")
	from := flag.String("from", "8.745062,53.083827", "origin longitude,latitude")
	to := flag.String("to", "8.765082,53.085226", "destination longitude,latitude")
	repeat := flag.Int("repeat", 1, "route repetitions (1..100)")
	labels := flag.Int("max-labels", 100000, "per-route label budget")
	audit := flag.Bool("audit", false, "scan sample references/geometry; Scout reports external dependencies")
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
	var r *valhallatiles.Reader
	if *scout != "" {
		var pin valhallatiles.ScoutLock
		if err := json.Unmarshal(data, &pin); err != nil {
			return err
		}
		r, err = valhallatiles.OpenScout(context.Background(), *scout, *scratch, pin, *cache<<20)
	} else {
		r, err = valhallatiles.Open(*archive, lock.SHA256, *cache<<20)
	}
	if err != nil {
		return err
	}
	defer r.Close()
	if *paged {
		if err := r.UsePageCache(); err != nil {
			return err
		}
	}
	router, err := valhallatiles.NewRouter(r)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if *audit {
		report, err := router.AuditSample(*scout != "")
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
	if *cold {
		r.ClearCache()
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
