//go:build integration

package routing

import (
	"context"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadingPhases(t *testing.T) {
	path := os.Getenv("OPENMAPS_PERF_DB")
	if path == "" {
		t.Skip("set OPENMAPS_PERF_DB")
	}
	ctx := WithLoadObserver(context.Background(), func(p LoadPhase) { t.Logf("PHASE %+v", p) })
	var peak atomic.Uint64
	stop := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		for {
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > peak.Load() {
				peak.Store(m.HeapAlloc)
			}
			select {
			case <-stop:
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
	}()
	var before, after, retained runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	var s *Store
	var e error
	if dir := os.Getenv("OPENMAPS_PREPARED"); dir != "" {
		s, e = OpenPrepared(ctx, path, dir)
	} else {
		s, e = Open(ctx, path)
		if e == nil {
			done := loadPhase(ctx, "legacy artifact verification and mapping")
			e = s.UseMappedQueryData(ctx, os.Getenv("OPENMAPS_ROUTING_CACHE"))
			done()
		}
	}
	elapsed := time.Since(start)
	close(stop)
	<-finished
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	runtime.ReadMemStats(&after)
	runtime.GC()
	runtime.ReadMemStats(&retained)
	t.Logf("LOAD seconds=%.3f allocated_bytes=%d peak_heap_bytes=%d retained_heap_bytes=%d mapped_bytes=%d", elapsed.Seconds(), after.TotalAlloc-before.TotalAlloc, peak.Load(), retained.HeapAlloc, s.MappedBytes())
	runtime.KeepAlive(s)
}

func openIntegration(ctx context.Context, path string) (*Store, error) {
	if dir := os.Getenv("OPENMAPS_PREPARED"); dir != "" {
		return OpenPrepared(ctx, path, dir)
	}
	return Open(ctx, path)
}
