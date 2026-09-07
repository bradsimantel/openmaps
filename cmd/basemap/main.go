package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
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
	manifest := flag.String("manifest", "imports/basemap.lock.json", "pinned Protomaps extraction manifest")
	output := flag.String("out", "data/newport.pmtiles", "regional PMTiles output")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	raw, e := os.ReadFile(*manifest)
	if e != nil {
		return e
	}
	var lock struct {
		URL         string    `json:"url"`
		ToolVersion string    `json:"tool_version"`
		BBox        []float64 `json:"bbox"`
		MaxZoom     int       `json:"maxzoom"`
		SHA256      string    `json:"sha256"`
	}
	if e = json.Unmarshal(raw, &lock); e != nil {
		return e
	}
	if !regexp.MustCompile(`^v\d+\.\d+\.\d+$`).MatchString(lock.ToolVersion) || len(lock.BBox) != 4 || lock.MaxZoom < 0 || lock.MaxZoom > 24 || !strings.HasPrefix(lock.URL, "https://") {
		return fmt.Errorf("invalid basemap lock")
	}
	if _, e = os.Stat(*output); e == nil {
		return importer.Verify(*output, lock.SHA256)
	} else if !os.IsNotExist(e) {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(*output), 0755); e != nil {
		return e
	}
	dir, e := os.MkdirTemp(filepath.Dir(*output), ".basemap-*")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	candidate := filepath.Join(dir, "newport.pmtiles")
	parts := []string{}
	for _, v := range lock.BBox {
		parts = append(parts, strconv.FormatFloat(v, 'f', -1, 64))
	}
	// The format-specific extractor is a pinned Go dependency, invoked in its own
	// module so cloud SDK dependencies do not enter the HTTP service's module.
	cmd := exec.CommandContext(ctx, "go", "run", "github.com/protomaps/go-pmtiles@"+lock.ToolVersion, "extract", lock.URL, candidate, "--bbox="+strings.Join(parts, ","), "--maxzoom="+strconv.Itoa(lock.MaxZoom))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if e = cmd.Run(); e != nil {
		return e
	}
	if e = importer.Verify(candidate, lock.SHA256); e != nil {
		return e
	}
	if e = os.Link(candidate, *output); e != nil {
		return e
	}
	fmt.Println("Verified", *output)
	return nil
}
