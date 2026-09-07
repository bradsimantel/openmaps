package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"openmaps/internal/importer"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
func run() error {
	bundle := flag.String("bundle", "data/newport.json", "normalized regional bundle")
	db := flag.String("db", "data/openmaps.sqlite", "output (must not already exist)")
	checksum := flag.String("checksum", "imports/newport.bundle.sha256", "expected bundle SHA-256 file")
	flag.Parse()
	expected, err := os.ReadFile(*checksum)
	if err != nil {
		return err
	}
	file, err := os.Open(*bundle)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != strings.TrimSpace(string(expected)) {
		return fmt.Errorf("bundle checksum mismatch")
	}
	if _, err = file.Seek(0, 0); err != nil {
		return err
	}
	var b importer.Bundle
	dec := json.NewDecoder(file)
	dec.DisallowUnknownFields()
	if err = dec.Decode(&b); err != nil {
		return err
	}
	if _, err = os.Stat(*db); err == nil {
		return fmt.Errorf("output exists; choose a new -db path")
	} else if !os.IsNotExist(err) {
		return err
	}
	abs, err := filepath.Abs(*db)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(abs), "openmaps-import-*.sqlite")
	if err != nil {
		return err
	}
	name := temp.Name()
	temp.Close()
	os.Remove(name)
	defer os.Remove(name)
	if err = importer.Build(context.Background(), name, b); err != nil {
		return err
	}
	// Link publishes atomically and refuses a destination created concurrently.
	if err = os.Link(name, abs); err != nil {
		return err
	}
	fmt.Printf("Imported %d source records into %s\n", len(b.Records), abs)
	return nil
}
