package valhallatiles

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Insert a tile before the original tiles in dense ID order, forcing all old
// vector positions to move. Its optional edge connects to a finite component.
func extensionFixture(t *testing.T, base string, connected ID, corruptOld bool) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(base, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m preparedScout
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(base, "tiles.bin"))
	if err != nil {
		t.Fatal(err)
	}
	var end int64
	for _, p := range m.Tiles {
		end = max(end, p.Offset+p.Bytes)
	}
	id := ID(824433<<3 | 2)
	size, info := 304, 304
	if connected != 0 {
		size, info = 364, 352
	}
	tile := make([]byte, size)
	put64 := func(at int, v uint64) { binary.LittleEndian.PutUint64(tile[at:], v) }
	put32 := func(at int, v uint32) { binary.LittleEndian.PutUint32(tile[at:], v) }
	put64(0, uint64(id))
	copy(tile[16:], "3.4.0")
	put64(32, m.Dataset)
	put64(40, 1)
	for _, at := range []int{96, 100, 104} {
		put32(at, uint32(info))
	}
	put32(108, uint32(size))
	put32(216, uint32(size))
	put32(224, uint32(size))
	put64(272, 1<<52)
	if connected != 0 {
		put64(40, 1|1<<21)
		put64(280, 1<<21)
		put64(304, uint64(connected))
		put64(320, 50)
		put64(328, 1)
		put64(336, 100<<32)
	}
	m.Tiles = append(m.Tiles, preparedTile{ID: id, Offset: end, ScoutTile: ScoutTile{Name: "valhalla/tiles/2/000/824/433.gph.gz", Bytes: int64(size), SHA256: hexSum(tile)}, Package: "synthetic"})
	data = append(data[:end], tile...)
	data = append(data, make([]byte, (pageSize-len(data)%pageSize)%pageSize)...)
	if corruptOld {
		p := m.Tiles[0]
		h := data[p.Offset:]
		edgeAt := p.Offset + 272 + int64(field(u64(h, 40), 0, 21))*32 + int64(field(u64(h, 48), 0, 22))*8
		data[edgeAt+16] ^= 1 // Cost changes, while the claimed old tile pin stays unchanged.
	}
	m.Bytes = int64(len(data))
	m.SHA256 = hexSum(data)
	m.BaseReceiptSHA256 = hexSum(raw)
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "tiles.bin"), data, 0600); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(out, "receipt.json"), b, 0600); err != nil {
		t.Fatal(err)
	}
	if err := PrepareScoutTurns(context.Background(), out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestLandmarkReindexDisconnectedExtension(t *testing.T) {
	ctx := context.Background()
	base := syntheticPrepared(t)
	s, err := OpenPreparedRouter(ctx, base, pageSize)
	if err != nil {
		t.Fatal(err)
	}
	edge, err := s.Reader.Edge(s.Reader.TileIDs()[0].WithIndex(0))
	if err != nil {
		t.Fatal(err)
	}
	shape, err := s.Reader.Shape(edge)
	if err != nil {
		t.Fatal(err)
	}
	seeds := []LandmarkSeed{{Name: "fixture", Point: clip(shape.Points, .5, .5)[0]}}
	s.Reader.Close()
	built := filepath.Join(t.TempDir(), "old")
	if err := PrepareLandmarks(ctx, base, built, seeds); err != nil {
		t.Fatal(err)
	}
	next := extensionFixture(t, base, 0, false)
	out := filepath.Join(t.TempDir(), "reindexed")
	if err := ReindexLandmarks(ctx, base, built, next, out); err != nil {
		t.Fatal(err)
	}
	independent := filepath.Join(t.TempDir(), "independent")
	if err := PrepareLandmarks(ctx, next, independent, seeds); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(out, "landmarks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m landmarkManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Entries {
		for _, v := range []landmarkVector{e.Forward, e.Reverse} {
			got, err := os.ReadFile(filepath.Join(out, v.File))
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(independent, v.File))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatal("reindexed distances differ from independently recomputed distances")
			}
			if binary.LittleEndian.Uint32(got[64:]) != 0x7f800000 {
				t.Fatal("new disconnected node is not infinite")
			}
		}
	}
	if err := ReindexLandmarks(ctx, base, built, next, out); err != nil {
		t.Fatal("resume", err)
	}
	after, _ := os.ReadFile(filepath.Join(out, "landmarks.json"))
	if !bytes.Equal(raw, after) {
		t.Fatal("resume mutated manifest")
	}
	reader, err := OpenPreparedRouter(ctx, next, pageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Reader.Close()
	if err := reader.EnableLandmarks(ctx, next, out); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "extension.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	fresh, err := OpenPreparedRouter(ctx, next, pageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Reader.Close()
	if err := fresh.EnableLandmarks(ctx, next, out); err == nil || !strings.Contains(err.Error(), "extension certificate") {
		t.Fatal("corrupt extension proof not rejected by validation", err)
	}
	corrupt := extensionFixture(t, base, 0, true)
	if err := ReindexLandmarks(ctx, base, built, corrupt, filepath.Join(t.TempDir(), "bad-payload")); err == nil || !strings.Contains(err.Error(), "payload differs") {
		t.Fatal("inconsistent retained tile pin accepted", err)
	}
	connected := extensionFixture(t, base, m.Entries[0].Forward.SeedNode, false)
	rejected := filepath.Join(t.TempDir(), "rejected")
	if err := ReindexLandmarks(ctx, base, built, connected, rejected); err == nil || !strings.Contains(err.Error(), "recomputation required") {
		t.Fatal("finite-component connection not rejected", err)
	}
	if _, err := os.Stat(filepath.Join(rejected, "landmarks.json")); !os.IsNotExist(err) {
		t.Fatal("rejected extension published")
	}
}
