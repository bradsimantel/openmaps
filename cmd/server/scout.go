package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"openmaps/internal/api"
	"openmaps/internal/routing/valhallatiles"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func serveScout(dir, selection, listen, public string, workers int, cache int64) error {
	started := time.Now()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	candidate, err := valhallatiles.OpenCandidate(ctx, dir, workers, cache)
	if err != nil {
		return err
	}
	watchDone := make(chan struct{})
	defer func() { cancel(); <-watchDone; candidate.Close() }()
	mux := http.NewServeMux()
	handler := api.Handler{Scout: candidate}
	mux.Handle("/directions/", api.RoutingAdmission(handler, workers))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		meta, reloadError := candidate.Status()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "routing_available": true, "routing_duration_available": true, "routing_candidate": meta, "reload_error": reloadError, "exhaustive_source_coverage_verified": false})
	})
	mux.Handle("/", http.FileServer(http.Dir(public)))
	server := &http.Server{Addr: listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	if selection != "" {
		go func() { defer close(watchDone); watchScoutSelection(ctx, candidate, selection) }()
	} else {
		close(watchDone)
	}
	log.Printf("Open Maps experimental Scout candidate: http://%s (loaded in %s)", listen, time.Since(started))
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		err := server.Shutdown(shutdown)
		if err != nil {
			server.Close()
		}
		return err
	}
}
func watchScoutSelection(ctx context.Context, c *valhallatiles.Candidate, path string) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var last string
	var lastError string
	failure := func(err error) {
		last = ""
		c.RecordSelectionError(err)
		if err.Error() != lastError {
			log.Printf("Scout selection: %v", err)
			lastError = err.Error()
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f, err := os.Open(path)
			if err != nil {
				failure(err)
				continue
			}
			b, err := io.ReadAll(io.LimitReader(f, 65537))
			f.Close()
			if err != nil || len(b) > 65536 {
				failure(errors.New("Scout selection unreadable or oversized"))
				continue
			}
			if string(b) == last {
				continue
			}
			last = string(b)
			var selection struct {
				Directory string `json:"directory"`
			}
			if err := json.Unmarshal(b, &selection); err != nil || selection.Directory == "" {
				failure(errors.New("Scout selection requires directory"))
				continue
			}
			dir := selection.Directory
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(filepath.Dir(path), dir)
			}
			dir, err = filepath.Abs(dir)
			if err != nil {
				failure(err)
				continue
			}
			current, _ := c.Status()
			if dir == current.Directory {
				c.RecordSelectionError(nil)
				lastError = ""
				continue
			}
			started := time.Now()
			if err := c.Replace(ctx, dir); err != nil {
				log.Printf("Scout replacement rejected; old snapshot retained: %v", err)
			} else {
				lastError = ""
				log.Printf("Scout candidate replaced: %s (load and retirement %s)", dir, time.Since(started))
			}
		}
	}
}
