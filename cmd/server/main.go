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
	"syscall"
	"time"

	"openmaps/internal/api"
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
	"openmaps/internal/routing"
)

type configuration struct {
	routingSnapshot, routingSelection, lookup, lookupSelection, listen, public, tiles string
	routingConcurrency                                                                int
	routingCache                                                                      int64
}

func main() {
	var c configuration
	flag.StringVar(&c.routingSnapshot, "routing-snapshot", "", "prepared routing snapshot directory")
	flag.StringVar(&c.routingSelection, "routing-selection", "", "JSON routing selection file, independent of lookup selection")
	flag.Int64Var(&c.routingCache, "routing-cache-mib", 128, "graph page payload per reader, 1..128 MiB; index caches are additional")
	flag.StringVar(&c.lookup, "lookup", "", "verified Parquet/DuckDB lookup generation directory; empty disables direct lookup")
	flag.StringVar(&c.lookupSelection, "lookup-selection", "data/lookup-selection.json", "JSON lookup-generation selection; empty uses -lookup")
	flag.StringVar(&c.listen, "listen", "127.0.0.1:8080", "HTTP listen address")
	flag.StringVar(&c.public, "public", "public", "public files directory")
	flag.StringVar(&c.tiles, "tiles", "data/newport.pmtiles", "local Protomaps archive")
	flag.IntVar(&c.routingConcurrency, "routing-concurrency", 4, "maximum concurrent routing requests, 1..4")
	flag.Parse()
	if c.routingConcurrency < 1 || c.routingConcurrency > 4 || c.routingCache < 1 || c.routingCache > 128 || c.routingSelection != "" && c.routingSnapshot == "" {
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
	lookupAPI := api.Handler{}
	var lookup http.Handler = lookupAPI
	var live *placeduckdb.LiveHandler
	var directReference placeduckdb.Reference
	if c.lookupSelection != "" {
		var err error
		live, err = placeduckdb.OpenLiveHandler(c.lookupSelection)
		if err != nil {
			return fail(err)
		}
		closers = append(closers, func() { live.Close() })
		lookup = live
	} else if c.lookup != "" {
		store, err := placeduckdb.Open(c.lookup)
		if err != nil {
			return fail(err)
		}
		closers = append(closers, func() { store.Close() })
		directReference, err = placeduckdb.Describe(c.lookup)
		if err != nil {
			return fail(err)
		}
		lookupAPI = api.Handler{Places: store, Geocoding: store}
		lookup = snapshotHeader(lookupAPI, directReference.SHA256)
	}
	var router *routing.Service
	if c.routingSnapshot != "" {
		var err error
		router, err = routing.OpenService(ctx, c.routingSnapshot, c.routingConcurrency, c.routingCache<<20)
		if err != nil {
			return fail(err)
		}
		closers = append(closers, func() { router.Close() })
		if c.routingSelection != "" {
			done := make(chan struct{})
			go func() { defer close(done); watchRoutingSelection(ctx, router, c.routingSelection) }()
			closers = append(closers, func() { <-done })
		}
	}
	mux := http.NewServeMux()
	routeAPI := lookupAPI
	routeAPI.Routing = router
	var routeHandler http.Handler = routeAPI
	if live != nil {
		routeHandler = live.Routes(router)
	} else if directReference.SHA256 != "" {
		routeHandler = snapshotHeader(routeAPI, directReference.SHA256)
	}
	mux.Handle("/directions/", api.RoutingAdmission(routeHandler, c.routingConcurrency))
	mux.Handle("/v1/", lookup)
	mux.Handle("/maps/api/", lookup)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			w.WriteHeader(405)
			return
		}
		health := map[string]any{"status": "ok", "routing_available": router != nil, "routing_duration_available": router != nil, "lookup_available": c.lookup != "" || live != nil}
		status := 200
		if live != nil {
			lookupSnapshot, errorText := live.Status()
			health["lookup_snapshot"] = lookupSnapshot
			if errorText != "" {
				health["error"] = errorText
				health["status"] = "degraded"
				status = 503
			}
		} else if c.lookup != "" {
			health["lookup_snapshot"] = directReference
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

func snapshotHeader(next http.Handler, checksum string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-OpenMaps-Lookup-Snapshot", checksum)
		next.ServeHTTP(w, r)
	})
}
