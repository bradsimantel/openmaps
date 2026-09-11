package duckdb

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"openmaps/internal/api"
	"openmaps/internal/importer"
)

type Reference struct {
	Path   string `json:"path"`
	SHA256 string `json:"manifest_sha256"`
}

type Selection struct {
	Schema   int        `json:"schema"`
	Current  Reference  `json:"current"`
	Previous *Reference `json:"previous,omitempty"`
}

func ReadSelection(path string) (Selection, error) {
	var selection Selection
	raw, err := os.ReadFile(path)
	if err != nil {
		return selection, err
	}
	if err = json.Unmarshal(raw, &selection); err != nil {
		return selection, err
	}
	if selection.Schema != 1 || selection.Current.Path == "" || len(selection.Current.SHA256) != 64 {
		return selection, fmt.Errorf("invalid DuckDB selection")
	}
	return selection, nil
}

func Describe(path string) (Reference, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Reference{}, err
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return Reference{}, err
	}
	if _, err = Verify(abs); err != nil {
		return Reference{}, err
	}
	digest, err := importer.Checksum(filepath.Join(abs, ManifestName))
	return Reference{Path: abs, SHA256: digest}, err
}

func verifyReference(reference Reference) (Manifest, error) {
	if !filepath.IsAbs(reference.Path) || len(reference.SHA256) != 64 {
		return Manifest{}, fmt.Errorf("invalid DuckDB reference")
	}
	if err := importer.Verify(filepath.Join(reference.Path, ManifestName), reference.SHA256); err != nil {
		return Manifest{}, err
	}
	return Verify(reference.Path)
}

func changeSelection(path string, change func(*Selection) error) error {
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("DuckDB selection locked: %w", err)
	}
	defer os.Remove(lock.Name())
	defer lock.Close()
	selection, err := ReadSelection(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err = change(&selection); err != nil {
		return err
	}
	if err = importer.WriteJSON(path, selection); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("selection published but directory could not be synced: %w", err)
	}
	defer dir.Close()
	return dir.Sync()
}

func InitializeSelection(path, generation string) error {
	return changeSelection(path, func(selection *Selection) error {
		if selection.Schema != 0 {
			return fmt.Errorf("DuckDB selection already exists")
		}
		reference, err := Describe(generation)
		if err != nil {
			return err
		}
		*selection = Selection{Schema: 1, Current: reference}
		return nil
	})
}

func ActivateSelection(path, generation string) error {
	return changeSelection(path, func(selection *Selection) error {
		if selection.Schema != 1 {
			return fmt.Errorf("DuckDB selection is not initialized")
		}
		reference, err := Describe(generation)
		if err != nil {
			return err
		}
		if reference == selection.Current {
			return fmt.Errorf("DuckDB generation is already active")
		}
		previous := selection.Current
		selection.Previous = &previous
		selection.Current = reference
		return nil
	})
}

func RollbackSelection(path string) error {
	return changeSelection(path, func(selection *Selection) error {
		if selection.Schema != 1 || selection.Previous == nil {
			return fmt.Errorf("no previous DuckDB generation")
		}
		if _, err := verifyReference(*selection.Previous); err != nil {
			return err
		}
		previous := selection.Current
		selection.Current = *selection.Previous
		selection.Previous = &previous
		return nil
	})
}

// LiveHandler is the non-default DuckDB snapshot lease proof. It verifies and
// opens a replacement before swapping, and closes the old generation only
// after in-flight readers release the shared lease.
type LiveHandler struct {
	mu        sync.RWMutex
	reloadMu  sync.Mutex
	closed    bool
	statePath string
	current   Reference
	store     *Store
}

func OpenLiveHandler(statePath string) (*LiveHandler, error) {
	handler := &LiveHandler{statePath: statePath}
	if err := handler.reload(); err != nil {
		return nil, err
	}
	return handler, nil
}

func (handler *LiveHandler) reload() error {
	selection, err := ReadSelection(handler.statePath)
	if err != nil {
		return err
	}
	handler.mu.RLock()
	same, closed := selection.Current == handler.current, handler.closed
	handler.mu.RUnlock()
	if closed {
		return fmt.Errorf("DuckDB deployment closed")
	}
	if same {
		return nil
	}
	manifest, err := verifyReference(selection.Current)
	if err != nil {
		return err
	}
	next, err := openVerified(selection.Current.Path, manifest)
	if err != nil {
		return err
	}
	handler.mu.Lock()
	old := handler.store
	handler.store = next
	handler.current = selection.Current
	if old != nil {
		_ = old.Close()
	}
	handler.mu.Unlock()
	return nil
}

func (handler *LiveHandler) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if handler.reloadMu.TryLock() {
		_ = handler.reload()
		handler.reloadMu.Unlock()
	}
	handler.mu.RLock()
	defer handler.mu.RUnlock()
	if handler.closed || handler.store == nil {
		http.Error(w, "Places data unavailable", http.StatusServiceUnavailable)
		return
	}
	api.Handler{Places: handler.store, Geocoding: handler.store}.ServeHTTP(w, request)
}

func (handler *LiveHandler) Close() error {
	handler.reloadMu.Lock()
	defer handler.reloadMu.Unlock()
	handler.mu.Lock()
	defer handler.mu.Unlock()
	if handler.closed {
		return nil
	}
	handler.closed = true
	if handler.store != nil {
		return handler.store.Close()
	}
	return nil
}

func (handler *LiveHandler) Current() Reference {
	handler.mu.RLock()
	defer handler.mu.RUnlock()
	return handler.current
}
