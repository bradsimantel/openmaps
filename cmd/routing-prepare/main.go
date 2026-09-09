// Command routing-prepare validates a complete snapshot and publishes immutable
// routing query data. This explicit offline operation may use graph-sized memory.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"openmaps/internal/importer"
	"openmaps/internal/routing"
)

func main() {
	db := flag.String("db", "", "authoritative immutable SQLite snapshot")
	out := flag.String("out", "", "new trusted publication directory (receipt must not exist)")
	scratch := flag.String("scratch-dir", "", "temporary identity-sort directory (default: beside source database)")
	batch := flag.Int("sort-records", routing.DefaultConstructionSortRecords, "identity-sort records per batch (1..4194304; graph memory is separate)")
	flag.Parse()
	if *db == "" || *out == "" {
		log.Fatal("-db and -out required")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *scratch == "" {
		*scratch = filepath.Dir(*db)
	}
	ctx, err := routing.WithConstructionScratch(ctx, *scratch, *batch)
	if err != nil {
		log.Fatal(err)
	}
	ctx = routing.WithLoadObserver(ctx, func(p routing.LoadPhase) { log.Printf("PHASE %+v", p) })
	sum, e := importer.Checksum(*db)
	if e != nil {
		log.Fatal(e)
	}
	_, s, e := importer.ReadSnapshotWithRouting(ctx, *db)
	if e != nil {
		log.Fatal(e)
	}
	defer s.Close()
	receipt, e := s.PublishPrepared(ctx, *db, sum, *out)
	if e != nil {
		log.Fatal(e)
	}
	json.NewEncoder(os.Stdout).Encode(receipt)
}
