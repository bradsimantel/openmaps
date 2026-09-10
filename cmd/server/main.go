package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"openmaps/internal/api"
	"openmaps/internal/dataset"
	"openmaps/internal/geocoding"
	"openmaps/internal/places"
	"openmaps/internal/routing"
)

type configuration struct {
	routing, selection, db, deployment, listen, public, tiles string
	workers                                                   int
	cache                                                     int64
}

func main() {
	var c configuration
	flag.StringVar(&c.routing, "routing-scout", "", "prepared Scout routing snapshot")
	flag.StringVar(&c.selection, "scout-selection", "", "JSON routing selection file, independent of lookup selection")
	flag.Int64Var(&c.cache, "scout-cache-mib", 128, "graph page payload per reader, 1..128 MiB; index caches are additional")
	flag.StringVar(&c.db, "db", "data/openmaps.sqlite", "lookup SQLite database; empty disables lookup")
	flag.StringVar(&c.deployment, "deployment", "", "lookup refresh deployment state; supersedes -db")
	flag.StringVar(&c.listen, "listen", "127.0.0.1:8080", "HTTP listen address")
	flag.StringVar(&c.public, "public", "public", "public files directory")
	flag.StringVar(&c.tiles, "tiles", "data/newport.pmtiles", "local Protomaps archive")
	flag.IntVar(&c.workers, "routing-concurrency", 4, "maximum concurrent routing requests, 1..4")
	flag.Parse()
	if c.workers < 1 || c.workers > 4 || c.cache < 1 || c.cache > 128 || c.selection != "" && c.routing == "" {
		log.Fatal("invalid routing budgets or selection without routing snapshot")
	}
	if err := serve(c); err != nil {
		log.Fatal(err)
	}
}

func serve(c configuration) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	mux, closeHandler, err := newService(ctx, c)
	if err != nil {
		return err
	}
	defer closeHandler()
	server := &http.Server{Addr: c.listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 60 * time.Second}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	log.Printf("Open Maps: http://%s", c.listen)
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, stop := context.WithTimeout(context.Background(), 35*time.Second)
		defer stop()
		err := server.Shutdown(shutdown)
		if err != nil {
			server.Close()
		}
		return err
	}
}

// One service owns lookup and routing independently. Routing leases cover encoding;
// lookup replacement cannot retire a routing reader or reset admission.
func newService(parent context.Context, c configuration) (http.Handler, func(), error) {
	ctx, cancel := context.WithCancel(parent)
	var closers []func()
	cleanup := func() {
		cancel()
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}
	fail := func(err error) (http.Handler, func(), error) { cleanup(); return nil, nil, err }
	var lookup http.Handler = api.Handler{}
	var live *dataset.Live
	if c.deployment != "" {
		var err error
		live, err = dataset.Open(ctx, c.deployment)
		if err != nil {
			return fail(err)
		}
		closers = append(closers, func() { live.Close() })
		lookup = live
	} else if c.db != "" {
		dbPath, err := filepath.Abs(c.db)
		if err != nil {
			return fail(err)
		}
		store, err := places.Open(dbPath)
		if err != nil {
			return fail(err)
		}
		closers = append(closers, func() { store.Close() })
		geocoder, err := geocoding.Open(ctx, dbPath)
		if err != nil {
			return fail(err)
		}
		lookup = api.Handler{Places: store, Geocoding: geocoder}
	}
	var router *routing.Service
	if c.routing != "" {
		var err error
		router, err = routing.OpenService(ctx, c.routing, c.workers, c.cache<<20)
		if err != nil {
			return fail(err)
		}
		closers = append(closers, func() { router.Close() })
		if c.selection != "" {
			done := make(chan struct{})
			go func() { defer close(done); watchScoutSelection(ctx, router, c.selection) }()
			closers = append(closers, func() { <-done })
		}
	}
	mux := http.NewServeMux()
	mux.Handle("/directions/", api.RoutingAdmission(api.Handler{Routing: router}, c.workers))
	mux.Handle("/v1/", lookup)
	mux.Handle("/maps/api/", lookup)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(405)
			return
		}
		health := map[string]any{"status": "ok", "routing_available": router != nil, "routing_duration_available": router != nil, "lookup_available": c.db != "" || live != nil}
		status := 200
		if live != nil {
			dataset, errorText := live.Status(r.Context())
			health["dataset"] = dataset
			if errorText != "" {
				health["error"] = errorText
				health["status"] = "degraded"
				status = 503
			}
		}
		if router != nil {
			meta, reloadError := router.Status()
			// Keep the established health JSON key for existing monitors.
			health["routing_candidate"] = meta
			health["reload_error"] = reloadError
			health["exhaustive_source_coverage_verified"] = false
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(health)
	})
	mux.HandleFunc("/tiles/newport.pmtiles", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(405)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeFile(w, r, c.tiles)
	})
	mux.Handle("/", http.FileServer(http.Dir(c.public)))
	return mux, cleanup, nil
}
