package routing

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
)

type landmarkValues [maxLandmarks][2]uint32
type landmarkTables struct {
	dense       *denseNodes
	vectors     [][2]*tile
	fingerprint string
}

// EnableLandmarks loads graph-bound, checksummed lower-bound vectors. Each
// direction has a separate 8 MiB page cache; vectors are never decoded globally.
func (s *Router) EnableLandmarks(ctx context.Context, dir, landmarkDir string) error {
	return s.EnableLandmarksWithCache(ctx, dir, landmarkDir, 8<<20)
}

// EnableLandmarksWithCache bounds each direction independently. The largest
// admitted sixteen-pair configuration retains at most 1 GiB of vector pages.
func (s *Router) EnableLandmarksWithCache(ctx context.Context, dir, landmarkDir string, cacheBytes int64) error {
	if cacheBytes < pageSize || cacheBytes > 32<<20 {
		return errors.New("landmark cache must be 64 KiB..32 MiB per vector")
	}
	if s.landmarks != nil {
		return errors.New("landmarks already loaded")
	}
	b, err := readBoundedFile(filepath.Join(landmarkDir, "landmarks.json"), 1<<20)
	if err != nil {
		return err
	}
	var manifest landmarkManifest
	if err := json.Unmarshal(b, &manifest); err != nil {
		return err
	}
	graph, err := readBoundedFile(filepath.Join(dir, "receipt.json"), maxPreparedIndex)
	if err != nil {
		return err
	}
	if manifest.Schema != landmarkSchema || manifest.GraphReceiptSHA256 != hexSum(graph) || manifest.GraphReceiptSHA256 != s.Reader.preparedSHA || manifest.Profile != Profile || len(manifest.Entries) < 1 || len(manifest.Entries) > maxLandmarks {
		return errors.New("foreign or unsupported landmark publication")
	}
	if manifest.ReindexedFrom != "" || manifest.ExtensionSHA256 != "" {
		proof, err := readBoundedFile(filepath.Join(landmarkDir, "extension.json"), 65536)
		if err != nil {
			return err
		}
		var p landmarkExtensionProof
		if checkDigest(manifest.ReindexedFrom) != nil || checkDigest(manifest.ExtensionSHA256) != nil || hexSum(proof) != manifest.ExtensionSHA256 || json.Unmarshal(proof, &p) != nil || p.Schema != "openmaps-scout-landmark-extension-v1" || p.OldLandmarks != manifest.ReindexedFrom || p.NewGraph != manifest.GraphReceiptSHA256 || p.NewNodes != manifest.NodeCount || p.OldNodes > p.NewNodes || checkDigest(p.OldGraph) != nil {
			return errors.New("invalid landmark extension certificate")
		}
	}
	dense, err := newDenseNodes(s.Reader)
	if err != nil {
		return err
	}
	if manifest.NodeCount != dense.count {
		return errors.New("landmark node order/count mismatch")
	}
	tables := &landmarkTables{dense: dense, fingerprint: hexSum(b)}
	var readers []*Reader
	ok := false
	defer func() {
		if !ok {
			for _, r := range readers {
				r.Close()
			}
		}
	}()
	for _, entry := range manifest.Entries {
		var pair [2]*tile
		if entry.Forward.SeedNode != entry.Reverse.SeedNode {
			return errors.New("landmark directions use different seeds")
		}
		for i, pin := range []landmarkVector{entry.Forward, entry.Reverse} {
			if filepath.Base(pin.File) != pin.File || pin.Reverse != (i == 1) || checkDigest(pin.SHA256) != nil || pin.Bytes != (64+int64(dense.count)*4+pageSize-1)/pageSize*pageSize {
				return errors.New("invalid landmark vector metadata")
			}
			if _, err := dense.index(pin.SeedNode); err != nil {
				return err
			}
			f, err := os.Open(filepath.Join(landmarkDir, pin.File))
			if err != nil {
				return err
			}
			r := &Reader{f: f, pages: map[int64]*list.Element{}, limit: cacheBytes, recordPageReuse: true}
			readers = append(readers, r)
			st, err := f.Stat()
			if err != nil {
				return err
			}
			if st.Size() != pin.Bytes {
				return errors.New("landmark vector extent mismatch")
			}
			hash := sha256.New()
			if _, err := io.Copy(hash, contextReader{ctx, f}); err != nil {
				return err
			}
			if hex.EncodeToString(hash.Sum(nil)) != pin.SHA256 {
				return errors.New("landmark payload checksum mismatch")
			}
			t := &tile{reader: r, size: int(pin.Bytes)}
			header, err := t.span(0, 64)
			if err != nil {
				return err
			}
			if string(bytes.TrimRight(header[:32], "\x00")) != landmarkSchema || u32(header, 32) != dense.count || ID(u64(header, 40)) != pin.SeedNode || header[48] != uint8(i) {
				return errors.New("landmark header mismatch")
			}
			pair[i] = t
		}
		tables.vectors = append(tables.vectors, pair)
	}
	s.Reader.landmarkReaders = readers
	s.landmarks = tables
	ok = true
	return nil
}
func readBoundedFile(path string, limit int64) ([]byte, error) {
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
		return nil, errors.New("metadata byte budget exceeded")
	}
	return b, nil
}
func (l *landmarkTables) values(id ID) (landmarkValues, error) {
	var out landmarkValues
	index, err := l.dense.index(id)
	if err != nil {
		return out, err
	}
	for i, pair := range l.vectors {
		for j, t := range pair {
			b, err := t.span(64+int(index)*4, 4)
			if err != nil {
				return out, err
			}
			v := u32(b, 0)
			if v > 0x7f800000 {
				return out, errors.New("invalid negative/NaN landmark cost")
			}
			out[i][j] = v
		}
	}
	return out, nil
}
func (l *landmarkTables) bound(from, to landmarkValues) float64 {
	value := 0.0
	for i := range l.vectors {
		// Quantized costs are lower endpoints of float32 intervals. Subtracting
		// an upper endpoint from a lower endpoint cannot overstate the difference.
		if from[i][0] != 0x7f800000 && to[i][0] != 0x7f800000 {
			lo := float64(math.Float32frombits(to[i][0]))
			hi := float64(math.Nextafter32(math.Float32frombits(from[i][0]), float32(math.Inf(1))))
			value = math.Max(value, lo-hi)
		}
		if from[i][1] != 0x7f800000 && to[i][1] != 0x7f800000 {
			lo := float64(math.Float32frombits(from[i][1]))
			hi := float64(math.Nextafter32(math.Float32frombits(to[i][1]), float32(math.Inf(1))))
			value = math.Max(value, lo-hi)
		}
	}
	return value * (1 - 1e-10)
}
