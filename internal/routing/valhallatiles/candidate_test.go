package valhallatiles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func syntheticPrepared(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "candidate")
	budget := ScoutBudgets{CompressedBytes: 1 << 20, ExpandedBytes: 1 << 20, ReserveBytes: 32 << 30}
	if err := diskReserve(root, 1<<20, budget.ReserveBytes); err != nil {
		t.Skip("declared disk reserve unavailable")
	}
	if err := PrepareScoutPackages(context.Background(), "testdata/scout", dir, syntheticScoutLock(t), budget); err != nil {
		t.Fatal(err)
	}
	if err := PrepareScoutTurns(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if err := PrepareScoutPotential(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	if err := PrepareScoutReverseTurns(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	return dir
}
func TestCandidateLeasesAndReplacement(t *testing.T) {
	dir := syntheticPrepared(t)
	c, err := OpenCandidate(context.Background(), dir, 1, pageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	old, err := c.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	if _, err := c.Acquire(); !errors.Is(err, ErrBusy) {
		t.Fatal("admission did not reject excess request")
	}
	ready := make(chan error, 1)
	go func() { ready <- c.Replace(context.Background(), dir) }()
	// Wait for publication without releasing the old response lease.
	deadline := time.Now().Add(3 * time.Second)
	for {
		c.mu.Lock()
		changed := c.snapshot != old.snapshot
		c.mu.Unlock()
		if changed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("replacement did not publish")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-ready:
		t.Fatalf("replacement retired an active lease: %v", err)
	default:
	}
	if old.Router.Reader.closed {
		t.Fatal("closed old active reader")
	}
	if _, err := c.Acquire(); !errors.Is(err, ErrBusy) {
		t.Fatal("replacement exceeded shared admission budget")
	}
	old.Close()
	old.Close()
	if err := <-ready; err != nil {
		t.Fatal(err)
	}
	if !old.Router.Reader.closed {
		t.Fatal("retired reader remained open")
	}
	current, err := c.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	current.Close()
	before, _ := c.Status()
	if err := c.Replace(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("accepted missing candidate")
	}
	after, reloadErr := c.Status()
	if after.Snapshot != before.Snapshot || reloadErr == "" {
		t.Fatal("failed load replaced selection or lost diagnostic")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Replace(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Acquire(); !errors.Is(err, ErrClosed) {
		t.Fatal("acquired closed candidate")
	}
	if _, err := os.Stat(filepath.Join(dir, "tiles.bin")); err != nil {
		t.Fatal("retirement removed immutable graph")
	}
}
func TestPotentialMatchesOrdinarySynthetic(t *testing.T) {
	dir := syntheticPrepared(t)
	s, err := OpenPreparedRouter(context.Background(), dir, pageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Reader.Close()
	if err := s.EnablePotential(dir); err != nil {
		t.Fatal(err)
	}
	if err := s.EnableBidirectional(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	ordinary, path := fixture(t, true, true)
	for _, fromFraction := range []float64{0, .25, .5, 1} {
		for _, toFraction := range []float64{0, .5, .9, 1} {
			a, _ := ordinary.Reader.Edge(path[0])
			b, _ := ordinary.Reader.Edge(path[2])
			sa, _ := ordinary.Reader.Shape(a)
			sb, _ := ordinary.Reader.Shape(b)
			from, to := clip(sa.Points, fromFraction, fromFraction)[0], clip(sb.Points, toFraction, toFraction)[0]
			got, err := s.RouteBidirectional(context.Background(), from, to, 1000)
			if err != nil {
				t.Fatal(err)
			}
			want, err := s.RouteSnaps(context.Background(), got.Origin, got.Destination, 1000)
			if err != nil {
				t.Fatal(err)
			}
			if got.Seconds != want.Seconds {
				t.Fatalf("partial route changed optimum: %v %v", got.Seconds, want.Seconds)
			}
		}
	}
}

func TestCandidateLandmarkOwnershipAndFailedLoad(t *testing.T) {
	ctx := context.Background()
	dir := syntheticPrepared(t)
	if err := PrepareLandmarks(ctx, dir, filepath.Join(dir, "landmarks"), []LandmarkSeed{{Name: "synthetic", Point: Point{8.749, 53.08}}}); err != nil {
		t.Fatal(err)
	}
	c, err := OpenCandidate(ctx, dir, 1, pageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	lease, err := c.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	if lease.Metadata.Search != "astar-directed-landmarks-v2" || len(lease.Router.Reader.landmarkReaders) != 2 {
		t.Fatal("candidate omitted landmark ownership/metadata")
	}
	readers := append([]*Reader(nil), lease.Router.Reader.landmarkReaders...)
	bad := syntheticPrepared(t)
	if err := os.Mkdir(filepath.Join(bad, "landmarks"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := c.Replace(ctx, bad); err == nil {
		t.Fatal("incomplete landmark publication accepted")
	}
	meta, reload := c.Status()
	if meta.Snapshot != lease.Metadata.Snapshot || reload == "" {
		t.Fatal("failed load changed active landmark snapshot")
	}
	for _, r := range readers {
		if r.closed {
			t.Fatal("active landmark lease closed")
		}
	}
	lease.Close()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	for _, r := range readers {
		if !r.closed {
			t.Fatal("retired landmark file remained open")
		}
	}
}
