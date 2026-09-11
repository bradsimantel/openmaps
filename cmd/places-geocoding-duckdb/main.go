// places-geocoding-duckdb builds a production Parquet plus DuckDB generation.
// places-geocoding-import is the normal Newport command and also selects it.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"openmaps/internal/importer"
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	bundlePath := flag.String("bundle", "data/newport.json", "deterministic provider-record stream")
	configPath := flag.String("config", "config/places-geocoding.json", "pinned Places and geocoding source configuration")
	out := flag.String("out", "data/openmaps-duckdb", "new output artifact directory")
	flag.Parse()
	if flag.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	manifest, err := importer.ReadManifest(*configPath)
	if err != nil {
		return err
	}
	if err = importer.Verify(*bundlePath, manifest.BundleSHA256); err != nil {
		return err
	}
	f, err := os.Open(*bundlePath)
	if err != nil {
		return err
	}
	defer f.Close()
	if err = placeduckdb.BuildJSON(context.Background(), *out, f); err != nil {
		return err
	}
	manifestOut, err := placeduckdb.Verify(*out)
	if err != nil {
		return err
	}
	var bytes int64
	entities := 0
	for _, file := range manifestOut.Files {
		stat, e := os.Stat(filepath.Join(*out, file.Name))
		if e != nil {
			return e
		}
		bytes += stat.Size()
		if file.Name == placeduckdb.EntitiesName {
			entities = file.Rows
		}
	}
	stat, err := os.Stat(filepath.Join(*out, placeduckdb.ManifestName))
	if err != nil {
		return err
	}
	bytes += stat.Size()
	fmt.Printf("Built %d entities into %s (%d bytes)\n", entities, *out, bytes)
	return nil
}
