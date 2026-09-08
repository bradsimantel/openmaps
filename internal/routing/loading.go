package routing

import (
	"context"
	"runtime"
	"time"
)

type LoadPhase struct {
	Name                      string
	Seconds                   float64
	AllocatedBytes, HeapBytes uint64
}
type loadObserverKey struct{}

// WithLoadObserver reports sequential loading phases; it does not force GC.
func WithLoadObserver(ctx context.Context, f func(LoadPhase)) context.Context {
	return context.WithValue(ctx, loadObserverKey{}, f)
}
func loadPhase(ctx context.Context, name string) func() {
	f, ok := ctx.Value(loadObserverKey{}).(func(LoadPhase))
	if !ok {
		return func() {}
	}
	var a runtime.MemStats
	runtime.ReadMemStats(&a)
	start := time.Now()
	return func() {
		var b runtime.MemStats
		runtime.ReadMemStats(&b)
		f(LoadPhase{name, time.Since(start).Seconds(), b.TotalAlloc - a.TotalAlloc, b.HeapAlloc})
	}
}
