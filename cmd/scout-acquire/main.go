// scout-acquire captures, plans, fetches and verifies immutable Scout inputs.
package main

import (
	"context"
	"flag"
	"fmt"
	"openmaps/internal/importer/scout"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: scout-acquire snapshot|plan|fetch|verify [flags]")
	}
	f := flag.NewFlagSet(os.Args[1], flag.ContinueOnError)
	root := f.String("root", "", "acquisition directory")
	name := f.String("plan", "acquisition.json", "immutable plan filename")
	regions := f.String("regions", "north-america/us,north-america/canada,north-america/mexico", "comma-separated catalog prefixes")
	extra := f.String("extra", "", "explicit additional package IDs")
	download := f.Int64("download-gib", 12, "compressed budget 1..16 GiB")
	reserve := f.Int64("reserve-gib", 32, "disk reserve 32..1024 GiB")
	if e := f.Parse(os.Args[2:]); e != nil {
		return e
	}
	if *root == "" || filepath.Base(*name) != *name || *download < 1 || *download > 16 || *reserve < 32 || *reserve > 1024 || f.NArg() != 0 {
		return fmt.Errorf("invalid paths/budgets")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	c := scout.NewClient()
	budget := scout.Budgets{Download: *download << 30, Reserve: *reserve << 30}
	switch os.Args[1] {
	case "snapshot":
		return c.Snapshot(ctx, *root)
	case "plan":
		p, e := c.MakePlan(ctx, *root, strings.Split(*regions, ","), strings.Split(*extra, ","), budget)
		if e != nil {
			return e
		}
		return scout.WriteJSON(filepath.Join(*root, *name), p)
	case "fetch", "verify":
		p, e := scout.ReadPlan(*root, *name)
		if e != nil {
			return e
		}
		if os.Args[1] == "verify" {
			return scout.VerifyRetained(*root, p, budget)
		}
		return c.Fetch(ctx, *root, p, budget, os.Stdout)
	default:
		return fmt.Errorf("unknown acquisition action")
	}
}
