// prepare owns regional acquisition, provider decoding, normalization and audit.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"openmaps/internal/importer"
)

func main() {
	if e := run(); e != nil {
		log.Fatal(e)
	}
}
func run() error {
	data := flag.String("data", "data", "source cache and output directory")
	config := flag.String("config", "config/places-geocoding.json", "pinned Places and geocoding source configuration")
	identities := flag.String("identities", "", "optional permanent source identity mappings JSON")
	fetch := flag.Bool("fetch", false, "download missing pinned source files")
	updateConfig := flag.Bool("update-config", false, "maintainer operation: accept reviewed exports and bundle checksum")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	m, e := importer.ReadManifest(*config)
	if e != nil {
		return e
	}
	expected := m.BundleSHA256
	m.BundleSHA256 = ""
	if e = os.MkdirAll(*data, 0755); e != nil {
		return e
	}
	if *fetch {
		m, e = importer.FetchSources(ctx, m, *data, *updateConfig)
		if e != nil {
			return e
		}
	}
	ids := map[string]string{}
	if *identities != "" {
		raw, e := os.ReadFile(*identities)
		if e != nil {
			return e
		}
		if e = json.Unmarshal(raw, &ids); e != nil {
			return e
		}
		if ids == nil {
			return fmt.Errorf("identities must be an object")
		}
	}
	bundle, audit, e := importer.Prepare(ctx, m, *data, ids)
	if e != nil {
		return e
	}
	encoded, e := json.Marshal(bundle)
	if e != nil {
		return e
	}
	sum := sha256.Sum256(append(encoded, '\n'))
	digest := hex.EncodeToString(sum[:])
	if !*updateConfig {
		if digest != expected {
			return fmt.Errorf("normalized bundle checksum mismatch: got %s; outputs were not published", digest)
		}
	}
	if e = importer.WriteJSON(filepath.Join(*data, "newport.json"), bundle); e != nil {
		return e
	}
	audit["bundle_sha256"] = digest
	if e = importer.WriteJSON(filepath.Join(*data, "audit.json"), audit); e != nil {
		return e
	}
	if *updateConfig {
		m.BundleSHA256 = digest
		if e = importer.WriteJSON(*config, m); e != nil {
			return e
		}
	}
	out, _ := json.MarshalIndent(audit, "", "  ")
	fmt.Println(string(out))
	return nil
}
