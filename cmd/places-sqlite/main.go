// places-sqlite is the opt-in SQLite FTS5/RTree autocomplete bakeoff command.
// It never reads or writes the production lookup selection.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"openmaps/internal/api"
	"openmaps/internal/places"
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
	placesqlite "openmaps/internal/placesgeocoding/sqlite"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: places-sqlite build|serve|inspect [flags]")
	}
	switch os.Args[1] {
	case "build":
		return build(os.Args[2:])
	case "serve":
		return serve(os.Args[2:])
	case "inspect":
		return inspect(os.Args[2:])
	default:
		return fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
}

func build(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	lookup := fs.String("lookup", "", "verified normalized Parquet/DuckDB generation")
	out := fs.String("out", "", "new immutable SQLite generation directory")
	report := fs.String("report", "", "write JSON build report")
	shards := fs.Int("shards", 4, "fixed SQLite shard count, 1..16")
	batch := fs.Int("batch", 10000, "entities per explicit transaction")
	reserve := fs.Int64("reserve-gib", 60, "minimum free disk reserve")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *lookup == "" || *out == "" || *report == "" {
		return fmt.Errorf("build requires -lookup, -out and -report")
	}
	stats, err := placesqlite.Build(context.Background(), placesqlite.BuildOptions{Generation: *lookup, Output: *out, Shards: *shards, BatchSize: *batch, ReserveBytes: *reserve << 30, Observe: func(s placesqlite.BuildStats) {
		if s.Batches%1000 == 0 {
			log.Printf("build attempted=%d accepted=%d rejected=%d batches=%d min_free=%d", s.Attempted, s.Accepted, s.Rejected, s.Batches, s.MinimumFreeBytes)
		}
	}})
	if writeErr := writeJSON(*report, stats); err == nil && writeErr != nil {
		err = writeErr
	}
	encoded, _ := json.Marshal(stats)
	log.Printf("build result %s", encoded)
	return err
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	lookup := fs.String("lookup", "", "verified normalized Parquet/DuckDB generation used for Details")
	catalog := fs.String("sqlite", "", "finalized SQLite experiment generation")
	listen := fs.String("listen", "127.0.0.1:18091", "experimental HTTP listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *lookup == "" || *catalog == "" {
		return fmt.Errorf("serve requires -lookup and -sqlite")
	}
	details, reference, err := placeduckdb.OpenWithReference(*lookup, func(phase string, elapsed time.Duration) {
		log.Printf("Details startup: phase=%s duration=%s", phase, elapsed)
	})
	if err != nil {
		return err
	}
	defer details.Close()
	store, err := placesqlite.Open(*catalog, details)
	if err != nil {
		return err
	}
	defer store.Close()
	mux := http.NewServeMux()
	mux.Handle("/v1/", api.Handler{Places: store})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "experiment": "sqlite", "sqlite_generation": *catalog, "lookup_snapshot": reference})
	})
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	log.Printf("Open Maps SQLite experiment: http://%s", *listen)
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		return server.Shutdown(shutdown)
	}
}

func inspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	catalog := fs.String("sqlite", "", "finalized SQLite experiment generation")
	report := fs.String("report", "", "write JSON query-plan report")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *catalog == "" || *report == "" {
		return fmt.Errorf("inspect requires -sqlite and -report")
	}
	store, err := placesqlite.Open(*catalog, unavailableDetails{})
	if err != nil {
		return err
	}
	defer store.Close()
	plans, err := store.QueryPlans(context.Background())
	if err != nil {
		return err
	}
	inventory, err := store.Inventory(context.Background())
	if err != nil {
		return err
	}
	return writeJSON(*report, map[string]any{"query_plans": plans, "shards": inventory})
}

type unavailableDetails struct{}

func (unavailableDetails) Details(context.Context, string) (places.Entity, error) {
	return places.Entity{}, fmt.Errorf("Details are unavailable during plan inspection")
}

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".sqlite-report-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	defer os.Remove(name)
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(value); err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, path)
}
