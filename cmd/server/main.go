package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"openmaps/internal/api"
	"openmaps/internal/dataset"
	"openmaps/internal/geocoding"
	"openmaps/internal/places"
	"openmaps/internal/routing"
)

func main() {
	scoutDir := flag.String("routing-scout", "", "isolated experimental prepared Scout candidate; coordinates only")
	scoutSelection := flag.String("scout-selection", "", "optional isolated JSON selection file for candidate replacement")
	scoutCache := flag.Int64("scout-cache-mib", 128, "graph page payload per Scout reader, 1..128 MiB; turn/landmark caches are additional")
	db := flag.String("db", "data/openmaps.sqlite", "SQLite database")
	state := flag.String("deployment", "", "refresh deployment state; supersedes -db and supports live switching")
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	public := flag.String("public", "public", "public files directory")
	tiles := flag.String("tiles", "data/newport.pmtiles", "local Protomaps regional archive")
	routingCache := flag.String("routing-cache", "", "optional directory for verified read-only mapped routing arrays (Linux/macOS)")
	preparedDir := flag.String("routing-prepared", "", "trusted offline prepared routing publication directory")
	legacyLoad := flag.Bool("routing-legacy-load", false, "explicitly allow graph-sized legacy routing reconstruction at startup")
	routingConcurrency := flag.Int("routing-concurrency", 4, "maximum concurrent Compute Routes HTTP requests (1-64)")
	flag.Parse()
	if *legacyLoad && *preparedDir != "" || !*legacyLoad && *routingCache != "" {
		log.Fatal("-routing-cache requires -routing-legacy-load; choose either legacy or prepared routing")
	}
	if *routingConcurrency < 1 || *routingConcurrency > 64 {
		log.Fatal("routing-concurrency must be 1-64")
	}
	if *scoutDir != "" {
		if *state != "" || *preparedDir != "" || *legacyLoad || *routingCache != "" {
			log.Fatal("Scout candidate cannot be combined with an existing deployment or routing backend")
		}
		if *scoutCache < 1 || *scoutCache > 128 {
			log.Fatal("scout-cache-mib must be 1..128")
		}
		if err := serveScout(*scoutDir, *scoutSelection, *listen, *public, *routingConcurrency, *scoutCache<<20); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *scoutSelection != "" {
		log.Fatal("-scout-selection requires -routing-scout")
	}
	abs, err := filepath.Abs(*db)
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	if *state != "" {
		var live *dataset.Live
		if *legacyLoad {
			live, err = dataset.OpenWithRoutingCache(context.Background(), *state, *routingCache)
		} else {
			live, err = dataset.OpenPrepared(context.Background(), *state, *preparedDir)
		}
		if err != nil {
			log.Fatal(err)
		}
		defer live.Close()
		mux.Handle("/directions/", api.RoutingAdmission(live, *routingConcurrency))
		mux.Handle("/v1/", live)
		mux.Handle("/maps/api/", live)
		mux.Handle("/healthz", live)
	} else {
		store, err := places.Open(abs)
		if err != nil {
			log.Fatal(err)
		}
		defer store.Close()
		geocoder, err := geocoding.Open(context.Background(), abs)
		if err != nil {
			log.Fatal(err)
		}
		router, err := routing.OpenRuntime(context.Background(), abs, *preparedDir, *legacyLoad, *routingCache)
		if err != nil {
			log.Fatal(err)
		}
		defer router.Close()
		handler := api.Handler{Places: store, Geocoding: geocoder, Routing: router}
		mux.Handle("/directions/", api.RoutingAdmission(handler, *routingConcurrency))
		mux.Handle("/v1/", handler)
		mux.Handle("/maps/api/", handler)
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{"status": "ok", "routing_available": router != nil, "routing_duration_available": router.HasDuration()})
		})
	}
	mux.HandleFunc("/tiles/newport.pmtiles", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		http.ServeFile(w, r, *tiles)
	})
	mux.Handle("/", http.FileServer(http.Dir(*public)))
	server := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 2 * time.Minute, IdleTimeout: 60 * time.Second}
	log.Printf("Open Maps: http://%s", *listen)
	log.Fatal(server.ListenAndServe())
}
