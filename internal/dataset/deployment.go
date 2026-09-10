// Package dataset owns local snapshot selection and safe HTTP handler replacement.
// Source interpretation and refresh comparisons remain in importer.
package dataset

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"openmaps/internal/api"
	"openmaps/internal/geocoding"
	"openmaps/internal/importer"
	"openmaps/internal/places"
)

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}
type State struct {
	Schema   int   `json:"schema"`
	Baseline File  `json:"baseline"`
	Current  File  `json:"current"`
	Previous *File `json:"previous,omitempty"`
}
type Review struct {
	ReportSHA256 string `json:"report_sha256"`
	Reviewer     string `json:"reviewer"`
	Reason       string `json:"reason"`
}

func Read(path string) (State, error) {
	var s State
	b, e := os.ReadFile(path)
	if e != nil {
		return s, e
	}
	e = json.Unmarshal(b, &s)
	if e != nil {
		return s, e
	}
	if s.Schema != 1 || s.Baseline.Path == "" || s.Current.Path == "" {
		return s, fmt.Errorf("invalid deployment state")
	}
	return s, nil
}
func Describe(path string) (File, error) {
	p, e := filepath.Abs(path)
	if e != nil {
		return File{}, e
	}
	p, e = filepath.EvalSymlinks(p)
	if e != nil {
		return File{}, e
	}
	sum, e := importer.Checksum(p)
	return File{p, sum}, e
}
func Validate(ctx context.Context, f File) error {
	if !filepath.IsAbs(f.Path) || len(f.SHA256) != 64 {
		return fmt.Errorf("invalid snapshot reference")
	}
	if e := importer.Verify(f.Path, f.SHA256); e != nil {
		return e
	}
	_, e := importer.ReadSnapshot(ctx, f.Path)
	return e
}

// Change serializes local writers and publishes a single synced state document.
// A process crash can leave the lock; remove it only after confirming no writer
// is running. Neither activation nor rollback modifies any database bytes.
func Change(path string, change func(*State) error) error {
	lock, e := os.OpenFile(path+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return fmt.Errorf("deployment locked: %w", e)
	}
	defer os.Remove(lock.Name())
	defer lock.Close()
	s, e := Read(path)
	if e != nil && !os.IsNotExist(e) {
		return e
	}
	if e = change(&s); e != nil {
		return e
	}
	if e = importer.WriteJSON(path, s); e != nil {
		return e
	}
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return fmt.Errorf("state published but directory could not be synced; check status: %w", e)
	}
	defer dir.Close()
	if e = dir.Sync(); e != nil {
		return fmt.Errorf("state published but directory sync failed; check status: %w", e)
	}
	return nil
}
func Init(ctx context.Context, path, baseline string) error {
	return Change(path, func(s *State) error {
		if s.Schema != 0 {
			return fmt.Errorf("deployment already exists")
		}
		f, e := Describe(baseline)
		if e != nil {
			return e
		}
		if e = Validate(ctx, f); e != nil {
			return e
		}
		*s = State{Schema: 1, Baseline: f, Current: f}
		return nil
	})
}
func Activate(ctx context.Context, path, candidate, reportPath, reviewPath string) error {
	var report importer.Report
	raw, e := os.ReadFile(reportPath)
	if e != nil {
		return e
	}
	if e = json.Unmarshal(raw, &report); e != nil {
		return e
	}
	var review Review
	raw, e = os.ReadFile(reviewPath)
	if e != nil {
		return e
	}
	if e = json.Unmarshal(raw, &review); e != nil {
		return e
	}
	if review.Reviewer == "" || review.Reason == "" {
		return fmt.Errorf("reviewer and reason required")
	}
	if e = importer.Verify(reportPath, review.ReportSHA256); e != nil {
		return e
	}
	if report.Schema != 1 || len(report.Violations) != 0 || len(report.Queries) == 0 {
		return fmt.Errorf("report has violations or no query checks")
	}
	return Change(path, func(s *State) error {
		if s.Schema != 1 || s.Current.SHA256 != report.BaselineSHA256 {
			return fmt.Errorf("report does not compare the current deployment")
		}
		f, e := Describe(candidate)
		if e != nil {
			return e
		}
		if f.SHA256 != report.CandidateSHA256 {
			return fmt.Errorf("candidate differs from reviewed database")
		}
		if f == s.Current {
			return fmt.Errorf("candidate is already active")
		}
		// Recompute validation rather than trusting editable report assertions.
		checks := []importer.QueryCheck{}
		for _, q := range report.Queries {
			checks = append(checks, q.Check)
		}
		actual, e := importer.Compare(ctx, s.Current.Path, f.Path, checks)
		if e != nil {
			return e
		}
		a, _ := json.Marshal(actual)
		b, _ := json.Marshal(report)
		if string(a) != string(b) {
			return fmt.Errorf("comparison differs from reviewed report")
		}
		previous := s.Current
		s.Previous = &previous
		s.Current = f
		return nil
	})
}
func Rollback(ctx context.Context, path string) error { return rollback(ctx, path, Validate) }

func rollback(ctx context.Context, path string, validate func(context.Context, File) error) error {
	return Change(path, func(s *State) error {
		if s.Previous == nil {
			return fmt.Errorf("no previous snapshot")
		}
		if e := validate(ctx, *s.Previous); e != nil {
			return e
		}
		previous := s.Current
		s.Current = *s.Previous
		s.Previous = &previous
		return nil
	})
}

type Live struct {
	mu        sync.RWMutex
	reloadMu  sync.Mutex
	closed    bool
	statePath string
	current   File
	store     *places.Store
	geocoder  *geocoding.Store
	lastError string
}

func Open(ctx context.Context, statePath string) (*Live, error) {
	l := &Live{statePath: statePath}
	if e := l.reload(ctx); e != nil {
		return nil, e
	}
	return l, nil
}

func (l *Live) reload(ctx context.Context) error {
	s, e := Read(l.statePath)
	if e != nil {
		return e
	}
	l.mu.RLock()
	same, closed := s.Current == l.current, l.closed
	l.mu.RUnlock()
	if closed {
		return fmt.Errorf("deployment closed")
	}
	if same {
		return nil
	}
	if !filepath.IsAbs(s.Current.Path) || len(s.Current.SHA256) != 64 {
		return fmt.Errorf("invalid snapshot reference")
	}
	if e = importer.Verify(s.Current.Path, s.Current.SHA256); e != nil {
		return e
	}
	if _, e = importer.ReadSnapshot(ctx, s.Current.Path); e != nil {
		return e
	}
	next, e := places.Open(s.Current.Path)
	if e != nil {
		return e
	}
	geocoder, e := geocoding.Open(ctx, s.Current.Path)
	if e != nil {
		next.Close()
		return e
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	old := l.store
	l.store = next
	l.geocoder = geocoder
	l.current = s.Current
	if old != nil {
		old.Close()
	}
	return nil
}
func (l *Live) Close() error {
	l.reloadMu.Lock()
	defer l.reloadMu.Unlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.store != nil {
		return l.store.Close()
	}
	return nil
}

// Requests hold a shared lease through response completion. A single request
// loads a changed snapshot outside that lease; concurrent requests keep using
// the previous snapshot. Publication waits for old leases before closing SQLite.
func (l *Live) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if l.reloadMu.TryLock() {
		err := l.reload(r.Context())
		message := ""
		if err != nil {
			message = err.Error()
		}
		l.mu.RLock()
		changed := l.lastError != message
		l.mu.RUnlock()
		if changed {
			l.mu.Lock()
			l.lastError = message
			l.mu.Unlock()
		}
		l.reloadMu.Unlock()
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.closed {
		http.Error(w, "deployment closed", http.StatusServiceUnavailable)
		return
	}
	if r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "application/json")
		status := "ok"
		if l.lastError != "" {
			status = "degraded"
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(struct {
			RoutingAvailable  bool   `json:"routing_available"`
			DurationAvailable bool   `json:"routing_duration_available"`
			Status            string `json:"status"`
			Dataset           File   `json:"dataset"`
			Error             string `json:"error,omitempty"`
		}{false, false, status, l.current, l.lastError})
		return
	}
	w.Header().Set("X-OpenMaps-Dataset", l.current.SHA256)
	api.Handler{Places: l.store, Geocoding: l.geocoder}.ServeHTTP(w, r)
}

// Status reports the lookup identity and the last failed selection check.
func (l *Live) Status(ctx context.Context) (File, string) {
	if l.reloadMu.TryLock() {
		err := l.reload(ctx)
		l.mu.Lock()
		l.lastError = ""
		if err != nil {
			l.lastError = err.Error()
		}
		l.mu.Unlock()
		l.reloadMu.Unlock()
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.current, l.lastError
}
