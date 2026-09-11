// places-geocoding-refresh builds, compares, reviews, activates and rolls back
// immutable Places/geocoding Parquet/DuckDB generations.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"openmaps/internal/importer"
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
)

type review struct {
	ReportSHA256 string `json:"report_sha256"`
	Reviewer     string `json:"reviewer"`
	Reason       string `json:"reason"`
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func readJSON(path string, value any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: places-geocoding-refresh build|compare|review|init|activate|rollback|status [flags]")
	}
	command := os.Args[1]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	baseline := flags.String("baseline", "", "verified baseline generation; compare defaults to the selected current generation")
	candidate := flags.String("candidate", "", "candidate generation directory (build refuses an existing directory)")
	bundle := flags.String("bundle", "data/newport.json", "normalized provider-record stream")
	config := flags.String("config", "config/places-geocoding.json", "Places/geocoding source configuration")
	queries := flags.String("queries", "", "optional autocomplete expectations JSON")
	reportPath := flags.String("report", "data/refresh-report.json", "deterministic generation comparison JSON")
	reviewPath := flags.String("review", "", "review JSON bound to the comparison report")
	reviewer := flags.String("reviewer", "", "person or agent recording the review")
	reason := flags.String("reason", "", "review findings and treatment of uncertain changes")
	selectionPath := flags.String("selection", "data/lookup-selection.json", "atomic lookup-generation selection")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	ctx := context.Background()
	switch command {
	case "build":
		if *candidate == "" {
			return fmt.Errorf("build requires -candidate")
		}
		manifest, err := importer.ReadManifest(*config)
		if err != nil {
			return err
		}
		if err = importer.Verify(*bundle, manifest.BundleSHA256); err != nil {
			return err
		}
		file, err := os.Open(*bundle)
		if err != nil {
			return err
		}
		defer file.Close()
		if err = placeduckdb.BuildJSON(ctx, *candidate, file); err != nil {
			return err
		}
		fmt.Println("Built", *candidate)
	case "compare":
		if *candidate == "" {
			return fmt.Errorf("compare requires -candidate")
		}
		if *baseline == "" {
			selection, err := placeduckdb.ReadSelection(*selectionPath)
			if err != nil {
				return err
			}
			*baseline = selection.Current.Path
		}
		checks := importer.NewportPlacesQueryChecks()
		if *queries != "" {
			if err := readJSON(*queries, &checks); err != nil {
				return err
			}
		}
		report, err := placeduckdb.Compare(ctx, *baseline, *candidate, checks)
		if err != nil {
			return err
		}
		if err = importer.WriteJSON(*reportPath, report); err != nil {
			return err
		}
		sum, err := importer.Checksum(*reportPath)
		if err != nil {
			return err
		}
		summary := map[string]any{"report": *reportPath, "report_sha256": sum, "before": report.BeforeCounts, "after": report.AfterCounts, "added": report.Added, "removed": report.Removed, "changed": report.Changed, "relationships_added": report.RelationshipsAdded, "relationships_removed": report.RelationshipsRemoved, "violations": report.Violations}
		out, _ := json.MarshalIndent(summary, "", "  ")
		fmt.Println(string(out))
		if len(report.Violations) != 0 {
			return fmt.Errorf("candidate failed validation; see report")
		}
	case "review":
		if *reviewPath == "" || strings.TrimSpace(*reviewer) == "" || strings.TrimSpace(*reason) == "" {
			return fmt.Errorf("review requires -review, -reviewer and -reason after inspecting the report")
		}
		var report placeduckdb.Comparison
		if err := readJSON(*reportPath, &report); err != nil {
			return err
		}
		if report.Schema != 1 || len(report.Violations) != 0 || len(report.Queries) == 0 {
			return fmt.Errorf("cannot review a failed or unchecked candidate")
		}
		sum, err := importer.Checksum(*reportPath)
		if err != nil {
			return err
		}
		return importer.WriteJSON(*reviewPath, review{ReportSHA256: sum, Reviewer: *reviewer, Reason: *reason})
	case "init":
		if *baseline == "" {
			return fmt.Errorf("init requires -baseline")
		}
		return placeduckdb.InitializeSelection(*selectionPath, *baseline)
	case "activate":
		if *candidate == "" || *reviewPath == "" {
			return fmt.Errorf("activate requires -candidate and -review")
		}
		var report placeduckdb.Comparison
		if err := readJSON(*reportPath, &report); err != nil {
			return err
		}
		var approved review
		if err := readJSON(*reviewPath, &approved); err != nil {
			return err
		}
		if approved.Reviewer == "" || approved.Reason == "" || importer.Verify(*reportPath, approved.ReportSHA256) != nil || report.Schema != 1 || len(report.Violations) != 0 || len(report.Queries) == 0 {
			return fmt.Errorf("comparison is not validly reviewed")
		}
		selection, err := placeduckdb.ReadSelection(*selectionPath)
		if err != nil {
			return err
		}
		candidateReference, err := placeduckdb.Describe(*candidate)
		if err != nil {
			return err
		}
		if selection.Current != report.Baseline || candidateReference != report.Candidate {
			return fmt.Errorf("reviewed comparison does not match current and candidate generations")
		}
		return placeduckdb.ActivateSelection(*selectionPath, *candidate)
	case "rollback":
		return placeduckdb.RollbackSelection(*selectionPath)
	case "status":
		selection, err := placeduckdb.ReadSelection(*selectionPath)
		if err != nil {
			return err
		}
		out, _ := json.MarshalIndent(selection, "", "  ")
		fmt.Println(string(out))
	default:
		return fmt.Errorf("unknown places-geocoding-refresh command %q", command)
	}
	return nil
}
