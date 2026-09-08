//go:build integration

package dataset

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"openmaps/internal/api"
	"openmaps/internal/routing"
)

// Only temporary deployment state is changed. Retained files are read-only.
func TestRegionalSnapshotLifetime(t *testing.T) {
	base, next := os.Getenv("OPENMAPS_LIFETIME_BASELINE"), os.Getenv("OPENMAPS_LIFETIME_CANDIDATE")
	if base == "" || next == "" {
		t.Skip("set lifetime snapshot paths")
	}
	ctx := context.Background()
	bf, err := Describe(base)
	if err != nil {
		t.Fatal(err)
	}
	nf, err := Describe(next)
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "deployment.json")
	if err := Change(state, func(s *State) error { *s = State{Schema: 1, Baseline: bf, Current: bf}; return nil }); err != nil {
		t.Fatal(err)
	}
	live, err := OpenWithRoutingCache(ctx, state, os.Getenv("OPENMAPS_ROUTING_CACHE"))
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	old := live.router
	oldMapped := old.MappedBytes()
	server := httptest.NewServer(api.RoutingAdmission(live, 4))
	defer server.Close()
	point := routing.Point{-122.67966965, 45.51925845}
	if old.Metadata().EndpointBounds[0] > -100 {
		point = routing.Point{-71.31373108, 41.49138952}
	}
	wp := map[string]any{"location": map[string]any{"latLng": map[string]float64{"longitude": point[0], "latitude": point[1]}}}
	body, _ := json.Marshal(map[string]any{"origin": wp, "destination": wp, "polylineEncoding": "GEO_JSON_LINESTRING"})
	timeout := 90 * time.Second
	if value := os.Getenv("OPENMAPS_LIFETIME_TIMEOUT"); value != "" {
		var err error
		timeout, err = time.ParseDuration(value)
		if err != nil || timeout < 90*time.Second || timeout > 10*time.Minute {
			t.Fatal("invalid lifetime timeout", value)
		}
	}
	client := &http.Client{Timeout: timeout}
	request := func() error {
		r, _ := http.NewRequest("POST", server.URL+"/directions/v2:computeRoutes", bytes.NewReader(body))
		r.Header.Set("X-Goog-FieldMask", "routes.distanceMeters,routes.duration")
		res, e := client.Do(r)
		if e != nil {
			return e
		}
		defer res.Body.Close()
		raw, e := io.ReadAll(res.Body)
		if e != nil {
			return e
		}
		hash := res.Header.Get("X-OpenMaps-Dataset")
		var parsed map[string]any
		if e = json.Unmarshal(raw, &parsed); e != nil {
			return e
		}
		meta, _ := parsed["openmaps"].(map[string]any)
		if hash != bf.SHA256 && hash != nf.SHA256 {
			return fmt.Errorf("mixed/unknown snapshot header %s", hash)
		}
		if hash == bf.SHA256 && (res.StatusCode != 200 || meta["source_release"] != old.Metadata().Release) {
			return fmt.Errorf("old snapshot response mismatch: %s", raw)
		}
		if hash == nf.SHA256 && hash != bf.SHA256 {
			if point[0] > -100 {
				if res.StatusCode != 400 || meta["outcome"] != "outside_coverage" {
					return fmt.Errorf("new snapshot response mismatch: %s", raw)
				}
			} else if res.StatusCode != 200 {
				return fmt.Errorf("new routing unavailable: %s", raw)
			}
		}
		return nil
	}
	if err := request(); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	errs := make(chan error, 4)
	var workers sync.WaitGroup
	for i := 0; i < 4; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if e := request(); e != nil {
					errs <- e
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
	}
	var peak uint64
	sampleStop := make(chan struct{})
	sampleDone := make(chan struct{})
	go func() {
		defer close(sampleDone)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-sampleStop:
				return
			case <-tick.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				if m.HeapAlloc > peak {
					peak = m.HeapAlloc
				}
			}
		}
	}()
	start := time.Now()
	if err := Change(state, func(s *State) error { s.Previous = &bf; s.Current = nf; return nil }); err != nil {
		t.Fatal(err)
	}
	// A worker may own the load. Poll health at a bounded rate until publication.
	deadline := time.Now().Add(timeout)
	for {
		res, e := client.Get(server.URL + "/healthz")
		if e != nil {
			t.Fatal(e)
		}
		var h struct {
			Dataset File
			Error   string
		}
		json.NewDecoder(res.Body).Decode(&h)
		res.Body.Close()
		if h.Error != "" {
			t.Fatal(h.Error)
		}
		if h.Dataset == nf {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reload timeout")
		}
		time.Sleep(50 * time.Millisecond)
	}
	publication := time.Since(start)
	close(stop)
	workers.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	close(sampleStop)
	<-sampleDone
	runtime.GC()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	t.Logf("SWITCH seconds=%.3f publication_seconds=%.3f prior_90s_gate=%t sampled_peak_heap_mib=%.2f retained_two_graph_heap_mib=%.2f", time.Since(start).Seconds(), publication.Seconds(), publication <= 90*time.Second, float64(peak)/1048576, float64(mem.HeapAlloc)/1048576)
	if old != live.router {
		if old.MappedBytes() != 0 {
			t.Fatal("retired mapping retained")
		}
		if _, err := old.Route(ctx, point, point); err == nil {
			t.Fatal("retired router still accepts queries")
		}
	}
	t.Logf("MAPPING old_bytes=%d new_bytes=%d overlap_virtual_bytes=%d retired_bytes=%d", oldMapped, live.router.MappedBytes(), oldMapped+live.router.MappedBytes(), old.MappedBytes())
	if err := request(); err != nil {
		t.Fatal(err)
	}
	// Corrupt selection is isolated to this temporary state, never the source files.
	badPath := filepath.Join(filepath.Dir(state), "bad.sqlite")
	os.WriteFile(badPath, []byte("not sqlite"), 0600)
	bad, _ := Describe(badPath)
	if err := Change(state, func(s *State) error { s.Previous = &nf; s.Current = bad; return nil }); err != nil {
		t.Fatal(err)
	}
	res, err := client.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 503 {
		t.Fatal("invalid selection not reported")
	}
	if err := request(); err != nil {
		t.Fatal("failure invalidated previous snapshot", err)
	}
	if err := Rollback(ctx, state); err != nil {
		t.Fatal(err)
	}
	if err := request(); err != nil {
		t.Fatal(err)
	}
	// Restore the actual baseline too, exercising graph and address-domain rollback.
	if err := Change(state, func(s *State) error { s.Previous = &bf; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(ctx, state); err != nil {
		t.Fatal(err)
	}
	if err := request(); err != nil {
		t.Fatal(err)
	}
	runtime.KeepAlive(old)
}
