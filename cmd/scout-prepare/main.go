// scout-prepare publishes a separate immutable Scout graph snapshot.
package main

import (
	"context"
	"flag"
	"fmt"
	"openmaps/internal/importer/scout"
	"openmaps/internal/routing"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	support := flag.Bool("reverse-support-only", false, "certify nodes where opposing indexes form a complete reverse view")
	potential := flag.Bool("potential-only", false, "prepare validated geometric A* lower bound")
	turns := flag.Bool("turns-only", false, "prepare routing index for an existing prepared graph")
	root := flag.String("root", "", "acquisition directory containing packages and receipts")
	plan := flag.String("plan", "acquisition.json", "acquisition plan filename")
	out := flag.String("out", "", "new prepared output directory")
	base := flag.String("extend", "", "verified prepared graph to extend without reimporting retained packages")
	clone := flag.Bool("clone-base", false, "use macOS copy-on-write clone for extension")
	expanded := flag.Int64("expanded-gib", 48, "expanded spool budget, 1..64 GiB")
	reserve := flag.Int64("reserve-gib", 32, "minimum remaining free disk GiB")
	flag.Parse()
	phases := 0
	for _, enabled := range []bool{*support, *potential, *turns} {
		if enabled {
			phases++
		}
	}
	if phases > 1 {
		return fmt.Errorf("choose one preparation phase")
	}
	if (phases != 0 && (*base != "" || *clone)) || (*clone && *base == "") {
		return fmt.Errorf("extension flags require the graph phase and a base")
	}
	if *root == "" || *out == "" || filepath.Base(*plan) != *plan || *expanded < 1 || *expanded > 64 || *reserve < 32 || *reserve > 1024 {
		return fmt.Errorf("invalid paths/budgets")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if *support {
		return routing.PrepareReverseSupport(ctx, *out)
	}
	if *potential {
		return routing.PrepareScoutPotential(ctx, *out)
	}
	if *turns {
		return routing.PrepareScoutTurns(ctx, *out)
	}
	pinned, err := scout.ReadAcquiredPlan(ctx, *root, *plan, scout.Budgets{Download: 16 << 30, Reserve: 32 << 30})
	if err != nil {
		return err
	}
	if pinned.Budgets.Reserve > *reserve<<30 {
		return fmt.Errorf("preparation reserve weaker than acquisition pin")
	}
	lock := preparationLock(pinned)
	budget := routing.ScoutBudgets{CompressedBytes: pinned.Budgets.Download, ExpandedBytes: *expanded << 30, ReserveBytes: *reserve << 30}
	if *base != "" {
		return routing.ExtendScoutPackages(ctx, filepath.Join(*root, "packages"), *out, *base, *clone, lock, budget)
	}
	return routing.PrepareScoutPackages(ctx, filepath.Join(*root, "packages"), *out, lock, budget)
}

// Keep acquisition provenance/receipt handling in importer/scout and the graph
// format in routing. Only validated graph inputs cross this explicit boundary.
func preparationLock(p scout.Plan) routing.ScoutLock {
	lock := routing.ScoutLock{Schema: p.Schema, PackageSchema: p.PackageSchema, Version: p.Version, Dataset: p.Dataset, Timestamp: p.Timestamp}
	for _, pin := range p.Packages {
		q := routing.ScoutPackage{ID: pin.ID, MD5: pin.MD5, Bytes: pin.Bytes, SHA256: pin.SHA256}
		for _, tile := range pin.Tiles {
			q.Tiles = append(q.Tiles, routing.ScoutTile{Name: tile.Name, Bytes: tile.Bytes, SHA256: tile.SHA256})
		}
		lock.Packages = append(lock.Packages, q)
	}
	return lock
}
