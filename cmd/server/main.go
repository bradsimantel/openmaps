package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"path/filepath"
	"time"

	"openmaps/internal/api"
	"openmaps/internal/dataset"
	"openmaps/internal/places"
)

func main() {
	db := flag.String("db", "data/openmaps.sqlite", "SQLite database")
	state := flag.String("deployment", "", "refresh deployment state; supersedes -db and supports live switching")
	listen := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	public := flag.String("public", "public", "public files directory")
	tiles := flag.String("tiles", "data/newport.pmtiles", "local Protomaps regional archive")
	flag.Parse()
	abs, err := filepath.Abs(*db)
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	if *state != "" {
		live, err := dataset.Open(context.Background(), *state)
		if err != nil {
			log.Fatal(err)
		}
		defer live.Close()
		mux.Handle("/v1/", live)
		mux.Handle("/healthz", live)
	} else {
		store, err := places.Open(abs)
		if err != nil {
			log.Fatal(err)
		}
		defer store.Close()
		mux.Handle("/v1/", api.Handler{Places: store})
		mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"status":"ok"}`))
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
	server := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	log.Printf("Open Maps: http://%s", *listen)
	log.Fatal(server.ListenAndServe())
}
