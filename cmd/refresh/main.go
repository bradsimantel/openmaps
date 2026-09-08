// refresh builds, compares, selects and rolls back immutable local snapshots.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"openmaps/internal/dataset"
	"openmaps/internal/importer"
)

func main() {
	if e := run(); e != nil {
		log.Fatal(e)
	}
}
func readJSON(path string, v any) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	return d.Decode(v)
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: refresh build|compare|review|init|activate|rollback|status [flags]")
	}
	command := os.Args[1]
	f := flag.NewFlagSet(command, flag.ContinueOnError)
	baseline := f.String("baseline", "data/openmaps.sqlite", "retained baseline database")
	candidate := f.String("candidate", "", "candidate database (build refuses existing output)")
	bundle := f.String("bundle", "", "normalized input bundle")
	checksum := f.String("checksum", "", "expected input bundle SHA-256 file")
	preparedDir := f.String("routing-prepared", "", "trusted prepared routing directory for rollback")
	routingPBF := f.String("routing-pbf", "", "optional pinned OSM PBF for a driving graph in a new build")
	replacements := f.String("replacements", "", "optional reviewed one-to-one replacements JSON")
	queries := f.String("queries", "imports/newport.queries.json", "representative search expectations JSON")
	report := f.String("report", "data/refresh-report.json", "full deterministic comparison JSON")
	review := f.String("review", "", "review JSON bound to report SHA-256")
	reviewer := f.String("reviewer", "", "person or agent recording the review")
	reason := f.String("reason", "", "review findings, including treatment of uncertainty")
	state := f.String("state", "data/deployment.json", "local deployment state")
	if e := f.Parse(os.Args[2:]); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	ctx := context.Background()
	switch command {
	case "build":
		if *candidate == "" || *bundle == "" || *checksum == "" {
			return fmt.Errorf("build requires -candidate, -bundle and -checksum")
		}
		expected, e := os.ReadFile(*checksum)
		if e != nil {
			return e
		}
		if e = importer.Verify(*bundle, string(expected)); e != nil {
			return e
		}
		var b importer.Bundle
		if e = readJSON(*bundle, &b); e != nil {
			return e
		}
		base, e := importer.ReadSnapshot(ctx, *baseline)
		if e != nil {
			return e
		}
		decisions := []importer.Replacement{}
		if *replacements != "" {
			if e = readJSON(*replacements, &decisions); e != nil {
				return e
			}
		}
		b, history, e := importer.Reconcile(b, base, decisions)
		if e != nil {
			return e
		}
		abs, e := filepath.Abs(*candidate)
		if e != nil {
			return e
		}
		temp, e := os.CreateTemp(filepath.Dir(abs), ".refresh-*.sqlite")
		if e != nil {
			return e
		}
		name := temp.Name()
		temp.Close()
		os.Remove(name)
		defer os.Remove(name)
		if e = importer.Build(ctx, name, b); e != nil {
			return e
		}
		if *routingPBF != "" {
			if e = importer.AddRouting(ctx, name, *routingPBF, b.Manifest); e != nil {
				return e
			}
		}
		if e = importer.SaveRefreshMetadata(name, history, decisions, strings.TrimSpace(string(expected))); e != nil {
			return e
		}
		if _, e = importer.ReadSnapshot(ctx, name); e != nil {
			return e
		}
		if e = os.Link(name, abs); e != nil {
			return e
		}
		fmt.Println("Built", abs)
	case "compare":
		if *candidate == "" {
			return fmt.Errorf("compare requires -candidate")
		}
		var checks []importer.QueryCheck
		if e := readJSON(*queries, &checks); e != nil {
			return e
		}
		r, e := importer.Compare(ctx, *baseline, *candidate, checks)
		if e != nil {
			return e
		}
		if e = importer.WriteJSON(*report, r); e != nil {
			return e
		}
		sum, e := importer.Checksum(*report)
		if e != nil {
			return e
		}
		summary := map[string]any{"report": *report, "report_sha256": sum, "before": r.BeforeCounts, "after": r.AfterCounts, "continuing_ids": r.ContinuingIDs, "added": len(r.Added), "removed": len(r.Removed), "changed": len(r.Changed), "relationships_added": len(r.RelationshipsAdded), "relationships_removed": len(r.RelationshipsRemoved), "review_matches": len(r.ReviewMatches), "violations": r.Violations}
		out, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Println(string(out))
		if len(r.Violations) > 0 {
			return fmt.Errorf("candidate failed validation; see report")
		}
	case "review":
		if *review == "" || strings.TrimSpace(*reviewer) == "" || strings.TrimSpace(*reason) == "" {
			return fmt.Errorf("review requires -review, -reviewer and -reason after inspecting the report")
		}
		var r importer.Report
		if e := readJSON(*report, &r); e != nil {
			return e
		}
		if r.Schema != 1 || len(r.Violations) != 0 || len(r.Queries) == 0 {
			return fmt.Errorf("cannot review a failed or unchecked candidate")
		}
		sum, e := importer.Checksum(*report)
		if e != nil {
			return e
		}
		return importer.WriteJSON(*review, dataset.Review{ReportSHA256: sum, Reviewer: *reviewer, Reason: *reason})
	case "init":
		return dataset.Init(ctx, *state, *baseline)
	case "activate":
		if *candidate == "" || *review == "" {
			return fmt.Errorf("activate requires -candidate and -review")
		}
		return dataset.Activate(ctx, *state, *candidate, *report, *review)
	case "rollback":
		if *preparedDir != "" {
			return dataset.RollbackPrepared(ctx, *state, *preparedDir)
		}
		return dataset.Rollback(ctx, *state)
	case "status":
		s, e := dataset.Read(*state)
		if e != nil {
			return e
		}
		out, _ := json.MarshalIndent(s, "", "  ")
		fmt.Println(string(out))
	default:
		return fmt.Errorf("unknown refresh command %q", command)
	}
	return nil
}
