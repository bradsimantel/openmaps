// places-opensearch is the opt-in OpenSearch autocomplete bakeoff command. It
// never reads or writes the production lookup selection.
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
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
	placeopensearch "openmaps/internal/placesgeocoding/opensearch"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: places-opensearch export|serve|benchmark [flags]")
	}
	switch os.Args[1] {
	case "export":
		return export(os.Args[2:])
	case "serve":
		return serve(os.Args[2:])
	case "benchmark":
		return benchmark(os.Args[2:])
	default:
		return fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
}

func common(fs *flag.FlagSet) (*string, *string) {
	endpoint := fs.String("opensearch", "http://127.0.0.1:19200", "OpenSearch endpoint")
	index := fs.String("index", placeopensearch.DefaultIndex, "experimental index name")
	return endpoint, index
}

func export(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	endpoint, index := common(fs)
	lookup := fs.String("lookup", "", "verified normalized Parquet/DuckDB generation")
	report := fs.String("report", "", "write final JSON report")
	batchDocs := fs.Int("bulk-docs", 1000, "maximum documents per bulk request")
	batchBytes := fs.Int("bulk-bytes", 8<<20, "approximate maximum bulk request bytes")
	workers := fs.Int("workers", 4, "bounded concurrent Parquet/Bulk workers, 1..16")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *lookup == "" || *report == "" {
		return fmt.Errorf("export requires -lookup and -report")
	}
	client, err := placeopensearch.NewClient(*endpoint, *index)
	if err != nil {
		return err
	}
	if err = client.Ready(context.Background()); err != nil {
		return err
	}
	stats, err := client.Export(context.Background(), placeopensearch.ExportOptions{
		Generation: *lookup, BatchDocs: *batchDocs, BatchBytes: *batchBytes, Workers: *workers,
		Observe: func(stats placeopensearch.ExportStats) {
			if stats.Batches%1000 == 0 {
				log.Printf("export read=%d accepted=%d rejected=%d retried=%d batches=%d", stats.Read, stats.Accepted, stats.Rejected, stats.Retried, stats.Batches)
			}
		},
	})
	stats.Elapsed = stats.Elapsed.Round(time.Millisecond)
	if writeErr := writeJSON(*report, stats); writeErr != nil && err == nil {
		err = writeErr
	}
	encoded, _ := json.Marshal(stats)
	log.Printf("export result %s", encoded)
	return err
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	endpoint, index := common(fs)
	lookup := fs.String("lookup", "", "verified normalized Parquet/DuckDB generation used only for Details")
	listen := fs.String("listen", "127.0.0.1:18090", "experimental HTTP listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 || *lookup == "" {
		return fmt.Errorf("serve requires -lookup")
	}
	client, err := placeopensearch.NewClient(*endpoint, *index)
	if err != nil {
		return err
	}
	if err = client.Ready(context.Background()); err != nil {
		return err
	}
	details, reference, err := placeduckdb.OpenWithReference(*lookup, func(phase string, elapsed time.Duration) {
		log.Printf("Details startup: phase=%s duration=%s", phase, elapsed)
	})
	if err != nil {
		return err
	}
	defer details.Close()
	store, err := placeopensearch.NewStore(client, details)
	if err != nil {
		return err
	}
	mux := http.NewServeMux()
	handler := api.Handler{Places: store}
	mux.Handle("/v1/", handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		check, stop := context.WithTimeout(r.Context(), time.Second)
		defer stop()
		if readyErr := client.Ready(check); readyErr != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			json.NewEncoder(w).Encode(map[string]any{"status": "unavailable", "experiment": "opensearch", "index": *index, "lookup_snapshot": reference})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"status": "ok", "experiment": "opensearch", "index": *index, "lookup_snapshot": reference})
	})
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 3 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	log.Printf("Open Maps OpenSearch experiment: http://%s", *listen)
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

func writeJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".opensearch-report-*")
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
