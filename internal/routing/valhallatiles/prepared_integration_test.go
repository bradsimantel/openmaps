//go:build integration

package valhallatiles

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"
)

func TestPreparedScoutRegional(t *testing.T) {
	dir := os.Getenv("OPENMAPS_SCOUT_PREPARED")
	if dir == "" {
		t.Skip("set OPENMAPS_SCOUT_PREPARED; no downloads")
	}
	s, err := OpenPreparedRouter(context.Background(), dir, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Reader.Close()
	if err := s.EnablePotential(dir); err != nil {
		t.Fatal(err)
	}
	if landmarks := os.Getenv("OPENMAPS_SCOUT_LANDMARKS"); landmarks != "" {
		if err := s.EnableLandmarks(context.Background(), dir, landmarks); err != nil {
			t.Fatal(err)
		}
	}
	reference, err := buildRouter(context.Background(), s.Reader, maxTurnRules, maxTurnPrefixes)
	if err != nil {
		t.Fatal(err)
	}
	if reference.RestrictionCount != s.RestrictionCount || reference.TimedRestrictionCount != s.TimedRestrictionCount {
		t.Fatal("prepared turn counts differ")
	}
	for _, tc := range []struct {
		name     string
		from, to Point
	}{
		{"Newport", Point{-71.31373108, 41.49138952}, Point{-71.30830418, 41.48654393}},
		{"Boston to Cambridge", Point{-71.0601, 42.3551}, Point{-71.1190, 42.3736}},
		{"Newport to Boston", Point{-71.31373108, 41.49138952}, Point{-71.0601, 42.3551}},
		{"Providence to Worcester held out", Point{-71.4128, 41.8240}, Point{-71.8023, 42.2626}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			got, err := s.RouteAccelerated(ctx, tc.from, tc.to, 2000000)
			if err != nil {
				t.Fatal(err)
			}
			verifyRoute(t, s, got)
			want, err := s.RouteSnaps(ctx, got.Origin, got.Destination, 2000000)
			if err != nil {
				t.Fatal(err)
			}
			if math.Abs(got.Seconds-want.Seconds) > math.Max(1e-6, want.Seconds*1e-10) {
				t.Fatal("prepared restrictions differ from ordinary reference")
			}
			summary := struct {
				Name            string
				Meters, Seconds float64
				Steps           int
				Metrics         Metrics
			}{tc.name, got.Meters, got.Seconds, len(got.Steps), got.Metrics}
			b, _ := json.Marshal(summary)
			t.Log(string(b))
		})
	}
	// Validate every encoded prohibited path against both representations,
	// including timed paths and references outside the acquired area.
	for _, id := range s.Reader.TileIDs() {
		rs, err := s.Reader.Restrictions(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, rule := range rs {
			a, b := 0, 0
			for _, edge := range rule.Path {
				next, banned, err := s.advanceChecked(a, edge)
				if err != nil {
					t.Fatal(err)
				}
				want, wb := reference.advance(b, edge)
				if next != want || banned != wb {
					t.Fatal("disk automaton mismatch")
				}
				a, b = next, want
			}
			_, banned, err := s.advanceChecked(a, invalid)
			if err != nil {
				t.Fatal(err)
			}
			if banned {
				t.Fatal("invalid edge failed to reset automaton")
			}
		}
	}
}
