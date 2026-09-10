package supervisor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChild(t *testing.T) {
	if os.Getenv("OPENMAPS_SUPERVISOR_TEST_CHILD") != "1" {
		return
	}
	if os.Getenv("OPENMAPS_SUPERVISOR_TEST_SLEEP") == "1" {
		time.Sleep(30 * time.Second)
	}
	os.Exit(0)
}
func options(t *testing.T) Options {
	t.Helper()
	t.Setenv("OPENMAPS_SUPERVISOR_TEST_CHILD", "1")
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	return Options{Root: t.TempDir(), Report: filepath.Join(t.TempDir(), "resource.json"), RSSMiB: 1, ReserveGiB: 32, Command: []string{exe, "-test.run=^TestChild$"}, Interval: 10 * time.Millisecond, Free: func(string) (int64, error) { return 100 << 30, nil }, RSS: func(context.Context, int) (int64, error) { return 0, nil }}
}
func TestReportAndNoOverwrite(t *testing.T) {
	o := options(t)
	r, e := Run(context.Background(), o)
	if e != nil || r.ReturnCode != 0 {
		t.Fatal(r, e)
	}
	raw, e := os.ReadFile(o.Report)
	if e != nil || !json.Valid(raw) {
		t.Fatal(e)
	}
	if _, e = Run(context.Background(), o); e == nil {
		t.Fatal("overwrote report")
	}
}
func TestBudgetTerminationAndCancellation(t *testing.T) {
	for _, kind := range []string{"rss", "disk", "cancel"} {
		t.Run(kind, func(t *testing.T) {
			o := options(t)
			t.Setenv("OPENMAPS_SUPERVISOR_TEST_SLEEP", "1")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "rss":
				o.RSS = func(context.Context, int) (int64, error) { return 2 << 20, nil }
			case "disk":
				n := 0
				o.Free = func(string) (int64, error) {
					n++
					if n > 1 {
						return 1, nil
					}
					return 100 << 30, nil
				}
			case "cancel":
				time.AfterFunc(30*time.Millisecond, cancel)
			}
			start := time.Now()
			r, e := Run(ctx, o)
			if e == nil || r.Abort == "" || time.Since(start) > 6*time.Second {
				t.Fatal(r, e)
			}
			b, _ := os.ReadFile(o.Report)
			if !strings.Contains(string(b), "abort_reason") {
				t.Fatal("lost abort report")
			}
		})
	}
}
func TestPreflightDiskRejection(t *testing.T) {
	o := options(t)
	o.Command = []string{"must-not-execute"}
	o.Free = func(string) (int64, error) { return 1, nil }
	r, e := Run(context.Background(), o)
	if e == nil || r.Abort != "disk reserve already crossed" {
		t.Fatal(r, e)
	}
}
