package valhallatiles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

const CandidateProfile = "osm-scout-public-auto-v1"

var ErrBusy = errors.New("candidate routing concurrency budget exhausted")
var ErrClosed = errors.New("candidate closed")
var ErrUnreachable = errors.New("unreachable under the retained graph and supported profile")
var ErrUnsnappable = errors.New("unsnappable within 100 metres")
var ErrQueryBudget = errors.New("query label budget exhausted")

type CandidateMetadata struct {
	Search    string `json:"search"`
	Snapshot  string `json:"snapshot"`
	Profile   string `json:"profile"`
	Note      string `json:"profile_note"`
	Directory string `json:"directory"`
	Timestamp string `json:"package_timestamp"`
	Dataset   uint64 `json:"dataset_id"`
}
type candidateSnapshot struct {
	metadata  CandidateMetadata
	available chan *Router
	readers   []*Router
	leases    sync.WaitGroup
}

func (s *candidateSnapshot) close() error {
	if s == nil {
		return nil
	}
	s.leases.Wait()
	var err error
	for _, r := range s.readers {
		err = errors.Join(err, r.Reader.Close())
	}
	return err
}

// Candidate owns an immutable generation and a fixed pool of independent,
// single-owner readers. A lease covers response encoding as well as routing.
// Replace serializes old/new overlap and retires old readers after their leases.
type Candidate struct {
	mu          sync.Mutex
	replacement sync.Mutex
	snapshot    *candidateSnapshot
	closed      bool
	workers     int
	cacheBytes  int64
	reloadError string
	slots       chan struct{}
}
type CandidateLease struct {
	Router    *Router
	Metadata  CandidateMetadata
	snapshot  *candidateSnapshot
	once      sync.Once
	candidate *Candidate
}

func (l *CandidateLease) Close() {
	l.once.Do(func() { l.snapshot.available <- l.Router; l.snapshot.leases.Done(); l.candidate.slots <- struct{}{} })
}
func OpenCandidate(ctx context.Context, dir string, workers int, cacheBytes int64) (*Candidate, error) {
	if workers < 1 || workers > 4 || cacheBytes < pageSize || cacheBytes > 128<<20 {
		return nil, errors.New("candidate supports 1..4 readers, 64 KiB..128 MiB cache each")
	}
	c := &Candidate{workers: workers, cacheBytes: cacheBytes, slots: make(chan struct{}, workers)}
	for i := 0; i < workers; i++ {
		c.slots <- struct{}{}
	}
	if err := c.Replace(ctx, dir); err != nil {
		return nil, err
	}
	return c, nil
}
func (c *Candidate) Replace(ctx context.Context, dir string) error {
	c.replacement.Lock()
	defer c.replacement.Unlock()
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return ErrClosed
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	next := &candidateSnapshot{available: make(chan *Router, c.workers)}
	ok := false
	defer func() {
		if !ok {
			next.close()
		}
	}()
	for i := 0; i < c.workers; i++ {
		r, err := OpenPreparedRouter(ctx, abs, c.cacheBytes)
		if err != nil {
			c.setReloadError(err)
			return err
		}
		next.readers = append(next.readers, r)
		if err := r.EnablePotential(abs); err != nil {
			c.setReloadError(err)
			return err
		}
		if _, err := os.Stat(filepath.Join(abs, "landmarks")); err == nil {
			if err := r.EnableLandmarks(ctx, abs, filepath.Join(abs, "landmarks")); err != nil {
				c.setReloadError(err)
				return err
			}
		} else if !os.IsNotExist(err) {
			c.setReloadError(err)
			return err
		}
		landmarkHash, search := "", "astar-geometric-v1"
		if r.landmarks != nil {
			landmarkHash = r.landmarks.fingerprint
			search = "astar-directed-landmarks-v2"
		}
		fingerprint := hexSum([]byte("scout-candidate-v2\n" + r.Reader.preparedSHA + "\n" + r.routingSHA + "\n" + r.potentialSHA + "\n" + landmarkHash))
		if i == 0 {
			next.metadata = CandidateMetadata{Timestamp: r.Reader.preparedTimestamp, Dataset: r.Reader.preparedDataset, Snapshot: fingerprint, Profile: CandidateProfile, Note: ProfileNote, Directory: abs, Search: search}
		} else if fingerprint != next.metadata.Snapshot {
			err := errors.New("candidate files changed between worker loads")
			c.setReloadError(err)
			return err
		}
		next.available <- r
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	previous := c.snapshot
	c.snapshot = next
	c.reloadError = ""
	c.mu.Unlock()
	ok = true
	return previous.close()
}
func (c *Candidate) setReloadError(err error) {
	c.RecordSelectionError(err)
}

// RecordSelectionError exposes a failed selection-file read/parse while keeping
// the current immutable snapshot available. Passing nil clears the diagnostic.
func (c *Candidate) RecordSelectionError(err error) {
	c.mu.Lock()
	c.reloadError = ""
	if err != nil {
		c.reloadError = err.Error()
	}
	c.mu.Unlock()
}
func (c *Candidate) Acquire() (*CandidateLease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || c.snapshot == nil {
		return nil, ErrClosed
	}
	select {
	case <-c.slots:
	default:
		return nil, ErrBusy
	}
	s := c.snapshot
	select {
	case r := <-s.available:
		s.leases.Add(1)
		return &CandidateLease{Router: r, Metadata: s.metadata, snapshot: s, candidate: c}, nil
	default:
		c.slots <- struct{}{}
		return nil, ErrBusy
	}
}
func (c *Candidate) Status() (CandidateMetadata, string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snapshot == nil {
		return CandidateMetadata{}, c.reloadError
	}
	return c.snapshot.metadata, c.reloadError
}
func (c *Candidate) Close() error {
	c.replacement.Lock()
	defer c.replacement.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	s := c.snapshot
	c.snapshot = nil
	c.mu.Unlock()
	return s.close()
}

// RoutePreparedSnaps is used after independent endpoint selection by the API.
func (s *Router) RoutePreparedSnaps(ctx context.Context, a, b Snap, labels int) (Result, error) {
	return s.routeSnaps(ctx, a, b, labels, true)
}
