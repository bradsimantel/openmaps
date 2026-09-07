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
	"strings"
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
	manifest := flag.String("manifest", "imports/newport.lock.json", "pinned regional source manifest")
	identities := flag.String("identities", "imports/identities.json", "permanent source identity mappings")
	checksum := flag.String("checksum", "imports/newport.bundle.sha256", "expected normalized bundle checksum")
	fetch := flag.Bool("fetch", false, "download missing pinned source files")
	writeLock := flag.Bool("write-lock", false, "maintainer operation: accept reviewed exports and bundle checksums")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	m, e := importer.ReadManifest(*manifest)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(*data, 0755); e != nil {
		return e
	}
	if *fetch {
		m, e = importer.FetchSources(ctx, m, *data, *writeLock)
		if e != nil {
			return e
		}
	}
	raw, e := os.ReadFile(*identities)
	if e != nil {
		return e
	}
	ids := map[string]string{}
	if e = json.Unmarshal(raw, &ids); e != nil {
		return e
	}
	if ids == nil {
		return fmt.Errorf("identities must be an object")
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
	if !*writeLock {
		expected, e := os.ReadFile(*checksum)
		if e != nil {
			return e
		}
		if digest != strings.TrimSpace(string(expected)) {
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
	if *writeLock {
		if e = importer.WriteJSON(*manifest, m); e != nil {
			return e
		}
		if e = os.WriteFile(*checksum, []byte(digest+"\n"), 0644); e != nil {
			return e
		}
	}
	out, _ := json.MarshalIndent(audit, "", "  ")
	fmt.Println(string(out))
	return nil
}
