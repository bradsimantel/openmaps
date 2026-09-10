// scout-prepare publishes a separate immutable provider-tile candidate.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"openmaps/internal/importer/scout"
	"openmaps/internal/routing/valhallatiles"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strings"
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
	reverse := flag.Bool("reverse-turns-only", false, "prepare reverse prohibition index for bidirectional search")
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
	for _, enabled := range []bool{*support, *reverse, *potential, *turns} {
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
		return valhallatiles.PrepareReverseSupport(ctx, *out)
	}
	if *reverse {
		return valhallatiles.PrepareScoutReverseTurns(ctx, *out)
	}
	if *potential {
		return valhallatiles.PrepareScoutPotential(ctx, *out)
	}
	if *turns {
		return valhallatiles.PrepareScoutTurns(ctx, *out)
	}
	pinned, err := scout.ReadPlan(*root, *plan)
	if err != nil {
		return err
	}
	if err := scout.ValidatePlan(*root, pinned, scout.Budgets{Download: 16 << 30, Reserve: 32 << 30}); err != nil {
		return err
	}
	b, err := readMetadata(filepath.Join(*root, *plan), 8<<20)
	if err != nil {
		return err
	}
	var lock struct {
		valhallatiles.ScoutLock
		MetadataSHA256 map[string]string `json:"metadata_sha256"`
		Budgets        struct {
			Download int64 `json:"download_bytes"`
			Reserve  int64 `json:"disk_reserve_bytes"`
		} `json:"budgets"`
	}
	if err := json.Unmarshal(b, &lock); err != nil {
		return err
	}
	if lock.Budgets.Download < 1 || lock.Budgets.Download > 16<<30 || lock.Budgets.Reserve < 32<<30 || lock.Budgets.Reserve > *reserve<<30 {
		return fmt.Errorf("invalid acquisition budgets or reserve weaker than pin")
	}
	if len(lock.MetadataSHA256) != 3 || lock.MetadataSHA256["catalog.json"] == "" || lock.MetadataSHA256["digest.md5.bz2"] == "" || lock.MetadataSHA256["packages.html"] == "" {
		return fmt.Errorf("complete provider metadata pins required")
	}
	for name, want := range lock.MetadataSHA256 {
		if filepath.Base(name) != name {
			return fmt.Errorf("invalid metadata filename")
		}
		b, err := readMetadata(filepath.Join(*root, name), 16<<20)
		if err != nil {
			return err
		}
		if fmt.Sprintf("%x", sha256.Sum256(b)) != want {
			return fmt.Errorf("metadata changed: %s", name)
		}
	}
	for i, p := range lock.Packages {
		if p.ID == "" || strings.IndexFunc(p.ID, func(c rune) bool { return c < '0' || c > '9' }) >= 0 {
			return fmt.Errorf("invalid package id")
		}
		b, err := readMetadata(filepath.Join(*root, "receipts", p.ID+".json"), 65536)
		if err != nil {
			return err
		}
		var receipt struct {
			valhallatiles.ScoutPackage
			Generation map[string]string `json:"generation"`
		}
		if err := json.Unmarshal(b, &receipt); err != nil {
			return err
		}
		if receipt.ID != p.ID || receipt.Bytes != p.Bytes || receipt.MD5 != p.MD5 || (p.SHA256 != "" && receipt.SHA256 != p.SHA256) || !reflect.DeepEqual(receipt.Generation, lock.MetadataSHA256) {
			return fmt.Errorf("package receipt/plan mismatch")
		}
		lock.Packages[i] = receipt.ScoutPackage
	}
	budget := valhallatiles.ScoutBudgets{CompressedBytes: lock.Budgets.Download, ExpandedBytes: *expanded << 30, ReserveBytes: *reserve << 30}
	if *base != "" {
		return valhallatiles.ExtendScoutPackages(ctx, filepath.Join(*root, "packages"), *out, *base, *clone, lock.ScoutLock, budget)
	}
	return valhallatiles.PrepareScoutPackages(ctx, filepath.Join(*root, "packages"), *out, lock.ScoutLock, budget)
}

func readMetadata(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("metadata exceeds budget")
	}
	return b, nil
}
