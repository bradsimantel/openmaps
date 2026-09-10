// scout-audit inspects prepared Scout source paths and geometry offline.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
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
	probeFile := flag.String("audit-snaps", "", "JSON point array for full-shape scan comparisons during audit")
	inspect := flag.Uint64("inspect-edge", 0, "emit one source edge, endpoints and geometry for diagnostics")
	landmarks := flag.String("landmarks", "", "prepared landmark directory (requires -accelerated)")
	accelerated := flag.Bool("accelerated", false, "use prepared validated geometric A* lower bound")
	prepared := flag.String("prepared", "", "prepared Scout snapshot directory")
	cold := flag.Bool("cold-cache", false, "clear application cache after preprocessing")
	cache := flag.Int64("cache-mib", 16, "retained page payload budget, maximum 128 MiB")
	from := flag.String("from", "8.745062,53.083827", "origin longitude,latitude")
	to := flag.String("to", "8.765082,53.085226", "destination longitude,latitude")
	repeat := flag.Int("repeat", 1, "route repetitions (1..100)")
	labels := flag.Int("max-labels", 100000, "per-route label budget")
	audit := flag.Bool("audit", false, "scan sample references/geometry; Scout reports external dependencies")
	timeout := flag.Duration("timeout", 30*time.Second, "per-route timeout")
	flag.Parse()
	if *repeat < 1 || *repeat > 100 || *cache < 1 || *cache > 128 || *prepared == "" {
		return fmt.Errorf("invalid repetition/cache budget")
	}
	lifetime, cancelLifetime := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelLifetime()
	start := time.Now()
	router, err := valhallatiles.OpenPreparedRouter(lifetime, *prepared, *cache<<20)
	if err != nil {
		return err
	}
	r := router.Reader
	defer r.Close()
	if *accelerated {
		if *prepared == "" {
			return fmt.Errorf("acceleration requires persistent preparation")
		}
		if err := router.EnablePotential(*prepared); err != nil {
			return err
		}
	}
	if *landmarks != "" {
		if !*accelerated {
			return fmt.Errorf("landmarks require one-sided -accelerated search")
		}
		if err := router.EnableLandmarks(lifetime, *prepared, *landmarks); err != nil {
			return err
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if *inspect != 0 {
		e, err := r.Edge(valhallatiles.ID(*inspect))
		if err != nil {
			return err
		}
		a, err := r.Start(e.ID)
		if err != nil {
			return err
		}
		b, err := r.Node(e.End)
		var endError string
		if err != nil {
			endError = err.Error()
		}
		shape, err := r.Shape(e)
		if err != nil {
			return err
		}
		allowed, err := router.Allowed(e)
		if err != nil {
			return err
		}
		return enc.Encode(map[string]any{"edge": e, "start": a, "end": b, "end_error": endError, "shape": shape, "allowed": allowed, "package": r.Package(e.ID)})
	}
	if *audit {
		var probes []valhallatiles.Point
		if *probeFile != "" {
			b, err := os.ReadFile(*probeFile)
			if err != nil {
				return err
			}
			if len(b) > 65536 {
				return fmt.Errorf("audit probe file oversized")
			}
			if err := json.Unmarshal(b, &probes); err != nil {
				return err
			}
		}
		report, err := router.AuditWithSnaps(lifetime, true, probes)
		if err != nil {
			enc.Encode(map[string]any{"partial_audit": report, "error": err.Error()})
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
		ctx, cancel := context.WithTimeout(lifetime, *timeout)
		start = time.Now()
		if *accelerated {
			route, err = router.RouteAccelerated(ctx, a, b, *labels)
		} else {
			route, err = router.Route(ctx, a, b, *labels)
		}
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
		Cache                                                                valhallatiles.CacheReport
		Route                                                                valhallatiles.Result
	}{loadMS, times, allocation, heapBeforeGC, after.HeapAlloc, after.Sys, r.CacheReport(), route})
}
