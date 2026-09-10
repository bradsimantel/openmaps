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
	File        string    `json:"file"`
	SHA256      string    `json:"sha256"`
	Release     string    `json:"release"`
	URL         string    `json:"url"`
	BBox        []float64 `json:"bbox,omitempty"`
	Attribution string    `json:"attribution"`
}
type Manifest struct {
	Schema          int        `json:"schema"`
	Region          string     `json:"region"`
	BBox            [4]float64 `json:"bbox"`
	CoordinateOrder string     `json:"coordinate_order"`
	Inputs          []Input    `json:"inputs"`
	Catalog         *Input     `json:"catalog,omitempty"`
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
	if m.Schema != 1 || m.Region == "" || m.CoordinateOrder != "longitude,latitude" || !validBounds(m.BBox) {
		return m, fmt.Errorf("invalid regional manifest")
	}
	seen := map[string]bool{}
	for _, i := range append(append([]Input{}, m.Inputs...), catalogInputs(m)...) {
		if i.File == "" || filepath.Base(i.File) != i.File || seen[i.File] || i.Release == "" || i.URL == "" || i.Attribution == "" {
			return m, fmt.Errorf("invalid or duplicate input: %s", i.File)
		}
		if _, e := hex.DecodeString(i.SHA256); e != nil || len(i.SHA256) != 64 {
			return m, fmt.Errorf("invalid SHA-256: %s", i.File)
		}
		if len(i.BBox) != 0 && (len(i.BBox) != 4 || !validBounds([4]float64(i.BBox))) {
			return m, fmt.Errorf("invalid input bounds")
		}
		seen[i.File] = true
	}
	return m, nil
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
