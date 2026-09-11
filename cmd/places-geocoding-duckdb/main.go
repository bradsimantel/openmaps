// places-geocoding-duckdb builds the non-default Parquet plus DuckDB candidate.
// The production server continues to use its SQLite snapshot.
package main

import (
	"context"
	"encoding/json"
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
	bundlePath := flag.String("bundle", "data/newport.json", "normalized regional bundle")
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
	var bundle importer.Bundle
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&bundle); err != nil {
		return err
	}
	if err = placeduckdb.Build(context.Background(), *out, bundle); err != nil {
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
