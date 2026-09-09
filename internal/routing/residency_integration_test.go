//go:build integration && (darwin || linux)

package routing

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// This diagnostic deliberately reproduces the old validation alias while the
// first store remains live. OS snapshots perturb timing; use the separate HTTP
// harness for latency. It never evicts pages or changes system settings.
func TestPreparedResidency(t *testing.T) {
	dir, path := os.Getenv("OPENMAPS_RESIDENCY_OUT"), os.Getenv("OPENMAPS_PERF_DB")
	if dir == "" || path == "" || os.Getenv("OPENMAPS_PREPARED") == "" {
		t.Skip("set new residency output directory and prepared performance paths")
	}
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	report := func(stage string) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		var u syscall.Rusage
		syscall.Getrusage(syscall.RUSAGE_SELF, &u)
		t.Logf("RESIDENCY stage=%s elapsed_s=%.3f heap=%d allocated=%d go_sys=%d minor_faults=%d major_faults=%d block_reads=%d", stage, time.Since(start).Seconds(), m.HeapAlloc, m.TotalAlloc, m.Sys, u.Minflt, u.Majflt, u.Inblock)
		commands := [][]string{{"ps", "-o", "rss=,vsz=", "-p", strconv.Itoa(os.Getpid())}}
		if runtime.GOOS == "darwin" {
			commands = append(commands, []string{"vmmap", "-wide", strconv.Itoa(os.Getpid())}, []string{"footprint", "-w", "-f", "bytes", "-p", strconv.Itoa(os.Getpid())}, []string{"vm_stat"}, []string{"sysctl", "vm.swapusage"})
		} else {
			commands = append(commands, []string{"cat", "/proc/" + strconv.Itoa(os.Getpid()) + "/smaps"}, []string{"cat", "/proc/vmstat"})
		}
		for _, c := range commands {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			raw, err := exec.CommandContext(ctx, c[0], c[1:]...).CombinedOutput()
			cancel()
			if err != nil {
				t.Logf("facility %s unavailable: %v", c[0], err)
			}
			label := c[0]
			if c[0] == "cat" {
				label = filepath.Base(c[1])
			}
			if err := os.WriteFile(filepath.Join(dir, stage+"-"+label+".txt"), raw, 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	report("before")
	loadStart := time.Now()
	s, err := OpenPrepared(context.Background(), path, os.Getenv("OPENMAPS_PREPARED"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("LOAD seconds=%.3f mapped_bytes=%d", time.Since(loadStart).Seconds(), s.MappedBytes())
	defer s.Close()
	report("loaded")
	if os.Getenv("OPENMAPS_RESIDENCY_VERIFY") == "1" {
		sum, err := hashFile(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		verifyStart := time.Now()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		if has, err := VerifyPreparedPublication(context.Background(), path, sum, os.Getenv("OPENMAPS_PREPARED")); err != nil || !has {
			t.Fatalf("publication: %t %v", has, err)
		}
		runtime.ReadMemStats(&after)
		t.Logf("VERIFY seconds=%.3f allocated_bytes=%d heap_before=%d heap_after=%d mapped_bytes=%d", time.Since(verifyStart).Seconds(), after.TotalAlloc-before.TotalAlloc, before.HeapAlloc, after.HeapAlloc, s.MappedBytes())
		report("verified")
	} else {
		alias, err := OpenPrepared(context.Background(), path, os.Getenv("OPENMAPS_PREPARED"))
		if err != nil {
			t.Fatal(err)
		}
		report("alias")
		alias.Close()
		report("alias_retired")
	}
	raw, err := os.ReadFile(os.Getenv("OPENMAPS_PERF_CASES"))
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
	for pass := 1; pass <= 2; pass++ {
		for i, c := range cases {
			_, err := s.Route(context.Background(), c.Origin, c.Destination)
			t.Logf("QUERY pass=%d name=%q error=%v", pass, c.Name, err)
			if pass == 1 && i == 0 {
				report("first_request")
			}
		}
		report("pass_" + strconv.Itoa(pass))
	}
	s.Close()
	report("retired")
}
