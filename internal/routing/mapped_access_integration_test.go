//go:build integration && (darwin || linux)

package routing

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// DONTNEED is a mapping-residency hint, not a system file-cache purge. The
// observed fault counts, not the hint alone, describe first-access behavior.
func TestMappedRoutingAccess(t *testing.T) {
	path, cache, casesPath := os.Getenv("OPENMAPS_PERF_DB"), os.Getenv("OPENMAPS_ROUTING_CACHE"), os.Getenv("OPENMAPS_PERF_CASES")
	if path == "" || cache == "" && os.Getenv("OPENMAPS_PREPARED") == "" || casesPath == "" {
		t.Skip("set mapped performance paths")
	}
	started := time.Now()
	var s *Store
	var err error
	if dir := os.Getenv("OPENMAPS_PREPARED"); dir != "" {
		s, err = OpenPrepared(context.Background(), path, dir)
	} else {
		s, err = OpenMapped(context.Background(), path, cache)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	load := time.Since(started).Seconds()
	debug.FreeOSMemory()
	report := func(stage string) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		var u syscall.Rusage
		syscall.Getrusage(syscall.RUSAGE_SELF, &u)
		rss, _ := exec.Command("ps", "-o", "rss=,vsz=", "-p", strconv.Itoa(os.Getpid())).Output()
		t.Logf("MEMORY stage=%s load_seconds=%.3f mapped_bytes=%d heap_bytes=%d rss_vsz_kib=%q minor_faults=%d major_faults=%d", stage, load, s.MappedBytes(), m.HeapAlloc, strings.TrimSpace(string(rss)), u.Minflt, u.Majflt)
	}
	report("validated")
	raw, err := os.ReadFile(casesPath)
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
	if err := unix.Madvise(s.mapping.data, unix.MADV_DONTNEED); err != nil {
		t.Fatal(err)
	}
	report("advised_dontneed")
	for pass := 1; pass <= 2; pass++ {
		var before, after syscall.Rusage
		syscall.Getrusage(syscall.RUSAGE_SELF, &before)
		started := time.Now()
		for _, c := range cases {
			s.Route(context.Background(), c.Origin, c.Destination)
		}
		elapsed := time.Since(started).Seconds()
		syscall.Getrusage(syscall.RUSAGE_SELF, &after)
		t.Logf("ACCESS pass=%d seconds=%.3f minor_faults=%d major_faults=%d", pass, elapsed, after.Minflt-before.Minflt, after.Majflt-before.Majflt)
		report("after_pass_" + strconv.Itoa(pass))
	}
	runtime.KeepAlive(s)
}
