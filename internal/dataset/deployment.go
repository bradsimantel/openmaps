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
func Rollback(ctx context.Context, path string) error {
	return Change(path, func(s *State) error {
		if s.Previous == nil {
			return fmt.Errorf("no previous snapshot")
		}
		if e := Validate(ctx, *s.Previous); e != nil {
			return e
		}
		previous := s.Current
		s.Current = *s.Previous
		s.Previous = &previous
		return nil
	})
}

type Live struct {
	mu        sync.Mutex
	statePath string
	current   File
	store     *places.Store
	lastError string
}

func Open(ctx context.Context, statePath string) (*Live, error) {
	l := &Live{statePath: statePath}
	l.mu.Lock()
	defer l.mu.Unlock()
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
	if s.Current == l.current {
		return nil
	}
	if e = Validate(ctx, s.Current); e != nil {
		return e
	}
	next, e := places.Open(s.Current.Path)
	if e != nil {
		return e
	}
	old := l.store
	l.store = next
	l.current = s.Current
	if old != nil {
		old.Close()
	}
	return nil
}
func (l *Live) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.store != nil {
		return l.store.Close()
	}
	return nil
}

// Hold the lock through a request so an in-flight lookup never loses its DB.
// Local demo requests are serialized. Only a changed selection opens/checks a DB.
func (l *Live) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e := l.reload(r.Context()); e != nil {
		l.lastError = e.Error()
	} else {
		l.lastError = ""
	}
	if r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "application/json")
		status := "ok"
		if l.lastError != "" {
			status = "degraded"
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(struct {
			Status  string `json:"status"`
			Dataset File   `json:"dataset"`
			Error   string `json:"error,omitempty"`
		}{status, l.current, l.lastError})
		return
	}
	w.Header().Set("X-OpenMaps-Dataset", l.current.SHA256)
	api.Handler{Places: l.store}.ServeHTTP(w, r)
}
