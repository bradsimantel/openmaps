package routing

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func constructionContext(t *testing.T, dir string, batch int) context.Context {
	t.Helper()
	ctx, err := WithConstructionScratch(context.Background(), dir, batch)
	if err != nil {
		t.Fatal(err)
	}
	return ctx
}
func assertNoIdentityScratch(t *testing.T, dir string) {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, ".routing-ids-*"))
	if err != nil || len(paths) != 0 {
		t.Fatalf("scratch leaked: %v %v", paths, err)
	}
}

func TestConstructionIDsBatchesAndOrdering(t *testing.T) {
	segments := make([]Segment, 259)
	for i := range segments {
		segments[i].ID = fmt.Sprintf("%d:%d", math.MaxInt64-int64(i)*101, i)
	}
	guards := []Guard{{Segment: "0:guard"}, {Segment: "z:guard"}}
	for _, batch := range []int{1, 2, 16, 17, 64, 260, 261, 262, DefaultConstructionSortRecords} {
		t.Run(fmt.Sprint(batch), func(t *testing.T) {
			dir := t.TempDir()
			x, err := newConstructionIDs(constructionContext(t, dir, batch), segments, guards)
			if err != nil {
				t.Fatal(err)
			}
			defer x.close()
			for i, s := range segments {
				got, ok, err := x.segment(s.ID)
				if err != nil || !ok || got != i {
					t.Fatalf("source identity moved: %d %d %v %v", i, got, ok, err)
				}
			}
			for _, id := range []string{"missing", "0:guard", "z:guard"} {
				if _, ok, err := x.segment(id); err != nil || ok {
					t.Fatalf("non-segment accepted: %s %v", id, err)
				}
			}
			if err := x.close(); err != nil {
				t.Fatal(err)
			}
			assertNoIdentityScratch(t, dir)
		})
	}
}

func TestConstructionIDsRejectDuplicatesAndEmpty(t *testing.T) {
	for _, batch := range []int{1, 2, 17} {
		for _, mode := range []string{"segments", "guards", "cross", "empty-segment", "empty-guard"} {
			t.Run(fmt.Sprintf("%d/%s", batch, mode), func(t *testing.T) {
				s := []Segment{{ID: "b"}, {ID: "a"}, {ID: "c"}}
				g := []Guard{{Segment: "e"}, {Segment: "d"}}
				switch mode {
				case "segments":
					s[2].ID = s[0].ID
				case "guards":
					g[1].Segment = g[0].Segment
				case "cross":
					g[1].Segment = s[0].ID
				case "empty-segment":
					s[0].ID = ""
				case "empty-guard":
					g[0].Segment = ""
				}
				dir := t.TempDir()
				if x, err := newConstructionIDs(constructionContext(t, dir, batch), s, g); err == nil {
					x.close()
					t.Fatal("invalid identity accepted")
				}
				assertNoIdentityScratch(t, dir)
			})
		}
	}
}

type cancelConstructionAfter struct {
	context.Context
	calls, limit int
}

func (c *cancelConstructionAfter) Err() error {
	c.calls++
	if c.calls >= c.limit {
		return context.Canceled
	}
	return nil
}

func TestConstructionIDsCancellationAndFailureCleanup(t *testing.T) {
	s := make([]Segment, 513)
	for i := range s {
		s[i].ID = fmt.Sprintf("%04d", i)
	}
	// Cancels before creation, during run writing, and during multiple merge
	// generations. A final uncancelled run also covers successful cleanup.
	for _, limit := range []int{1, 2, 3, 10, 100, 200, 202, 10000} {
		dir := t.TempDir()
		ctx := &cancelConstructionAfter{Context: constructionContext(t, dir, 8), limit: limit}
		x, err := newConstructionIDs(ctx, s, nil)
		if err == nil {
			x.close()
		} else if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if limit <= 202 && err == nil {
			t.Fatal("cancellation did not reach construction")
		}
		assertNoIdentityScratch(t, dir)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(file, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := newConstructionIDs(constructionContext(t, file, 1), s, nil); err == nil {
		t.Fatal("invalid scratch destination accepted")
	}
	got, _ := os.ReadFile(file)
	if string(got) != "retained" {
		t.Fatal("existing path changed")
	}
	for _, n := range []int{0, -1, MaxConstructionSortRecords + 1, math.MaxInt} {
		if _, err := WithConstructionScratch(context.Background(), dir, n); err == nil {
			t.Fatal("invalid/overflowing batch accepted")
		}
	}
}

func TestConstructionIDsMalformedIntermediate(t *testing.T) {
	for _, mode := range []string{"truncated", "empty", "ordinal-overflow", "unordered", "duplicate", "missing"} {
		t.Run(mode, func(t *testing.T) {
			x := &constructionIDs{segments: []Segment{{ID: "a"}, {ID: "b"}, {ID: "c"}}, count: 3, dir: t.TempDir()}
			defer x.close()
			if err := writeIdentityRun(context.Background(), x.run(0, 0), []uint64{0, 1, 2}); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(x.run(0, 0))
			switch mode {
			case "truncated":
				raw = raw[:len(raw)-1]
			case "empty":
				raw = nil
			case "ordinal-overflow":
				binary.LittleEndian.PutUint64(raw, math.MaxUint64)
			case "unordered":
				binary.LittleEndian.PutUint64(raw, 2)
			case "duplicate":
				binary.LittleEndian.PutUint64(raw[8:], 0)
			case "missing":
				os.Remove(x.run(0, 0))
			}
			if mode != "missing" {
				if err := os.WriteFile(x.run(0, 0), raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := x.merge(context.Background(), 0, 0, 1, x.run(1, 0)); err == nil {
				t.Fatal("malformed intermediate accepted")
			}
		})
	}
}

func TestConstructionBatchesPreservePreparedBytes(t *testing.T) {
	for version := 1; version <= GraphVersion; version++ {
		d := fixture()
		d.Metadata.Version = version
		if version < GraphVersion {
			d.Metadata.Profile = fmt.Sprintf("driving-distance-v%d", version)
		}
		d = uniformCosts(d)
		slices.Reverse(d.Segments)
		d.Bans = []Ban{{Relation: math.MaxInt64, Path: []EdgeRef{{Segment: "a"}, {Segment: "c"}, {Segment: "d"}}}}
		d.Guards = []Guard{{Segment: "guard", Way: math.MaxInt64, From: Point{.9, .9}, To: Point{.8, .8}}}
		original := slices.Clone(d.Segments)
		var baseline []byte
		for _, batch := range []int{DefaultConstructionSortRecords, 1, 2, 3, 5, 6, 7} {
			dir := t.TempDir()
			s, err := newStoreContext(constructionContext(t, dir, batch), d, false)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(d.Segments, original) {
				t.Fatal("caller source order changed")
			}
			assertNoIdentityScratch(t, dir)
			path := filepath.Join(dir, "prepared.bin")
			if _, err = s.writePrepared(context.Background(), path); err != nil {
				t.Fatal(err)
			}
			s.Close()
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if baseline == nil {
				baseline = raw
			} else if !slices.Equal(baseline, raw) {
				t.Fatalf("graph %d batch %d changed prepared bytes", version, batch)
			}
		}
	}
}

func TestConstructionMissingReferencesCleanScratch(t *testing.T) {
	for _, mode := range []string{"node", "ban", "guard-ban", "direction"} {
		d := uniformCosts(fixture())
		d.Bans = []Ban{{Relation: 1, Path: []EdgeRef{{Segment: "a"}, {Segment: "b"}}}}
		switch mode {
		case "node":
			d.Segments[0].From = math.MaxInt64
		case "ban":
			d.Bans[0].Path[1].Segment = "absent"
		case "guard-ban":
			d.Guards = []Guard{{Segment: "guard", Way: 9, From: Point{.9, .9}, To: Point{.8, .8}}}
			d.Bans[0].Path[1].Segment = "guard"
		case "direction":
			d.Segments[0].Forward = false
		}
		dir := t.TempDir()
		if _, err := newStoreContext(constructionContext(t, dir, 1), d, false); err == nil {
			t.Fatal("missing reference accepted", mode)
		}
		assertNoIdentityScratch(t, dir)
	}
}

func TestConstructionFinalRunCountAndReadFailures(t *testing.T) {
	x := &constructionIDs{segments: []Segment{{ID: "a"}, {ID: "b"}, {ID: "c"}}, count: 3, dir: t.TempDir()}
	defer x.close()
	path := x.run(0, 0)
	if err := writeIdentityRun(context.Background(), path, []uint64{0, 1}); err != nil {
		t.Fatal(err)
	}
	if err := x.openRun(path); err == nil {
		t.Fatal("missing whole record accepted")
	}
	os.Remove(path)
	if err := writeIdentityRun(context.Background(), path, []uint64{0, 1, 2}); err != nil {
		t.Fatal(err)
	}
	if err := x.openRun(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, 9); err != nil {
		t.Fatal(err)
	}
	if _, _, err := x.segment("b"); err == nil {
		t.Fatal("truncated lookup succeeded")
	}
}

func TestConstructionPublicationStreamsAndCancels(t *testing.T) {
	d := uniformCosts(fixture())
	// A source reference crosses the writer boundary; multibyte text and zero
	// bytes must retain byte offsets, never rune counts or a normalized identity.
	d.Segments[0].ID = strings.Repeat("é\x00", (1<<20)/3+7)
	d.Guards = []Guard{{Segment: "尾guard", Way: 9, From: Point{.9, .9}, To: Point{.8, .8}}}
	s := store(t, d)
	defer s.Close()
	for _, version := range []string{preparedVersionV1, PreparedVersion} {
		path := filepath.Join(t.TempDir(), "complete.bin")
		h, err := s.writePreparedVersion(context.Background(), path, version)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		want := ""
		for _, seg := range s.segments {
			want += seg.ID
		}
		for _, g := range s.guards {
			want += g.Segment
		}
		for _, section := range h.Sections {
			if section.Name == "strings" && string(raw[section.Offset:section.Offset+section.Count]) != want {
				t.Fatal("source string bytes changed")
			}
		}
	}
	for _, phase := range []string{"segments", "guards", "node_lookup", "strings", "spatial_0_ids"} {
		dir := t.TempDir()
		path := filepath.Join(dir, "cancelled.bin")
		ctx, cancel := context.WithCancel(context.Background())
		reached := false
		ctx = WithLoadObserver(ctx, func(p LoadPhase) {
			if p.Name == "publication "+phase {
				reached = true
				cancel()
			}
		})
		_, err := s.writePrepared(ctx, path)
		cancel()
		if !reached || !errors.Is(err, context.Canceled) {
			t.Fatalf("phase %s: %v reached=%v", phase, err, reached)
		}
		files, err := os.ReadDir(dir)
		if err != nil || len(files) != 0 {
			t.Fatalf("failed publication leaked files: %v %v", files, err)
		}
	}
}
