// places-geocoding-import builds and selects the immutable normalized-Parquet
// plus DuckDB Places/geocoding generation.
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
	bundle := flag.String("bundle", "data/newport.json", "deterministic provider-record stream")
	out := flag.String("out", "data/openmaps-duckdb", "new immutable generation directory")
	selection := flag.String("selection", "data/lookup-selection.json", "selection to initialize or activate; empty builds without selecting")
	config := flag.String("config", "config/places-geocoding.json", "pinned Places and geocoding source configuration")
	flag.Parse()
	manifest, err := importer.ReadManifest(*config)
	if err != nil {
		return err
	}
	if err = importer.Verify(*bundle, manifest.BundleSHA256); err != nil {
		return err
	}
	abs, err := filepath.Abs(*out)
	if err != nil {
		return err
	}
	file, err := os.Open(*bundle)
	if err != nil {
		return err
	}
	defer file.Close()
	if err = placeduckdb.BuildJSON(context.Background(), abs, file); err != nil {
		return err
	}
	artifact, err := placeduckdb.Verify(abs)
	if err != nil {
		return err
	}
	if *selection != "" {
		if _, readErr := placeduckdb.ReadSelection(*selection); os.IsNotExist(readErr) {
			err = placeduckdb.InitializeSelection(*selection, abs)
		} else if readErr != nil {
			return readErr
		} else {
			err = placeduckdb.ActivateSelection(*selection, abs)
		}
		if err != nil {
			return err
		}
	}
	entities := 0
	for _, item := range artifact.Files {
		if item.Name == placeduckdb.EntitiesName {
			entities = item.Rows
		}
	}
	fmt.Printf("Built %d entities into %s", entities, abs)
	if *selection != "" {
		fmt.Printf(" and selected it in %s", *selection)
	}
	fmt.Println()
	return nil
}
