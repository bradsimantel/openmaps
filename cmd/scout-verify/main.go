// scout-verify runs frozen coordinate cases with source-path verification.
// It is offline and never changes graph data or an active deployment.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"openmaps/internal/routing"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"
)

type routeCase struct {
	Name      string        `json:"name"`
	State     string        `json:"state,omitempty"`
	Kind      string        `json:"kind"`
	From      routing.Point `json:"from"`
	To        routing.Point `json:"to"`
	Reference bool          `json:"reference"`
	Expect    string        `json:"expect"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	dir := flag.String("prepared", "", "prepared graph")
	landmarks := flag.String("landmarks", "", "optional landmarks directory")
	casesPath := flag.String("cases", "", "frozen coordinate case JSON array")
	snaps := flag.Bool("snaps-only", false, "endpoint discovery without routing")
	timeout := flag.Duration("timeout", 30*time.Second, "per-case calculation deadline")
	repeat := flag.Int("repeat", 1, "1..10 consecutive runs per case; first application-cache cold, subsequent warm")
	labels := flag.Int("max-labels", 2000000, "bounded offline label budget, 1..4000000")
	landmarkCache := flag.Int64("landmark-cache-mib", 8, "per-vector cache MiB, 1..32")
	flag.Parse()
	if *dir == "" || *casesPath == "" || *timeout <= 0 || *repeat < 1 || *repeat > 10 || *labels < 1 || *labels > 4000000 || *landmarkCache < 1 || *landmarkCache > 32 {
		return errors.New("prepared, cases and positive timeout required")
	}
	f, err := os.Open(*casesPath)
	if err != nil {
		return err
	}
	b, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	f.Close()
	if err != nil {
		return err
	}
	if len(b) > 1<<20 {
		return errors.New("case input exceeds budget")
	}
	var cases []routeCase
	if err := json.Unmarshal(b, &cases); err != nil {
		return err
	}
	if len(cases) < 1 || len(cases) > 500 {
		return errors.New("require 1..500 cases")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	start := time.Now()
	s, err := routing.OpenPreparedRouter(ctx, *dir, 128<<20)
	if err != nil {
		return err
	}
	defer s.Reader.Close()
	if err := s.EnablePotential(*dir); err != nil {
		return err
	}
	if *landmarks != "" {
		if err := s.EnableLandmarksWithCache(ctx, *dir, *landmarks, *landmarkCache<<20); err != nil {
			return err
		}
	}
	var verifier *routing.PathVerifier
	if !*snaps {
		verifier, err = routing.NewPathVerifier(ctx, s.Reader)
		if err != nil {
			return err
		}
	}
	enc := json.NewEncoder(os.Stdout)
	enc.Encode(map[string]any{"startup_seconds": time.Since(start).Seconds(), "cases": len(cases), "prepared": *dir, "landmarks": *landmarks})
	failures := 0
	for run := 0; run < len(cases)*(*repeat); run++ {
		c := cases[run/(*repeat)]
		if err := ctx.Err(); err != nil {
			return err
		}
		query, stop := context.WithTimeout(ctx, *timeout)
		condition := "warm"
		if run%(*repeat) == 0 {
			s.Reader.ClearCache()
			condition = "application-cache-cold"
		}
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		cacheBefore := s.Reader.CacheReport()
		start = time.Now()
		a, ae := s.SnapContext(query, c.From)
		b, be := s.SnapContext(query, c.To)
		row := map[string]any{"case": c, "origin": a, "destination": b, "repeat": run % (*repeat), "cache_condition": condition, "cache_before": cacheBefore}
		var got routing.Result
		err = errors.Join(ae, be)
		if err == nil && !*snaps {
			got, err = s.RoutePreparedSnaps(query, a, b, *labels)
		}
		row["calculation_seconds"] = time.Since(start).Seconds()
		row["search_metrics"] = got.Metrics
		runtime.ReadMemStats(&after)
		row["allocated_bytes"] = after.TotalAlloc - before.TotalAlloc
		row["heap_bytes"] = after.HeapAlloc
		row["cache"] = s.Reader.CacheReport()
		if err == nil && !*snaps {
			row["route"] = got
			if err = verifier.Verify(query, got); err == nil {
				row["verified"] = true
			} else {
				row["verification_error"] = err.Error()
			}
			if err == nil && c.Reference {
				want, we := s.RouteSnaps(query, a, b, 2000000)
				row["reference_metrics"] = want.Metrics
				if we != nil {
					err = fmt.Errorf("ordinary reference: %w", we)
				} else if math.Abs(got.Seconds-want.Seconds) > math.Max(1e-6, want.Seconds*1e-10) {
					err = errors.New("accelerated cost differs from ordinary reference")
				} else {
					row["reference_matches"] = true
				}
			}
		}
		outcome := "routed"
		if *snaps {
			outcome = "snapped"
		}
		if err != nil {
			outcome = "error"
			row["error"] = err.Error()
			var missing *routing.MissingTileError
			switch {
			case errors.Is(err, routing.ErrUnreachable):
				outcome = "unreachable"
			case errors.Is(err, routing.ErrUnsnappable):
				outcome = "unsnappable"
			case errors.As(err, &missing):
				outcome = "incomplete_data"
			case errors.Is(err, routing.ErrQueryBudget):
				outcome = "query_budget_exhausted"
			case errors.Is(err, context.DeadlineExceeded):
				outcome = "timeout"
			}
		}
		row["outcome"] = outcome
		expected := c.Expect
		if *snaps {
			expected = "snapped"
		} else if expected == "" {
			expected = "routed"
		}
		if outcome != expected {
			failures++
		}
		stop()
		if err := enc.Encode(row); err != nil {
			return err
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d cases did not meet frozen expected outcome", failures)
	}
	return nil
}
