//go:build integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"openmaps/internal/routing/qualification"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// This test requires an explicitly isolated service and selection file. It never
// discovers or modifies a deployment, and restores the initial selection.
func TestNationalServiceRoundTrip(t *testing.T) {
	base, selection, alternate, offline := os.Getenv("OPENMAPS_SCOUT_TEST_URL"), os.Getenv("OPENMAPS_SCOUT_TEST_SELECTION"), os.Getenv("OPENMAPS_SCOUT_TEST_ALTERNATE"), os.Getenv("OPENMAPS_SCOUT_TEST_OFFLINE")
	if base == "" || selection == "" || alternate == "" || offline == "" {
		t.Skip("requires explicitly isolated Scout service, selection, alternate and offline evidence")
	}
	initial, e := os.ReadFile(selection)
	if e != nil {
		t.Fatal(e)
	}
	defer func() {
		if e := atomicSelection(selection, initial); e != nil {
			t.Error(e)
		}
	}()
	rows, e := qualification.LoadCases([]string{offline})
	if e != nil {
		t.Fatal(e)
	}
	pick := func(name string) qualification.Offline {
		for _, r := range rows {
			if strings.Contains(r.Case.Name, name) {
				return r
			}
		}
		t.Fatalf("missing frozen case %s", name)
		return qualification.Offline{}
	}
	cheap, expensive := pick("Anchorage to Seattle"), pick("Dallas to Washington")
	client := &http.Client{Timeout: 40 * time.Second}
	defer client.CloseIdleConnections()
	request := func(ctx context.Context, row qualification.Offline, mask string) (int, []byte, http.Header, error) {
		req, e := http.NewRequestWithContext(ctx, "POST", base+"/directions/v2:computeRoutes", bytes.NewReader(qualification.Body(row.Case)))
		if e != nil {
			return 0, nil, nil, e
		}
		req.Header.Set("X-Goog-FieldMask", mask)
		response, e := client.Do(req)
		if e != nil {
			return 0, nil, nil, e
		}
		defer response.Body.Close()
		b, e := io.ReadAll(io.LimitReader(response.Body, qualification.ResponseLimit+1))
		return response.StatusCode, b, response.Header, e
	}
	health := func() (string, string, string, error) {
		r, e := client.Get(base + "/healthz")
		if e != nil {
			return "", "", "", e
		}
		defer r.Body.Close()
		var h struct {
			Routing struct{ Directory, Snapshot string } `json:"routing_candidate"`
			Error   string                               `json:"reload_error"`
			Lookup  bool                                 `json:"lookup_available"`
		}
		e = json.NewDecoder(r.Body).Decode(&h)
		if !h.Lookup {
			return "", "", "", fmt.Errorf("lookup missing from unified service")
		}
		return h.Routing.Directory, h.Routing.Snapshot, h.Error, e
	}
	initialDir, initialID, _, e := health()
	if e != nil {
		t.Fatal(e)
	}
	// All eight clients start together, so the two-reader budget rejects excess work.
	start := make(chan struct{})
	codes := make(chan int, 8)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			<-start
			status, b, h, e := request(context.Background(), expensive, qualification.Mask)
			if e != nil {
				t.Error(e)
			} else if status == 200 {
				if _, e = qualification.CheckResponse(expensive, status, b); e != nil {
					t.Error(e)
				}
			} else if status != 429 || h.Get("Retry-After") != "1" {
				t.Errorf("unexpected admission: %d %s", status, b)
			}
			codes <- status
		})
	}
	close(start)
	wg.Wait()
	close(codes)
	counts := map[int]int{}
	for status := range codes {
		counts[status]++
	}
	if counts[200] != 2 || counts[429] != 6 {
		t.Fatalf("admission counts %v", counts)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); request(ctx, expensive, qualification.Mask) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	<-done
	time.Sleep(time.Second)
	for range 2 {
		wg.Go(func() {
			status, b, _, e := request(context.Background(), cheap, qualification.Mask)
			if e != nil {
				t.Error(e)
				return
			}
			if _, e = qualification.CheckResponse(cheap, status, b); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	status, b, _, e := request(context.Background(), cheap, "routes.distanceMeters")
	if e != nil || status != 200 {
		t.Fatal(status, e)
	}
	var masked struct{ Routes []map[string]any }
	json.Unmarshal(b, &masked)
	if len(masked.Routes) != 1 || len(masked.Routes[0]) != 1 {
		t.Fatal("mask leaked route fields")
	}
	status, b, _, e = request(context.Background(), cheap, "routes")
	if e != nil || status != 400 || !bytes.Contains(b, []byte("unsupported_input")) {
		t.Fatal(status, e, string(b))
	}
	for _, body := range []string{`{"origin":{"address":"Newport"},"destination":{"address":"Boston"},"polylineEncoding":"GEO_JSON_LINESTRING"}`, `{"origin":{"location":{"latLng":{"longitude":180,"latitude":65}}},"destination":{"location":{"latLng":{"longitude":-180,"latitude":65}}},"polylineEncoding":"GEO_JSON_LINESTRING"}`} {
		req, _ := http.NewRequest("POST", base+"/directions/v2:computeRoutes", strings.NewReader(body))
		req.Header.Set("X-Goog-FieldMask", "routes.duration")
		r, e := client.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		b, _ := io.ReadAll(r.Body)
		r.Body.Close()
		expected := "incomplete_data"
		if strings.Contains(body, "address") {
			expected = "address_routing_unavailable"
		}
		if r.StatusCode != 503 || !bytes.Contains(b, []byte(expected)) {
			t.Fatal(r.StatusCode, string(b))
		}
	}
	wait := func(wantDir string, wantError bool) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Minute)
		for time.Now().Before(deadline) {
			dir, _, msg, e := health()
			if e == nil && dir == wantDir && (msg != "") == wantError {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("selection did not settle")
	}
	for _, bad := range [][]byte{[]byte(`{"directory":"does-not-exist"}`), []byte(`{broken`)} {
		if e := atomicSelection(selection, bad); e != nil {
			t.Fatal(e)
		}
		wait(initialDir, true)
		expected := "does-not-exist"
		if string(bad) == "{broken" {
			expected = "requires directory"
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			_, _, message, err := health()
			if err == nil && strings.Contains(message, expected) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("specific selection error not observed", expected, message)
			}
			time.Sleep(20 * time.Millisecond)
		}
		_, id, _, _ := health()
		if id != initialID {
			t.Fatal("failed load changed snapshot")
		}
	}
	if e = atomicSelection(selection, initial); e != nil {
		t.Fatal(e)
	}
	wait(initialDir, false)
	// Compare full returned paths throughout loading and retirement of both graphs.
	trafficCtx, stopTraffic := context.WithCancel(context.Background())
	var completed, failed, totalBytes atomic.Int64
	var snapshotMu sync.Mutex
	seen := map[string]int{}
	for range 2 {
		wg.Go(func() {
			for trafficCtx.Err() == nil {
				status, b, _, e := request(trafficCtx, cheap, qualification.Mask)
				if trafficCtx.Err() != nil {
					return
				}
				if e != nil {
					failed.Add(1)
					continue
				}
				id, e := qualification.CheckResponse(cheap, status, b)
				if e != nil {
					failed.Add(1)
				} else {
					completed.Add(1)
					totalBytes.Add(int64(len(b)))
					snapshotMu.Lock()
					seen[id]++
					snapshotMu.Unlock()
				}
			}
		})
	}
	defer func() { stopTraffic(); wg.Wait() }()
	next, _ := json.Marshal(map[string]string{"directory": alternate})
	if e = atomicSelection(selection, next); e != nil {
		t.Fatal(e)
	}
	altAbs, _ := filepath.Abs(alternate)
	wait(altAbs, false)
	// Ensure publication is observed by traffic before switching back.
	time.Sleep(time.Second)
	if e = atomicSelection(selection, initial); e != nil {
		t.Fatal(e)
	}
	wait(initialDir, false)
	time.Sleep(time.Second)
	stopTraffic()
	wg.Wait()
	_, id, _, e := health()
	if e != nil || id != initialID || failed.Load() != 0 || len(seen) != 2 {
		t.Fatalf("roundtrip failed: restored=%s expected=%s errors=%d snapshots=%v error=%v", id, initialID, failed.Load(), seen, e)
	}
	summary, _ := json.Marshal(map[string]any{"requests": completed.Load(), "failed": failed.Load(), "bytes": totalBytes.Load(), "snapshots": seen, "admission": counts, "restored": id})
	t.Log(string(summary))
}
func atomicSelection(path string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), "selection-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, e = f.Write(b); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}
