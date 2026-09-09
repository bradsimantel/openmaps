package valhallatiles

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// ScoutLock pins complete package bytes and every uncompressed road tile. Other
// provenance fields in the JSON lock are documentary; these fields are enforced.
type ScoutLock struct {
	Schema        int            `json:"schema"`
	PackageSchema string         `json:"package_schema"`
	Version       string         `json:"tile_version"`
	Dataset       uint64         `json:"dataset_id"`
	Timestamp     string         `json:"timestamp"`
	Packages      []ScoutPackage `json:"packages"`
}
type ScoutPackage struct {
	ID     string      `json:"id"`
	Bytes  int64       `json:"bytes"`
	SHA256 string      `json:"sha256"`
	Tiles  []ScoutTile `json:"tiles"`
}
type ScoutTile struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(b)
}

func checkDigest(digest string) error {
	b, err := hex.DecodeString(digest)
	if err != nil || len(b) != sha256.Size {
		return errors.New("pinned SHA-256 required")
	}
	return nil
}

func scoutID(name string) (ID, error) {
	if !strings.HasPrefix(name, "valhalla/tiles/") || !strings.HasSuffix(name, ".gph.gz") {
		return 0, errors.New("unsupported Scout tile path")
	}
	parts := strings.Split(strings.TrimSuffix(strings.TrimPrefix(name, "valhalla/tiles/"), ".gph.gz"), "/")
	if len(parts) < 2 || len(parts[0]) != 1 || parts[0][0] < '0' || parts[0][0] > '2' {
		return 0, errors.New("only road tile paths supported")
	}
	for _, part := range parts[1:] {
		if len(part) != 3 {
			return 0, errors.New("invalid tile path component")
		}
		for _, c := range part {
			if c < '0' || c > '9' {
				return 0, errors.New("invalid tile path digit")
			}
		}
	}
	n, err := strconv.ParseUint(strings.Join(parts[1:], ""), 10, 22)
	if err != nil {
		return 0, err
	}
	level := int(parts[0][0] - '0')
	if n >= uint64([...]int{4050, 64800, 1036800}[level]) {
		return 0, errors.New("tile outside world grid")
	}
	return ID(n<<3) | ID(level), nil
}

// OpenScout streams bzip2/tar/gzip into a private seekable temporary file, then
// serves records through 64 KiB pages. No archive path is extracted to disk and
// no graph-sized arrays are built. Close removes the spool. Keep inputs immutable.
// Caps: <100 MB compressed total, 256 MiB spool, 64 MiB/tile, 128 tiles,
// 512 tar members/package, and a 64 KiB..64 MiB page cache.
func OpenScout(ctx context.Context, dir, scratch string, lock ScoutLock, cacheBytes int64) (*Reader, error) {
	if lock.Schema != 1 || lock.PackageSchema != "2" || lock.Version != "3.4.0" || len(lock.Packages) == 0 || len(lock.Packages) > 8 || cacheBytes < pageSize || cacheBytes > 64<<20 {
		return nil, errors.New("unsupported Scout version/package/cache limits")
	}
	var compressed, expanded int64
	packageIDs := map[string]bool{}
	tileIDs := map[ID]bool{}
	for _, p := range lock.Packages {
		if p.ID == "" || strings.IndexFunc(p.ID, func(c rune) bool { return c < '0' || c > '9' }) >= 0 || packageIDs[p.ID] || p.Bytes <= 0 || checkDigest(p.SHA256) != nil || len(p.Tiles) == 0 {
			return nil, errors.New("invalid or duplicate package pin")
		}
		packageIDs[p.ID] = true
		if p.Bytes >= 100000000-compressed {
			return nil, errors.New("compressed download budget exceeded")
		}
		compressed += p.Bytes
		for _, pin := range p.Tiles {
			id, err := scoutID(pin.Name)
			if err != nil || tileIDs[id] || pin.Bytes < 272 || pin.Bytes > 64<<20 || checkDigest(pin.SHA256) != nil {
				return nil, errors.New("invalid or duplicate tile pin")
			}
			tileIDs[id] = true
			expanded += pin.Bytes
		}
	}
	if expanded > 256<<20 || len(tileIDs) > 128 {
		return nil, errors.New("expanded sample budget exceeded")
	}
	f, err := os.CreateTemp(scratch, "openmaps-scout-*.tiles")
	if err != nil {
		return nil, err
	}
	r := &Reader{f: f, version: lock.Version, removeOnClose: true, index: map[ID]entry{}, tiles: map[ID]*tile{}, packages: map[ID]string{}, pages: map[int64]*list.Element{}, limit: cacheBytes}
	ok := false
	defer func() {
		if !ok {
			r.Close()
		}
	}()
	for _, p := range lock.Packages {
		if err := r.unpackScout(ctx, filepath.Join(dir, p.ID+".tar.bz2"), p, lock); err != nil {
			return nil, fmt.Errorf("package %s: %w", p.ID, err)
		}
	}
	// Pad the final page so fixed ReadAt calls cannot read beyond the spool.
	size, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	if err := f.Truncate((size + pageSize - 1) / pageSize * pageSize); err != nil {
		return nil, err
	}
	ok = true
	return r, nil
}

func (r *Reader) unpackScout(ctx context.Context, path string, p ScoutPackage, lock ScoutLock) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if st.Size() != p.Bytes {
		return errors.New("package byte count mismatch")
	}
	h := sha256.New()
	if _, err := io.Copy(h, contextReader{ctx, f}); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != p.SHA256 {
		return errors.New("package SHA-256 mismatch")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	outer := &io.LimitedReader{R: bzip2.NewReader(contextReader{ctx, f}), N: 128<<20 + 1}
	tr := tar.NewReader(outer)
	pins := map[string]ScoutTile{}
	for _, pin := range p.Tiles {
		pins[pin.Name] = pin
	}
	seen := map[string]bool{}
	timestamp := false
	for count := 0; ; count++ {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if count >= 512 || hdr.Typeflag != tar.TypeReg || hdr.Size < 0 || hdr.Size > 64<<20 || seen[hdr.Name] {
			return errors.New("invalid/duplicate tar member or member cap exceeded")
		}
		seen[hdr.Name] = true
		pin, isTile := pins[hdr.Name]
		if !isTile {
			if hdr.Size > 64<<10 {
				return errors.New("oversized package metadata")
			}
			b, err := io.ReadAll(tr)
			if err != nil {
				return err
			}
			switch hdr.Name {
			case "valhalla/tiles/timestamp":
				if strings.TrimSpace(string(b)) != lock.Timestamp {
					return errors.New("mixed package timestamp")
				}
				timestamp = true
			case "valhalla/packages/" + p.ID + ".tar.list":
				listed := strings.Fields(string(b))
				if len(listed) != len(pins) {
					return errors.New("tile list mismatch")
				}
				listedSet := map[string]bool{}
				for _, name := range listed {
					if _, ok := pins[name]; !ok || listedSet[name] {
						return errors.New("tile list mismatch")
					}
					listedSet[name] = true
				}
			default:
				return fmt.Errorf("unpinned archive member %q", hdr.Name)
			}
			continue
		}
		offset, err := r.f.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
		if err := copyScoutTile(ctx, tr, r.f, pin); err != nil {
			return err
		}
		n := pin.Bytes
		header := make([]byte, 272)
		if _, err := r.f.ReadAt(header, offset); err != nil {
			return err
		}
		id, _ := scoutID(pin.Name)
		if string(bytes.TrimRight(header[16:32], "\x00")) != lock.Version || u64(header, 32) != lock.Dataset || u64(header, 88) != 0 {
			return errors.New("mixed version/dataset or nonzero 3.4.0 reserved checksum field")
		}
		t, err := parseTileHeader(header, id, int(n))
		if err != nil {
			return err
		}
		t.reader, t.offset = r, offset
		r.tiles[id], r.index[id], r.packages[id] = t, entry{offset, n}, p.ID
	}
	if !timestamp || !seen["valhalla/packages/"+p.ID+".tar.list"] {
		return errors.New("missing package metadata")
	}
	for name := range pins {
		if !seen[name] {
			return errors.New("missing pinned tile")
		}
	}
	// Finish bzip2 CRC validation, including bytes after tar's end markers.
	if _, err := io.Copy(io.Discard, outer); err != nil {
		return err
	}
	if outer.N <= 0 {
		return errors.New("outer decompression budget exceeded")
	}
	return nil
}

// copyScoutTile checks one gzip stream, its CRC, exact expanded extent and pin.
// The +1 read detects bombs without materializing their expanded payload.
func copyScoutTile(ctx context.Context, source io.Reader, out io.Writer, pin ScoutTile) error {
	if pin.Bytes < 272 || pin.Bytes > 64<<20 {
		return errors.New("invalid expanded tile budget")
	}
	br := bufio.NewReader(source)
	gz, err := gzip.NewReader(br)
	if err != nil {
		return err
	}
	gz.Multistream(false)
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(out, h), io.LimitReader(contextReader{ctx, gz}, pin.Bytes+1))
	closeErr := gz.Close()
	if copyErr != nil || closeErr != nil {
		return errors.Join(copyErr, closeErr)
	}
	if n != pin.Bytes || hex.EncodeToString(h.Sum(nil)) != pin.SHA256 {
		return errors.New("tile size/SHA-256 mismatch")
	}
	if _, err := br.ReadByte(); err != io.EOF {
		return errors.New("trailing gzip data")
	}
	return nil
}
