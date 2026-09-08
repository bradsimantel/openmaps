//go:build integration

package routing

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRoutingPerformance is an opt-in bounded, warm-file-cache regional workload.
// /usr/bin/time -l around the compiled test binary records process peak RSS.
func TestRoutingPerformance(t *testing.T) {
	path := os.Getenv("OPENMAPS_PERF_DB")
	if path == "" {
		t.Skip("set OPENMAPS_PERF_DB")
	}
	fixture := os.Getenv("OPENMAPS_PERF_CASES")
	if fixture == "" {
		fixture = "testdata/newport.json"
	}
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name                string
		Origin, Destination Point
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	started := time.Now()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	load := time.Since(started).Seconds()
	var before, retained runtime.MemStats
	runtime.ReadMemStats(&before)
	runtime.GC()
	runtime.ReadMemStats(&retained)
	if p := os.Getenv("OPENMAPS_HEAP"); p != "" {
		f, e := os.Create(p)
		if e != nil {
			t.Fatal(e)
		}
		pprof.WriteHeapProfile(f)
		f.Close()
	}
	type distribution struct{ P50, P95, Max float64 }
	stats := func(v []float64) distribution {
		sort.Float64s(v)
		return distribution{v[len(v)/2], v[(len(v)-1)*95/100], v[len(v)-1]}
	}
	if os.Getenv("OPENMAPS_PERF_LOAD_ONLY") == "1" {
		t.Logf("LOAD_ONLY seconds=%.3f retained_heap_mib=%.2f", load, float64(retained.HeapAlloc)/1048576)
		runtime.KeepAlive(s)
		return
	}
	snaps := []float64{}
	for _, c := range cases {
		start := time.Now()
		s.snap(ctx, c.Origin, "origin")
		s.snap(ctx, c.Destination, "destination")
		snaps = append(snaps, float64(time.Since(start).Microseconds())/1000)
	}
	t.Logf("LOAD seconds=%.3f pre_gc_heap_mib=%.2f retained_heap_mib=%.2f heap_sys_mib=%.2f snap_pair_ms=%+v", load, float64(before.HeapAlloc)/1048576, float64(retained.HeapAlloc)/1048576, float64(retained.HeapSys)/1048576, stats(snaps))
	levels := []int{1, 4, 8}
	if text := os.Getenv("OPENMAPS_PERF_CONCURRENCY"); text != "" {
		levels = nil
		for _, v := range strings.Split(text, ",") {
			n, e := strconv.Atoi(v)
			if e != nil || n < 1 || n > 8 {
				t.Fatal("invalid concurrency")
			}
			levels = append(levels, n)
		}
	}
	for _, concurrency := range levels {
		var wg sync.WaitGroup
		var mu sync.Mutex
		times := []float64{}
		start := time.Now()
		for worker := 0; worker < concurrency; worker++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for repeat := 0; repeat < 2; repeat++ {
					for _, c := range cases {
						start := time.Now()
						s.Route(ctx, c.Origin, c.Destination)
						ms := float64(time.Since(start).Microseconds()) / 1000
						mu.Lock()
						times = append(times, ms)
						mu.Unlock()
					}
				}
			}()
		}
		wg.Wait()
		t.Logf("ROUTES concurrency=%d requests=%d seconds=%.3f requests_per_second=%.2f latency_ms=%+v", concurrency, len(times), time.Since(start).Seconds(), float64(len(times))/time.Since(start).Seconds(), stats(times))
	}
	runtime.KeepAlive(s)
}
