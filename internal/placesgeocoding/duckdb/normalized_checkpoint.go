package duckdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/parquet-go/parquet-go"

	"openmaps/internal/importer"
)

const normalizedCheckpointName = "normalized-checkpoint.json"

type normalizedCheckpointIdentity struct {
	ManifestSHA256     string `json:"manifest_sha256"`
	IdentitiesSHA256   string `json:"identities_sha256"`
	MemoryLimit        string `json:"memory_limit"`
	DatabaseThreads    int    `json:"database_threads"`
	ExpectedDataSHA256 string `json:"expected_data_sha256,omitempty"`
	BuildIdentity      string `json:"build_identity"`
}

type normalizedCheckpoint struct {
	Schema   int                          `json:"schema"`
	Identity normalizedCheckpointIdentity `json:"identity"`
	Artifact Manifest                     `json:"artifact"`
}

// NormalizedResumeOptions identifies immutable normalized Parquet independently
// of catalog execution controls. Catalog thread and memory settings may vary
// between benchmarks without weakening the normalized-data identity check.
type NormalizedResumeOptions struct {
	Path               string
	Manifest           json.RawMessage
	Identities         map[string]string
	MemoryLimit        string
	DatabaseThreads    int
	ExpectedDataSHA256 string
	BuildIdentity      string
}

func makeNormalizedIdentity(options NormalizedResumeOptions) (normalizedCheckpointIdentity, error) {
	var compact bytes.Buffer
	if len(options.Manifest) == 0 || json.Compact(&compact, options.Manifest) != nil {
		return normalizedCheckpointIdentity{}, fmt.Errorf("normalized checkpoint requires a valid source manifest")
	}
	identities, err := json.Marshal(options.Identities)
	if err != nil {
		return normalizedCheckpointIdentity{}, err
	}
	manifestDigest := sha256.Sum256(compact.Bytes())
	identityDigest := sha256.Sum256(identities)
	return normalizedCheckpointIdentity{
		ManifestSHA256: fmt.Sprintf("%x", manifestDigest), IdentitiesSHA256: fmt.Sprintf("%x", identityDigest),
		MemoryLimit: options.MemoryLimit, DatabaseThreads: options.DatabaseThreads,
		ExpectedDataSHA256: options.ExpectedDataSHA256, BuildIdentity: options.BuildIdentity,
	}, nil
}

func publishNormalizedCheckpoint(path, generation string, artifact Manifest, identity normalizedCheckpointIdentity) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if filepath.Dir(abs) != filepath.Dir(generation) {
		return fmt.Errorf("normalized checkpoint and candidate must be sibling paths")
	}
	building := abs + ".building"
	for _, reserved := range []string{abs, building} {
		if _, err = os.Stat(reserved); err == nil {
			return fmt.Errorf("normalized checkpoint path exists: %s", reserved)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if err = os.Mkdir(building, 0700); err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(building)
		}
	}()
	for _, file := range artifact.Files {
		if file.Role == "serving" {
			return fmt.Errorf("normalized checkpoint cannot contain a serving catalog")
		}
		if err = os.Link(filepath.Join(generation, file.Name), filepath.Join(building, file.Name)); err != nil {
			return fmt.Errorf("link normalized artifact %s: %w", file.Name, err)
		}
	}
	marker := normalizedCheckpoint{Schema: 1, Identity: identity, Artifact: artifact}
	if err = importer.WriteJSON(filepath.Join(building, normalizedCheckpointName), marker); err != nil {
		return err
	}
	if err = os.Rename(building, abs); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func readNormalizedCheckpoint(path string, expected normalizedCheckpointIdentity) (normalizedCheckpoint, error) {
	var checkpoint normalizedCheckpoint
	raw, err := os.ReadFile(filepath.Join(path, normalizedCheckpointName))
	if err != nil {
		return checkpoint, fmt.Errorf("read normalized checkpoint: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&checkpoint); err != nil {
		return checkpoint, fmt.Errorf("decode normalized checkpoint: %w", err)
	}
	if trailing := decoder.Decode(&struct{}{}); trailing != io.EOF {
		return checkpoint, fmt.Errorf("decode normalized checkpoint: trailing data")
	}
	if checkpoint.Schema != 1 || !reflect.DeepEqual(checkpoint.Identity, expected) {
		return checkpoint, fmt.Errorf("normalized checkpoint identity does not match this build")
	}
	if checkpoint.Artifact.Schema != 2 || checkpoint.Artifact.CoordinateOrder != "longitude,latitude" || len(checkpoint.Artifact.Files) < 6 {
		return checkpoint, fmt.Errorf("invalid normalized checkpoint artifact")
	}
	if expected.ExpectedDataSHA256 != "" && checkpoint.Artifact.DataSHA256 != expected.ExpectedDataSHA256 {
		return checkpoint, fmt.Errorf("normalized checkpoint retained-data checksum mismatch")
	}
	for _, file := range checkpoint.Artifact.Files {
		if file.Role == "serving" || filepath.Base(file.Name) != file.Name {
			return checkpoint, fmt.Errorf("invalid normalized checkpoint file %s", file.Name)
		}
		filePath := filepath.Join(path, file.Name)
		if err = importer.Verify(filePath, file.SHA256); err != nil {
			return checkpoint, fmt.Errorf("verify normalized checkpoint %s: %w", file.Name, err)
		}
		if filepath.Ext(file.Name) == ".parquet" {
			f, openErr := os.Open(filePath)
			if openErr != nil {
				return checkpoint, openErr
			}
			stat, statErr := f.Stat()
			if statErr != nil {
				f.Close()
				return checkpoint, statErr
			}
			parquetFile, parquetErr := parquet.OpenFile(f, stat.Size())
			f.Close()
			if parquetErr != nil || parquetFile.NumRows() != int64(file.Rows) {
				return checkpoint, fmt.Errorf("invalid normalized checkpoint row count %s: %v", file.Name, parquetErr)
			}
		}
	}
	return checkpoint, nil
}

func cloneNormalizedCheckpoint(checkpointPath, generation string, checkpoint normalizedCheckpoint) error {
	if err := os.Mkdir(generation, 0700); err != nil {
		return err
	}
	for _, file := range checkpoint.Artifact.Files {
		if err := os.Link(filepath.Join(checkpointPath, file.Name), filepath.Join(generation, file.Name)); err != nil {
			return fmt.Errorf("link normalized checkpoint file %s: %w", file.Name, err)
		}
	}
	return nil
}

// BuildCatalogFromNormalized constructs and verifies a new generation without
// reopening the input-staging database or rerunning normalization.
func BuildCatalogFromNormalized(ctx context.Context, output string, normalized NormalizedResumeOptions, catalogMemoryLimit string, catalogThreads int, observe func(string, time.Duration)) (err error) {
	if normalized.Path == "" || normalized.BuildIdentity == "" || catalogMemoryLimit == "" || catalogThreads < 1 || catalogThreads > 64 {
		return fmt.Errorf("normalized path, exact build identity, and valid catalog controls are required")
	}
	absOutput, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	absCheckpoint, err := filepath.Abs(normalized.Path)
	if err != nil {
		return err
	}
	if filepath.Dir(absOutput) != filepath.Dir(absCheckpoint) {
		return fmt.Errorf("normalized checkpoint and output must be sibling paths")
	}
	if _, err = os.Stat(absOutput); err == nil {
		return fmt.Errorf("output exists; choose a new artifact directory")
	} else if !os.IsNotExist(err) {
		return err
	}
	identity, err := makeNormalizedIdentity(normalized)
	if err != nil {
		return err
	}
	checkpoint, err := readNormalizedCheckpoint(absCheckpoint, identity)
	if err != nil {
		return err
	}
	generation, err := os.MkdirTemp(filepath.Dir(absOutput), ".duckdb-catalog-generation-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(generation) }()
	if err = os.Remove(generation); err != nil {
		return err
	}
	if err = cloneNormalizedCheckpoint(absCheckpoint, generation, checkpoint); err != nil {
		return err
	}
	var identitiesJSON string
	if encoded, marshalErr := json.Marshal(normalized.Identities); marshalErr != nil {
		return marshalErr
	} else {
		identitiesJSON = string(encoded)
	}
	entityRows := 0
	for _, file := range checkpoint.Artifact.Files {
		if file.Role == "entities" {
			entityRows += file.Rows
		}
	}
	started := time.Now()
	if err = buildIndexJSONWithOptionsObserved(ctx, filepath.Join(generation, IndexName), filepath.Join(generation, "entities*.parquet"), entityRows, normalized.Manifest, identitiesJSON, catalogMemoryLimit, catalogThreads, shardedSchema, observe); err != nil {
		return fmt.Errorf("build serving catalog: %w", err)
	}
	indexDigest, err := importer.Checksum(filepath.Join(generation, IndexName))
	if err != nil {
		return err
	}
	artifact := checkpoint.Artifact
	artifact.Files = append(append([]File(nil), artifact.Files...), File{Name: IndexName, Role: "serving", SHA256: indexDigest, Rows: entityRows})
	if observe != nil {
		observe("catalog", time.Since(started))
	}
	if err = importer.WriteJSON(filepath.Join(generation, ManifestName), artifact); err != nil {
		return err
	}
	verifyStarted := time.Now()
	if _, err = VerifyObserved(generation, observe); err != nil {
		return err
	}
	if observe != nil {
		observe("validation", time.Since(verifyStarted))
	}
	return os.Rename(generation, absOutput)
}
