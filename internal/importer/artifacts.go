package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Input pins a concrete source export or original download. Bounds use WGS84
// longitude,latitude. Catalog pins select Overture assets, never "latest".
type Input struct {
	File                string    `json:"file"`
	SHA256              string    `json:"sha256"`
	AssetsSHA256        string    `json:"assets_sha256,omitempty"`
	AssetVersionsSHA256 string    `json:"asset_versions_sha256,omitempty"`
	Release             string    `json:"release"`
	URL                 string    `json:"url"`
	BBox                []float64 `json:"bbox,omitempty"`
	Attribution         string    `json:"attribution"`
}
type Scope struct {
	Name    string     `json:"name"`
	BBox    [4]float64 `json:"bbox"`
	Regions []string   `json:"regions,omitempty"`
}
type Streaming struct {
	BatchRows            int    `json:"batch_rows"`
	MemoryLimit          string `json:"memory_limit"`
	CatalogMemoryLimit   string `json:"catalog_memory_limit"`
	FreeDiskFloorGiB     int64  `json:"free_disk_floor_gib"`
	GoMemoryLimitMiB     int64  `json:"go_memory_limit_mib"`
	EstimatedBytesPerRow int64  `json:"estimated_peak_bytes_per_candidate_row"`
}
type Manifest struct {
	Schema          int        `json:"schema"`
	Region          string     `json:"region"`
	BBox            [4]float64 `json:"bbox"`
	CoordinateOrder string     `json:"coordinate_order"`
	BundleSHA256    string     `json:"bundle_sha256,omitempty"`
	Inputs          []Input    `json:"inputs"`
	Catalog         *Input     `json:"catalog,omitempty"`
	Scopes          []Scope    `json:"scopes,omitempty"`
	Streaming       *Streaming `json:"streaming,omitempty"`
}

func ReadManifest(path string) (Manifest, error) {
	var m Manifest
	b, e := os.ReadFile(path)
	if e != nil {
		return m, e
	}
	e = json.Unmarshal(b, &m)
	if e != nil {
		return m, e
	}
	if (m.Schema != 1 && m.Schema != 2) || m.Region == "" || m.CoordinateOrder != "longitude,latitude" || !validBounds(m.BBox) {
		return m, fmt.Errorf("invalid Places/geocoding manifest")
	}
	if m.Schema == 1 && (len(m.BundleSHA256) != 64 || !validDigest(m.BundleSHA256)) {
		return m, fmt.Errorf("invalid normalized bundle SHA-256")
	}
	if m.Schema == 2 {
		if m.BundleSHA256 != "" || m.Catalog == nil || len(m.Scopes) == 0 || m.Streaming == nil || m.Streaming.BatchRows < 1 || m.Streaming.BatchRows > 32768 || m.Streaming.MemoryLimit == "" || m.Streaming.CatalogMemoryLimit == "" || m.Streaming.FreeDiskFloorGiB < 60 || m.Streaming.GoMemoryLimitMiB < 512 || m.Streaming.GoMemoryLimitMiB > 2048 || m.Streaming.EstimatedBytesPerRow < 1 {
			return m, fmt.Errorf("invalid streaming manifest")
		}
		for index, scope := range m.Scopes {
			if scope.Name == "" || !validBounds(scope.BBox) {
				return m, fmt.Errorf("invalid streaming scope")
			}
			if scope.BBox[0] < m.BBox[0] || scope.BBox[1] < m.BBox[1] || scope.BBox[2] > m.BBox[2] || scope.BBox[3] > m.BBox[3] {
				return m, fmt.Errorf("streaming scope outside manifest bounds")
			}
			for prior := 0; prior < index; prior++ {
				other := m.Scopes[prior].BBox
				if scope.BBox[0] < other[2] && scope.BBox[2] > other[0] && scope.BBox[1] < other[3] && scope.BBox[3] > other[1] {
					return m, fmt.Errorf("streaming scopes overlap")
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, i := range append(append([]Input{}, m.Inputs...), catalogInputs(m)...) {
		if i.File == "" || filepath.Base(i.File) != i.File || seen[i.File] || i.Release == "" || i.URL == "" || i.Attribution == "" {
			return m, fmt.Errorf("invalid or duplicate input: %s", i.File)
		}
		isCatalog := m.Catalog != nil && i.File == m.Catalog.File
		if m.Schema == 1 || isCatalog {
			if !validDigest(i.SHA256) {
				return m, fmt.Errorf("invalid SHA-256: %s", i.File)
			}
		} else if i.SHA256 != "" {
			return m, fmt.Errorf("streaming source %s pins assets, not a regional file", i.File)
		}
		if m.Schema == 2 && !isCatalog && !validDigest(i.AssetsSHA256) {
			return m, fmt.Errorf("invalid asset-set SHA-256: %s", i.File)
		}
		if m.Schema == 2 && !isCatalog && !validDigest(i.AssetVersionsSHA256) {
			return m, fmt.Errorf("invalid asset-version SHA-256: %s", i.File)
		}
		if len(i.BBox) != 0 && (len(i.BBox) != 4 || !validBounds([4]float64(i.BBox))) {
			return m, fmt.Errorf("invalid input bounds")
		}
		seen[i.File] = true
	}
	return m, nil
}
func validDigest(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil && len(value) == 64
}
func catalogInputs(m Manifest) []Input {
	if m.Catalog != nil {
		return []Input{*m.Catalog}
	}
	return nil
}
func validBounds(b [4]float64) bool {
	return b[0] >= -180 && b[2] <= 180 && b[1] >= -90 && b[3] <= 90 && b[0] < b[2] && b[1] < b[3]
}
func inside(p [2]float64, b [4]float64) bool {
	return p[0] >= b[0] && p[0] <= b[2] && p[1] >= b[1] && p[1] <= b[3]
}
func Checksum(path string) (string, error) {
	f, e := os.Open(path)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func Verify(path, expected string) error {
	got, e := Checksum(path)
	if e != nil {
		return e
	}
	if got != strings.TrimSpace(expected) {
		return fmt.Errorf("checksum mismatch: %s (got %s)", path, got)
	}
	return nil
}
func WriteJSON(path string, v any) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return writeAtomic(path, append(b, '\n'))
}
func writeAtomic(path string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".openmaps-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(b); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	return os.Rename(f.Name(), path)
}

var downloadClient = &http.Client{Timeout: 2 * time.Minute}

func download(ctx context.Context, url, path, expected string) error {
	if _, e := os.Stat(path); e == nil {
		return Verify(path, expected)
	} else if !os.IsNotExist(e) {
		return e
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".download-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	defer f.Close()
	req, e := http.NewRequestWithContext(ctx, "GET", url, nil)
	if e != nil {
		return e
	}
	resp, e := downloadClient.Do(req)
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	if _, e = io.Copy(f, resp.Body); e != nil {
		return e
	}
	if e = f.Sync(); e != nil {
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = Verify(f.Name(), expected); e != nil {
		return e
	}
	// Never clobber a concurrently published or pre-existing source artifact.
	return os.Link(f.Name(), path)
}
