package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"openmaps/internal/routing/valhallatiles"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	memory := flag.Int64("memory-mib", 2048, "Go soft memory target; use external RSS/disk supervision")
	prepared := flag.String("prepared", "", "prepared Scout graph directory")
	out := flag.String("out", "", "new or matching resumable landmark directory")
	seeds := flag.String("seeds", "", "JSON landmark seed array, 1..16 points")
	prefix := flag.Int("publish-prefix", 0, "publish this many completed pairs as a separate immutable snapshot")
	built := flag.String("built", "", "resumable build directory for prefix publication")
	reindex := flag.String("reindex-from", "", "old prepared graph for proven disconnected extension; requires built old landmarks")
	flag.Parse()
	if *prepared == "" || *out == "" || *memory < 512 || *memory > 3072 {
		return fmt.Errorf("prepared and out required; memory-mib must be 512..3072")
	}
	debug.SetMemoryLimit(*memory << 20)
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *reindex != "" {
		if *built == "" || *seeds != "" || *prefix != 0 {
			return fmt.Errorf("reindex requires built and no seeds/prefix")
		}
		return valhallatiles.ReindexLandmarks(ctx, *reindex, *built, *prepared, *out)
	}
	if *prefix != 0 {
		if *built == "" || *seeds != "" {
			return fmt.Errorf("prefix publication requires built and no seeds")
		}
		return valhallatiles.PublishLandmarkPrefix(ctx, *prepared, *built, *out, *prefix)
	}
	if *seeds == "" || *built != "" {
		return fmt.Errorf("construction requires seeds and no built")
	}
	f, err := os.Open(*seeds)
	if err != nil {
		return err
	}
	b, err := io.ReadAll(io.LimitReader(f, 65537))
	f.Close()
	if err != nil {
		return err
	}
	if len(b) > 65536 {
		return fmt.Errorf("seed file oversized")
	}
	var points []valhallatiles.LandmarkSeed
	if err := json.Unmarshal(b, &points); err != nil {
		return err
	}
	return valhallatiles.PrepareLandmarks(ctx, *prepared, *out, points)
}
