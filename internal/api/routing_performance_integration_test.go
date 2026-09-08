//go:build integration

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"openmaps/internal/routing"
)

// Uses a private loopback server. Encode samples re-encode actual HTTP envelopes;
// handler residual includes parse, translation, encoding and loopback transport.
func TestRoutingHTTPPerformance(t *testing.T) {
	path, fixture := os.Getenv("OPENMAPS_PERF_DB"), os.Getenv("OPENMAPS_PERF_CASES")
	if path == "" || fixture == "" {
		t.Skip("set performance database and cases")
	}
	ctx := context.Background()
	s, err := routing.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UseMappedQueryData(ctx, os.Getenv("OPENMAPS_ROUTING_CACHE")); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	raw, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name                string
		Origin, Destination routing.Point
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	observations := []routing.SearchMetrics{}
	cancelEntered, cancelFinished := make(chan struct{}, 1), make(chan routing.SearchMetrics, 1)
	handler := Handler{Routing: s}
	server := httptest.NewServer(RoutingAdmission(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var m routing.SearchMetrics
		cancelling := r.Header.Get("X-OpenMaps-Test-Cancel") == "1"
		if cancelling {
			cancelEntered <- struct{}{}
		}
		handler.ServeHTTP(w, r.WithContext(routing.WithSearchMetrics(r.Context(), &m)))
		if cancelling {
			cancelFinished <- m
		}
		mu.Lock()
		observations = append(observations, m)
		mu.Unlock()
	}), 4))
	defer server.Close()
	client := &http.Client{Timeout: 30 * time.Second}
	body := func(a, b routing.Point) []byte {
		wp := func(p routing.Point) any {
			return object{"location": object{"latLng": object{"longitude": p[0], "latitude": p[1]}}}
		}
		raw, _ := json.Marshal(object{"origin": wp(a), "destination": wp(b), "polylineEncoding": "GEO_JSON_LINESTRING"})
		return raw
	}
	request := func(ctx context.Context, b []byte) ([]byte, error) {
		r, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/directions/v2:computeRoutes", bytes.NewReader(b))
		r.Header.Set("X-Goog-FieldMask", "routes.distanceMeters,routes.duration,routes.staticDuration,routes.polyline")
		resp, err := client.Do(r)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 && resp.StatusCode != 400 {
			return raw, fmt.Errorf("unexpected HTTP %d: %s", resp.StatusCode, raw)
		}
		return raw, err
	}
	for _, c := range cases {
		requestStart := time.Now()
		raw, err := request(ctx, body(c.Origin, c.Destination))
		elapsed := time.Since(requestStart)
		if err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		m := observations[len(observations)-1]
		mu.Unlock()
		t.Logf("HTTP_PHASE name=%q snap_us=%d search_us=%d geometry_us=%d residual_us=%d", c.Name, m.Snap.Microseconds(), m.Search.Microseconds(), m.Geometry.Microseconds(), (elapsed - m.Snap - m.Search - m.Geometry).Microseconds())
		var envelope any
		if err = json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		for i := 0; i < 20; i++ {
			if err := json.NewEncoder(io.Discard).Encode(envelope); err != nil {
				t.Fatal(err)
			}
		}
		t.Logf("ENCODE name=%q bytes=%d mean_us=%.3f", c.Name, len(raw), float64(time.Since(started).Nanoseconds())/20000)
	}
	for _, workers := range []int{1, 2, 4} {
		mu.Lock()
		observations = nil
		mu.Unlock()
		var wg sync.WaitGroup
		var timingMu sync.Mutex
		times := []float64{}
		errs := make(chan error, workers)
		started := time.Now()
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for repeat := 0; repeat < 2; repeat++ {
					for _, c := range cases {
						start := time.Now()
						_, err := request(ctx, body(c.Origin, c.Destination))
						if err != nil {
							errs <- err
							return
						}
						timingMu.Lock()
						times = append(times, float64(time.Since(start).Microseconds())/1000)
						timingMu.Unlock()
					}
				}
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatal(err)
		}
		elapsed := time.Since(started).Seconds()
		sort.Float64s(times)
		t.Logf("HTTP concurrency=%d requests=%d rps=%.2f p50_ms=%.3f p95_ms=%.3f max_ms=%.3f", workers, len(times), float64(len(times))/elapsed, times[len(times)/2], times[(len(times)-1)*95/100], times[len(times)-1])
	}
	for _, c := range cases {
		if !strings.Contains(c.Name, "Portland to Ashland") && !strings.Contains(c.Name, "Seattle to Boise") {
			continue
		}
		query, cancel := context.WithCancel(ctx)
		r, _ := http.NewRequestWithContext(query, "POST", server.URL+"/directions/v2:computeRoutes", bytes.NewReader(body(c.Origin, c.Destination)))
		r.Header.Set("X-Goog-FieldMask", "routes.distanceMeters,routes.duration,routes.polyline")
		r.Header.Set("X-OpenMaps-Test-Cancel", "1")
		finished := make(chan error, 1)
		go func() {
			response, err := client.Do(r)
			if response != nil {
				response.Body.Close()
			}
			finished <- err
		}()
		select {
		case <-cancelEntered:
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatal("HTTP request did not enter")
		}
		time.Sleep(10 * time.Millisecond)
		start := time.Now()
		cancel()
		if err := <-finished; !errors.Is(err, context.Canceled) {
			t.Fatal("HTTP cancellation not observed", err)
		}
		select {
		case m := <-cancelFinished:
			t.Logf("HTTP_CANCEL name=%q release_ms=%.3f expanded=%d search_us=%d", c.Name, float64(time.Since(start).Microseconds())/1000, m.Expanded, m.Search.Microseconds())
		case <-time.After(2 * time.Second):
			t.Fatal("canceled HTTP search retained resources")
		}
		if _, err := request(ctx, body(c.Origin, c.Origin)); err != nil {
			t.Fatal("cancellation damaged next request", err)
		}
		break
	}
	runtime.KeepAlive(s)
}
