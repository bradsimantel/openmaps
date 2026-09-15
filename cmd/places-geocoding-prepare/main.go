// places-geocoding-prepare owns regional acquisition, provider decoding,
// normalization and audit for the shared Places/geocoding inputs.
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
	"runtime"
	"runtime/debug"
	"syscall"
	"time"

	"openmaps/internal/importer"
	placeduckdb "openmaps/internal/placesgeocoding/duckdb"
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
	acceptSourceUpdate := flag.Bool("accept-reviewed-source-update", false, "maintainer operation: replace reviewed source exports and accept their checksums")
	acceptNormalizationUpdate := flag.Bool("accept-reviewed-normalization-update", false, "maintainer operation: accept a reviewed normalized-output checksum change without changing source pins")
	preflight := flag.Bool("preflight", false, "inspect pinned Parquet assets and estimate bounded-build workspace use")
	streamOut := flag.String("stream-out", "", "build a normalized Parquet/DuckDB generation directly from pinned Parquet (schema 2 configs)")
	auditPath := flag.String("audit", "", "optional new streaming audit JSON path")
	checkpointPath := flag.String("checkpoint", "", "durable input-staging checkpoint directory for a streaming build")
	resume := flag.Bool("resume", false, "resume normalization from the completed input-staging checkpoint")
	flag.Parse()
	if *acceptSourceUpdate && !*fetch {
		return fmt.Errorf("-accept-reviewed-source-update requires -fetch")
	}
	if *resume && *checkpointPath == "" {
		return fmt.Errorf("-resume requires -checkpoint")
	}
	if *checkpointPath != "" && *streamOut == "" {
		return fmt.Errorf("-checkpoint requires -stream-out")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	m, e := importer.ReadManifest(*config)
	if e != nil {
		return e
	}
	if e = os.MkdirAll(*data, 0755); e != nil {
		return e
	}
	if *fetch {
		m, e = importer.FetchSources(ctx, m, *data, *acceptSourceUpdate)
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
	if m.Schema == 2 {
		if *acceptSourceUpdate || *acceptNormalizationUpdate {
			return fmt.Errorf("reviewed checksum updates are not supported by streaming configs")
		}
		preflightResult, err := importer.PreflightStreaming(ctx, m, *data)
		if err != nil {
			return err
		}
		dataFree, err := freeDisk(*data)
		if err != nil {
			return err
		}
		floor := m.Streaming.FreeDiskFloorGiB << 30
		outputFree := dataFree
		outputParent := *data
		if *streamOut != "" {
			output, absErr := filepath.Abs(*streamOut)
			if absErr != nil {
				return absErr
			}
			outputParent = filepath.Dir(output)
			if err = os.MkdirAll(outputParent, 0755); err != nil {
				return err
			}
			if outputFree, err = freeDisk(outputParent); err != nil {
				return err
			}
			if err = requireDiskHeadroom("preparation", dataFree, preflightResult.EstimatedPeakWorkspaceBytes, floor); err != nil {
				return err
			}
			if err = requireDiskHeadroom("output", outputFree, preflightResult.EstimatedPeakWorkspaceBytes, floor); err != nil {
				return err
			}
		}
		if *preflight || *streamOut == "" {
			out, _ := json.MarshalIndent(map[string]any{"preflight": preflightResult, "free_disk_bytes": dataFree, "preparation_free_disk_bytes": dataFree, "output_free_disk_bytes": outputFree, "output_parent": outputParent, "free_disk_floor_bytes": floor}, "", "  ")
			fmt.Println(string(out))
		}
		if *streamOut == "" {
			return nil
		}
		if *auditPath != "" {
			if _, err = os.Stat(*auditPath); err == nil {
				return fmt.Errorf("audit output exists: %s", *auditPath)
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		manifestRaw, err := json.Marshal(m)
		if err != nil {
			return err
		}
		var audit importer.StreamingAudit
		oldMemoryLimit := debug.SetMemoryLimit(m.Streaming.GoMemoryLimitMiB << 20)
		defer debug.SetMemoryLimit(oldMemoryLimit)
		buildIdentity := ""
		if *checkpointPath != "" {
			buildIdentity, err = cleanBuildIdentity()
			if err != nil {
				return err
			}
		}
		phases := map[string]float64{}
		started := time.Now()
		checkpointState, err := placeduckdb.BuildStreamResumable(ctx, *streamOut, manifestRaw, ids, m.Streaming.MemoryLimit, m.Streaming.CatalogMemoryLimit, m.Streaming.DatabaseThreads, m.Streaming.CatalogThreads, m.Streaming.ExpectedDataSHA256, placeduckdb.StreamCheckpointOptions{Path: *checkpointPath, Resume: *resume, BuildIdentity: buildIdentity}, func(name string, elapsed time.Duration) {
			phases[name] = elapsed.Seconds()
			log.Printf("Places/geocoding build phase complete phase=%s elapsed=%s", name, elapsed.Round(time.Second))
		}, func(buildCtx context.Context, sink placeduckdb.StreamWriter) (json.RawMessage, error) {
			var prepareErr error
			audit, prepareErr = importer.PrepareStreaming(buildCtx, m, *data, sink)
			if prepareErr != nil {
				return nil, prepareErr
			}
			state, marshalErr := json.Marshal(audit)
			return state, marshalErr
		})
		if err != nil {
			return err
		}
		if err = json.Unmarshal(checkpointState, &audit); err != nil {
			return fmt.Errorf("decode checkpointed preparation audit: %w", err)
		}
		if audit.Region != m.Region || audit.BatchRows != m.Streaming.BatchRows {
			return fmt.Errorf("checkpointed preparation audit does not match the streaming manifest")
		}
		artifact, err := placeduckdb.Verify(*streamOut)
		if err != nil {
			return err
		}
		phases["total"] = time.Since(started).Seconds()
		report := map[string]any{"preflight": preflightResult, "preparation": audit, "build_phase_seconds": phases, "artifact": artifact, "free_disk_before": dataFree, "preparation_free_disk_before": dataFree, "output_free_disk_before": outputFree, "output_parent": outputParent}
		if *auditPath != "" {
			if err = importer.WriteJSON(*auditPath, report); err != nil {
				return err
			}
		}
		out, _ := json.MarshalIndent(report, "", "  ")
		fmt.Println(string(out))
		return nil
	}
	if *preflight || *streamOut != "" || *auditPath != "" {
		return fmt.Errorf("streaming flags require a schema 2 config")
	}
	expected := m.BundleSHA256
	m.BundleSHA256 = ""
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
	if !*acceptSourceUpdate && !*acceptNormalizationUpdate {
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
	if *acceptSourceUpdate || *acceptNormalizationUpdate {
		m.BundleSHA256 = digest
		if e = importer.WriteJSON(*config, m); e != nil {
			return e
		}
	}
	out, _ := json.MarshalIndent(audit, "", "  ")
	fmt.Println(string(out))
	return nil
}

func cleanBuildIdentity() (string, error) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", fmt.Errorf("checkpointing requires embedded Go build information")
	}
	revision, modified := "", ""
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	if revision == "" {
		return "", fmt.Errorf("checkpointing requires an embedded VCS revision")
	}
	if modified != "false" {
		return "", fmt.Errorf("checkpointing requires a clean VCS build")
	}
	return fmt.Sprintf("revision=%s;target=%s/%s", revision, runtime.GOOS, runtime.GOARCH), nil
}

func freeDisk(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

func requireDiskHeadroom(volume string, free, estimate, floor int64) error {
	if free < estimate || free-estimate < floor {
		return fmt.Errorf("estimated build would cross %s-volume free-disk floor: free=%d estimate=%d floor=%d", volume, free, estimate, floor)
	}
	return nil
}
