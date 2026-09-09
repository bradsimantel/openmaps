package valhallatiles

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func syntheticScoutLock(t *testing.T) ScoutLock {
	t.Helper()
	b, err := os.ReadFile("testdata/scout/lock.json")
	if err != nil {
		t.Fatal(err)
	}
	var lock ScoutLock
	if err := json.Unmarshal(b, &lock); err != nil {
		t.Fatal(err)
	}
	return lock
}

func TestScoutSyntheticRouting(t *testing.T) {
	legacy, path := fixture(t, true, true)
	a, _ := legacy.Reader.Edge(path[0])
	b, _ := legacy.Reader.Edge(path[2])
	sa, _ := legacy.Reader.Shape(a)
	sb, _ := legacy.Reader.Shape(b)
	from, to := clip(sa.Points, .5, .5)[0], clip(sb.Points, .5, .5)[0]
	want, err := legacy.Route(context.Background(), from, to, 1000)
	if err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	r, err := OpenScout(context.Background(), "testdata/scout", scratch, syntheticScoutLock(t), pageSize)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewRouter(r)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.Route(context.Background(), from, to, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Steps, want.Steps) || got.Seconds != want.Seconds || !reflect.DeepEqual(got.Geometry, want.Geometry) {
		t.Fatal("paged 3.4.0 differs from ordinary synthetic reference")
	}
	if r.Package(path[0]) == r.Package(path[2]) {
		t.Fatal("did not cross package boundary")
	}
	// Retain the restriction even with absent references; never erase a path
	// just because its origin lies outside a bounded package selection.
	delete(r.index, path[0].Base())
	delete(r.tiles, path[0].Base())
	partial, err := NewRouter(r)
	if err != nil || partial.RestrictionCount != s.RestrictionCount {
		t.Fatalf("lost incomplete restriction: %v", err)
	}
	_, err = s.Route(context.Background(), from, to, 1000)
	var missing *MissingTileError
	if !errors.As(err, &missing) {
		t.Fatalf("missing dependency became no-route: %v", err)
	}
	name := r.f.Name()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Node(path[2]); err == nil {
		t.Fatal("read after close")
	}
	if _, err := os.Stat(name); !os.IsNotExist(err) {
		t.Fatalf("spool retained: %v", err)
	}
}

func TestScoutRejectsPinsAndCleansUp(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*ScoutLock)
	}{
		{"version", func(l *ScoutLock) { l.Version = "3.6.3" }},
		{"lock-schema", func(l *ScoutLock) { l.Schema = 2 }},
		{"package-schema", func(l *ScoutLock) { l.PackageSchema = "3" }},
		{"dataset", func(l *ScoutLock) { l.Dataset++ }},
		{"timestamp", func(l *ScoutLock) { l.Timestamp = "wrong" }},
		{"sha", func(l *ScoutLock) { l.Packages[0].SHA256 = fmt.Sprintf("%064d", 0) }},
		{"size", func(l *ScoutLock) { l.Packages[0].Bytes++ }},
		{"tile-sha", func(l *ScoutLock) { l.Packages[0].Tiles[0].SHA256 = fmt.Sprintf("%064d", 0) }},
		{"tile-size", func(l *ScoutLock) { l.Packages[0].Tiles[0].Bytes-- }},
		{"duplicate-package", func(l *ScoutLock) { l.Packages = append(l.Packages, l.Packages[0]) }},
		{"duplicate-tile", func(l *ScoutLock) { l.Packages[1].Tiles = append(l.Packages[1].Tiles, l.Packages[0].Tiles[0]) }},
		{"archive-path", func(l *ScoutLock) { l.Packages[0].ID = "../1" }},
		{"tile-path", func(l *ScoutLock) { l.Packages[0].Tiles[0].Name = "../../unsafe.gph.gz" }},
		{"tile-cap", func(l *ScoutLock) { l.Packages[0].Tiles[0].Bytes = 64<<20 + 1 }},
		{"download-cap", func(l *ScoutLock) { l.Packages[0].Bytes = 100000000 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			lock := syntheticScoutLock(t)
			test.mutate(&lock)
			scratch := t.TempDir()
			r, err := OpenScout(context.Background(), "testdata/scout", scratch, lock, pageSize)
			if err == nil {
				r.Close()
				t.Fatal("invalid input accepted")
			}
			files, err := os.ReadDir(scratch)
			if err != nil || len(files) != 0 {
				t.Fatal("failed preparation left spool")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	scratch := t.TempDir()
	if _, err := OpenScout(ctx, "testdata/scout", scratch, syntheticScoutLock(t), pageSize); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	files, _ := os.ReadDir(scratch)
	if len(files) != 0 {
		t.Fatal("cancelled spool retained")
	}
	// Outer checksum is repinned here solely to reach bzip2 CRC validation.
	lock := syntheticScoutLock(t)
	dir := t.TempDir()
	for i, p := range lock.Packages {
		b, err := os.ReadFile(filepath.Join("testdata/scout", p.ID+".tar.bz2"))
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			b[len(b)-2] ^= 128
			lock.Packages[i].SHA256 = fmt.Sprintf("%x", sha256.Sum256(b))
		}
		if err := os.WriteFile(filepath.Join(dir, p.ID+".tar.bz2"), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if r, err := OpenScout(context.Background(), dir, t.TempDir(), lock, pageSize); err == nil {
		r.Close()
		t.Fatal("bzip CRC corruption accepted")
	}
}

func TestNestedGzipBoundsAndCRC(t *testing.T) {
	body := bytes.Repeat([]byte{42}, 4096)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	pin := ScoutTile{Bytes: int64(len(body)), SHA256: fmt.Sprintf("%x", sha256.Sum256(body))}
	if err := copyScoutTile(context.Background(), bytes.NewReader(buf.Bytes()), io.Discard, pin); err != nil {
		t.Fatal(err)
	}
	corrupt := append([]byte(nil), buf.Bytes()...)
	corrupt[len(corrupt)-8] ^= 1
	for _, b := range [][]byte{corrupt, buf.Bytes()[:buf.Len()-1], append(append([]byte(nil), buf.Bytes()...), 0), append(append([]byte(nil), buf.Bytes()...), buf.Bytes()...)} {
		if err := copyScoutTile(context.Background(), bytes.NewReader(b), io.Discard, pin); err == nil {
			t.Fatal("bad or multiple gzip streams accepted")
		}
	}
	pin.Bytes = 272
	var out bytes.Buffer
	if err := copyScoutTile(context.Background(), bytes.NewReader(buf.Bytes()), &out, pin); err == nil || out.Len() > 273 {
		t.Fatal("expanded byte cap not enforced")
	}
}

func TestPageReadsEvictionAndErrors(t *testing.T) {
	r, err := OpenScout(context.Background(), "testdata/scout", t.TempDir(), syntheticScoutLock(t), pageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	// A byte-pattern file checks exact reads across boundaries under a one-page
	// cache, including a view retained while its page is evicted.
	b := make([]byte, 3*pageSize)
	for i := range b {
		b[i] = byte(i % 251)
	}
	if err := r.f.Truncate(int64(len(b))); err != nil {
		t.Fatal(err)
	}
	if _, err := r.f.WriteAt(b, 0); err != nil {
		t.Fatal(err)
	}
	tile := &tile{size: len(b), reader: r}
	first, err := tile.span(0, 32)
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range []int{pageSize - 7, 2*pageSize - 4, 8, 2*pageSize + 8} {
		got, err := tile.span(at, 32)
		if err != nil || !bytes.Equal(got, b[at:at+32]) {
			t.Fatalf("paged read %d: %v", at, err)
		}
	}
	if !bytes.Equal(first, b[:32]) {
		t.Fatal("borrowed record corrupted by eviction")
	}
	if r.Stats.PeakBytes > pageSize || r.Stats.Evictions == 0 {
		t.Fatal("page cache budget")
	}
	if _, err := tile.span(len(b)-1, 2); err == nil {
		t.Fatal("out-of-tile read")
	}
	r.ClearCache()
	if err := r.f.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if _, err := tile.span(0, 8); err == nil {
		t.Fatal("I/O error hidden")
	}
}

func TestVersionSpecificFields(t *testing.T) {
	s, path := fixture(t, false, false)
	tile, err := s.Reader.get(path[0])
	if err != nil {
		t.Fatal(err)
	}
	b := append([]byte(nil), tile.b...)
	copy(b[16:32], "3.4.0")
	binary.LittleEndian.PutUint32(b[8:], 0)
	binary.LittleEndian.PutUint32(b[12:], 0)
	old, err := parseTile(b, tile.id)
	if err != nil {
		t.Fatal(err)
	}
	n, err := old.node(0)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := tile.node(0)
	if n.Point != want.Point {
		t.Fatal("3.4.0 used stored base instead of GraphId")
	}
	copy(b[16:32], "3.5.0")
	if _, err := parseTile(b, tile.id); err == nil {
		t.Fatal("unaudited version accepted")
	}
}

func TestOriginalPagedReaderRetainsStrictRestrictions(t *testing.T) {
	s, path := fixture(t, false, true)
	r := s.Reader
	r.limit = pageSize
	if err := r.UsePageCache(); err != nil {
		t.Fatal(err)
	}
	delete(r.index, path[0].Base())
	delete(r.tiles, path[0].Base())
	if _, err := NewRouter(r); err == nil {
		t.Fatal("page caching relaxed the original complete-archive requirement")
	}
}
