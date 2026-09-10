package valhallatiles

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
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

func TestPageReadsEvictionAndErrors(t *testing.T) {
	r := prepareSourceFixture(t, "testdata/scout", syntheticScoutLock(t), pageSize)
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
