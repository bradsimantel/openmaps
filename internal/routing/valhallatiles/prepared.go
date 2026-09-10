package valhallatiles

// Scout preparation publishes immutable bounded page files and source receipts.
import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"container/list"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"syscall"
)

const preparedScoutSchema = "openmaps-scout-pages-v1"
const maxPreparedTiles = 100000
const maxPreparedIndex = 64 << 20
const maxPreparedTile = 256 << 20

// ScoutBudgets controls incremental disk writes. It is not a physical RSS limit.
type ScoutBudgets struct {
	CompressedBytes int64 `json:"compressed_bytes"`
	ExpandedBytes   int64 `json:"expanded_bytes"`
	ReserveBytes    int64 `json:"reserve_bytes"`
}

func (b ScoutBudgets) validate() error {
	if b.CompressedBytes < 1 || b.CompressedBytes > 16<<30 || b.ExpandedBytes < 1 || b.ExpandedBytes > 64<<30 || b.ReserveBytes < 32<<30 {
		return errors.New("Scout budgets require 1..16 GiB compressed, 1..64 GiB expanded and >=32 GiB disk reserve")
	}
	return nil
}
func diskReserve(path string, need, reserve int64) error {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return err
	}
	free := uint64(st.Bavail) * uint64(st.Bsize)
	if need < 0 || free < uint64(need)+uint64(reserve) {
		return errors.New("Scout disk reserve would be exceeded")
	}
	return nil
}

type preparedTile struct {
	ID     ID    `json:"id"`
	Offset int64 `json:"offset"`
	ScoutTile
	Package string `json:"package"`
}
type preparedScout struct {
	BaseReceiptSHA256 string         `json:"base_receipt_sha256,omitempty"`
	Schema            string         `json:"schema"`
	Version           string         `json:"tile_version"`
	Dataset           uint64         `json:"dataset_id"`
	Timestamp         string         `json:"timestamp"`
	Bytes             int64          `json:"bytes"`
	SHA256            string         `json:"sha256"`
	Packages          []ScoutPackage `json:"packages"`
	Tiles             []preparedTile `json:"tiles"`
	Budgets           ScoutBudgets   `json:"budgets"`
}

// PrepareScoutPackages accepts SHA-256-pinned compressed packages and discovers
// their tile pins while streaming. It refuses an existing output directory and
// publishes receipt.json last. The directory is private until that publication.
// At most one gzip stream and 1 MiB copy buffer are live; headers/index entries
// are separately capped. A failed build is retained as unpublished evidence.
func PrepareScoutPackages(ctx context.Context, dir, out string, lock ScoutLock, budget ScoutBudgets) error {
	return prepareScoutPackages(ctx, dir, out, "", false, lock, budget)
}

// ExtendScoutPackages preserves a verified immutable prefix and imports only
// newly pinned packages. clone uses macOS copy-on-write cloning, never a hardlink.
// Other filesystems can use a bounded ordinary copy with sufficient disk space.
func ExtendScoutPackages(ctx context.Context, dir, out, base string, clone bool, lock ScoutLock, budget ScoutBudgets) error {
	if base == "" {
		return errors.New("extension requires a base graph")
	}
	return prepareScoutPackages(ctx, dir, out, base, clone, lock, budget)
}

func prepareScoutPackages(ctx context.Context, dir, out, base string, clone bool, lock ScoutLock, budget ScoutBudgets) error {
	if err := budget.validate(); err != nil {
		return err
	}
	if lock.Schema != 1 || lock.Version != "3.4.0" || lock.PackageSchema != "2" || lock.Timestamp == "" || len(lock.Packages) == 0 || len(lock.Packages) > 4096 {
		return errors.New("unsupported Scout acquisition lock")
	}
	seenPackages := map[string]bool{}
	var compressed int64
	for _, p := range lock.Packages {
		if p.ID == "" || strings.IndexFunc(p.ID, func(c rune) bool { return c < '0' || c > '9' }) >= 0 || seenPackages[p.ID] || p.Bytes < 1 || p.Bytes > 256<<20 || checkDigest(p.SHA256) != nil {
			return errors.New("invalid Scout package pin")
		}
		seenPackages[p.ID] = true
		compressed += p.Bytes
	}
	if compressed > budget.CompressedBytes {
		return errors.New("Scout compressed budget exceeded")
	}
	manifest := preparedScout{Schema: preparedScoutSchema, Version: lock.Version, Dataset: lock.Dataset, Timestamp: lock.Timestamp, Budgets: budget, Packages: lock.Packages}
	oldPackages := map[string]bool{}
	var old *Reader
	if base != "" {
		var err error
		old, err = OpenPreparedScout(ctx, base, pageSize)
		if err != nil {
			return err
		}
		defer old.Close()
		b, err := readBoundedFile(filepath.Join(base, "receipt.json"), maxPreparedIndex)
		if err != nil {
			return err
		}
		if hexSum(b) != old.preparedSHA {
			return errors.New("base receipt changed")
		}
		var prior preparedScout
		if err := json.Unmarshal(b, &prior); err != nil {
			return err
		}
		if prior.Version != lock.Version || prior.Timestamp != lock.Timestamp || (lock.Dataset != 0 && lock.Dataset != prior.Dataset) {
			return errors.New("mixed extension generation")
		}
		for _, p := range prior.Packages {
			found := false
			for _, q := range lock.Packages {
				if p.ID == q.ID {
					found = reflect.DeepEqual(p, q)
					break
				}
			}
			if !found {
				return errors.New("extension removes or changes an existing package pin")
			}
			oldPackages[p.ID] = true
		}
		manifest.Dataset = prior.Dataset
		manifest.Tiles = prior.Tiles
		manifest.BaseReceiptSHA256 = old.preparedSHA
	}
	if err := os.Mkdir(out, 0700); err != nil {
		return err
	}
	path := filepath.Join(out, "tiles.bin")
	if clone {
		if old == nil || runtime.GOOS != "darwin" {
			return errors.New("clone-base requires a verified base and macOS clone-capable filesystem")
		}
		source, err := filepath.Abs(filepath.Join(base, "tiles.bin"))
		if err != nil {
			return err
		}
		target, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		if err := exec.CommandContext(ctx, "/bin/cp", "-c", source, target).Run(); err != nil {
			return fmt.Errorf("copy-on-write clone: %w", err)
		}
	}
	flags := os.O_RDWR
	if !clone {
		flags |= os.O_CREATE | os.O_EXCL
	}
	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if old != nil {
		a, err := old.f.Stat()
		if err != nil {
			return err
		}
		copyInfo, err := f.Stat()
		if err != nil {
			return err
		}
		if os.SameFile(a, copyInfo) {
			return errors.New("extension must own a distinct file")
		}
		if !clone {
			if err := diskReserve(out, a.Size(), budget.ReserveBytes); err != nil {
				return err
			}
			if _, err := old.f.Seek(0, io.SeekStart); err != nil {
				return err
			}
			if _, err := io.Copy(f, contextReader{ctx, old.f}); err != nil {
				return err
			}
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		h := sha256.New()
		n, err := io.Copy(h, contextReader{ctx, f})
		if err != nil {
			return err
		}
		b, err := readBoundedFile(filepath.Join(base, "receipt.json"), maxPreparedIndex)
		if err != nil {
			return err
		}
		if hexSum(b) != old.preparedSHA {
			return errors.New("base receipt changed during copy")
		}
		var prior preparedScout
		if err := json.Unmarshal(b, &prior); err != nil {
			return err
		}
		if n != prior.Bytes || hex.EncodeToString(h.Sum(nil)) != prior.SHA256 {
			return errors.New("copied base bytes do not match verified source")
		}
		last := manifest.Tiles[len(manifest.Tiles)-1]
		end := last.Offset + last.Bytes
		if err := f.Truncate(end); err != nil {
			return err
		}
		if _, err := f.Seek(end, io.SeekStart); err != nil {
			return err
		}
	}
	ids := map[ID]int{}
	for i, p := range manifest.Tiles {
		ids[p.ID] = i
	}
	for _, p := range lock.Packages {
		if oldPackages[p.ID] {
			continue
		}
		if err := preparePackage(ctx, dir, out, f, p, &manifest, ids); err != nil {
			return fmt.Errorf("package %s: %w", p.ID, err)
		}
	}
	size, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		return err
	}
	padded := (size + pageSize - 1) / pageSize * pageSize
	if padded > budget.ExpandedBytes {
		return errors.New("padded spool budget exceeded")
	}
	if err := diskReserve(out, padded-size, budget.ReserveBytes); err != nil {
		return err
	}
	if err := f.Truncate(padded); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(h, contextReader{ctx, f}); err != nil {
		return err
	}
	manifest.Bytes = padded
	manifest.SHA256 = hex.EncodeToString(h.Sum(nil))
	b, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if len(b) > maxPreparedIndex {
		return errors.New("prepared index exceeds 64 MiB")
	}
	return publishJSON(out, "receipt.json", manifest)
}

func preparePackage(ctx context.Context, dir, out string, f *os.File, p ScoutPackage, m *preparedScout, ids map[ID]int) error {
	source, err := os.Open(filepath.Join(dir, p.ID+".tar.bz2"))
	if err != nil {
		return err
	}
	defer source.Close()
	stat, err := source.Stat()
	if err != nil {
		return err
	}
	if stat.Size() != p.Bytes {
		return errors.New("package size mismatch")
	}
	h := sha256.New()
	transport := md5.New()
	if _, err := io.Copy(io.MultiWriter(h, transport), contextReader{ctx, source}); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != p.SHA256 {
		return errors.New("package SHA-256 mismatch")
	}
	if p.MD5 != "" && hex.EncodeToString(transport.Sum(nil)) != p.MD5 {
		return errors.New("package provider MD5 mismatch")
	}
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return err
	}
	expectedTiles := map[string]ScoutTile{}
	for _, pin := range p.Tiles {
		if _, err := scoutID(pin.Name); err != nil {
			return err
		}
		if _, exists := expectedTiles[pin.Name]; exists || pin.Bytes < 272 || pin.Bytes > maxPreparedTile || checkDigest(pin.SHA256) != nil {
			return errors.New("invalid or duplicate explicit tile pin")
		}
		expectedTiles[pin.Name] = pin
	}
	outer := &io.LimitedReader{R: bzip2.NewReader(contextReader{ctx, source}), N: 512<<20 + 1}
	tr := tar.NewReader(outer)
	members := map[string]bool{}
	tileNames := map[string]bool{}
	var listed []string
	stamp := ""
	buf := make([]byte, 1<<20)
	for count := 0; ; count++ {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if count >= 8192 || hdr.Typeflag != tar.TypeReg || hdr.Size < 0 || hdr.Size > 256<<20 || members[hdr.Name] {
			return errors.New("invalid or duplicate package member")
		}
		members[hdr.Name] = true
		id, idErr := scoutID(hdr.Name)
		if idErr != nil {
			if hdr.Size > 1<<20 {
				return errors.New("oversized package metadata")
			}
			b, err := io.ReadAll(tr)
			if err != nil {
				return err
			}
			switch hdr.Name {
			case "valhalla/tiles/timestamp":
				stamp = strings.TrimSpace(string(b))
			case "valhalla/packages/" + p.ID + ".tar.list":
				listed = strings.Fields(string(b))
			default:
				return fmt.Errorf("unsupported member %q", hdr.Name)
			}
			continue
		}
		tileNames[hdr.Name] = true
		offset, err := f.Seek(0, io.SeekCurrent)
		if err != nil {
			return err
		}
		br := bufio.NewReader(tr)
		gz, err := gzip.NewReader(br)
		if err != nil {
			return err
		}
		gz.Multistream(false)
		h := sha256.New()
		var size int64
		for {
			n, readErr := contextReader{ctx, gz}.Read(buf)
			if n > 0 {
				if size+int64(n) > maxPreparedTile || offset+size+int64(n) > m.Budgets.ExpandedBytes {
					return errors.New("tile/spool expansion budget exceeded")
				}
				if err := diskReserve(out, int64(n), m.Budgets.ReserveBytes); err != nil {
					return err
				}
				if _, err := f.Write(buf[:n]); err != nil {
					return err
				}
				h.Write(buf[:n])
				size += int64(n)
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
		if err := gz.Close(); err != nil {
			return err
		}
		if _, err := br.ReadByte(); err != io.EOF {
			return errors.New("trailing gzip stream/data")
		}
		if size < 272 {
			return errors.New("short tile")
		}
		header := make([]byte, 272)
		if _, err := f.ReadAt(header, offset); err != nil {
			return err
		}
		dataset := u64(header, 32)
		if len(m.Tiles) == 0 && m.Dataset == 0 {
			m.Dataset = dataset
		}
		if string(bytes.TrimRight(header[16:32], "\x00")) != m.Version || dataset != m.Dataset || u64(header, 88) != 0 {
			return errors.New("mixed tile version/dataset")
		}
		if _, err := parseTileHeader(header, id, int(size)); err != nil {
			return err
		}
		pin := preparedTile{ID: id, Offset: offset, ScoutTile: ScoutTile{Name: hdr.Name, Bytes: size, SHA256: hex.EncodeToString(h.Sum(nil))}, Package: p.ID}
		if len(p.Tiles) > 0 {
			expected, exists := expectedTiles[hdr.Name]
			if !exists || expected.Bytes != size || expected.SHA256 != pin.SHA256 {
				return errors.New("explicit tile pin mismatch")
			}
			delete(expectedTiles, hdr.Name)
		}
		if index, exists := ids[id]; exists {
			previous := m.Tiles[index]
			if previous.Bytes != pin.Bytes || previous.SHA256 != pin.SHA256 {
				return errors.New("conflicting duplicate tile")
			}
			// Identical duplicates do not consume another extent. Package provenance
			// remains in the acquisition pins, even when payload storage is deduplicated.
			if err := f.Truncate(offset); err != nil {
				return err
			}
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				return err
			}
		} else {
			if len(m.Tiles) >= maxPreparedTiles {
				return errors.New("prepared tile index budget exceeded")
			}
			ids[id] = len(m.Tiles)
			m.Tiles = append(m.Tiles, pin)
		}
	}
	if len(expectedTiles) != 0 {
		return errors.New("missing explicitly pinned tiles")
	}
	if stamp != m.Timestamp || len(listed) != len(tileNames) {
		return errors.New("mixed timestamp or member list mismatch")
	}
	for _, name := range listed {
		if !tileNames[name] {
			return errors.New("member list mismatch")
		}
		delete(tileNames, name)
	}
	if len(tileNames) != 0 {
		return errors.New("incomplete member list")
	}
	if _, err := io.Copy(io.Discard, outer); err != nil {
		return err
	}
	if outer.N <= 0 {
		return errors.New("outer expansion budget exceeded")
	}
	return nil
}

// OpenPreparedScout verifies immutable prepared bytes before exposing a bounded,
// single-owner reader. It never decompresses packages or builds a complete graph.
func OpenPreparedScout(ctx context.Context, dir string, cacheBytes int64) (*Reader, error) {
	if cacheBytes < pageSize || cacheBytes > 256<<20 {
		return nil, errors.New("prepared cache must be 64 KiB..256 MiB")
	}
	receipt, err := os.Open(filepath.Join(dir, "receipt.json"))
	if err != nil {
		return nil, err
	}
	b, readErr := io.ReadAll(io.LimitReader(receipt, maxPreparedIndex+1))
	closeErr := receipt.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if len(b) > maxPreparedIndex {
		return nil, errors.New("oversized prepared index")
	}
	var m preparedScout
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m.Schema != preparedScoutSchema || m.Version != "3.4.0" || m.Timestamp == "" || m.Bytes < pageSize || m.Bytes > 64<<30 || m.Bytes%pageSize != 0 || checkDigest(m.SHA256) != nil || (m.BaseReceiptSHA256 != "" && checkDigest(m.BaseReceiptSHA256) != nil) || len(m.Tiles) == 0 || len(m.Tiles) > maxPreparedTiles {
		return nil, errors.New("invalid prepared receipt")
	}
	f, err := os.Open(filepath.Join(dir, "tiles.bin"))
	if err != nil {
		return nil, err
	}
	r := &Reader{f: f, version: m.Version, index: map[ID]entry{}, tiles: map[ID]*tile{}, packages: map[ID]string{}, pages: map[int64]*list.Element{}, limit: cacheBytes, preparedSHA: hexSum(b), preparedTimestamp: m.Timestamp, preparedDataset: m.Dataset}
	ok := false
	defer func() {
		if !ok {
			r.Close()
		}
	}()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if st.Size() != m.Bytes {
		return nil, errors.New("prepared size mismatch")
	}
	h := sha256.New()
	if _, err := io.Copy(h, contextReader{ctx, f}); err != nil {
		return nil, err
	}
	if hex.EncodeToString(h.Sum(nil)) != m.SHA256 {
		return nil, errors.New("prepared SHA-256 mismatch")
	}
	var next int64
	for _, pin := range m.Tiles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		id, err := scoutID(pin.Name)
		if err != nil || id != pin.ID || pin.Offset != next || pin.Bytes < 272 || pin.Bytes > maxPreparedTile || pin.Offset > m.Bytes-pin.Bytes || checkDigest(pin.SHA256) != nil {
			return nil, errors.New("invalid prepared tile extent")
		}
		if _, exists := r.index[id]; exists {
			return nil, errors.New("duplicate prepared tile")
		}
		header := make([]byte, 272)
		if _, err := f.ReadAt(header, pin.Offset); err != nil {
			return nil, err
		}
		if string(bytes.TrimRight(header[16:32], "\x00")) != m.Version || u64(header, 32) != m.Dataset || u64(header, 88) != 0 {
			return nil, errors.New("mixed prepared tile metadata")
		}
		t, err := parseTileHeader(header, id, int(pin.Bytes))
		if err != nil {
			return nil, err
		}
		t.reader, t.offset = r, pin.Offset
		r.tiles[id] = t
		r.index[id] = entry{pin.Offset, pin.Bytes}
		r.packages[id] = pin.Package
		next = pin.Offset + pin.Bytes
	}
	if (next+pageSize-1)/pageSize*pageSize != m.Bytes {
		return nil, errors.New("unindexed prepared payload")
	}
	ok = true
	return r, nil
}
