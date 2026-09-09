package routing

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func preparedFixture(t *testing.T, d Data) (*Store, string, PreparedReceipt) {
	return preparedFixtureVersion(t, d, PreparedVersion)
}
func preparedFixtureVersion(t *testing.T, d Data, version string) (*Store, string, PreparedReceipt) {
	t.Helper()
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("prepared mapping requires Linux/macOS")
	}
	s := store(t, d)
	path := filepath.Join(t.TempDir(), "graph.bin")
	h, e := s.writePreparedVersion(context.Background(), path, version)
	if e != nil {
		t.Fatal(e)
	}
	digest, e := hashFile(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	return s, path, PreparedReceipt{Version: version, ArtifactSHA256: digest, GraphSHA256: h.GraphSHA256, Summary: &Summary{Metadata: s.meta, SHA256: s.graphSHA}}
}
func TestPreparedRoutes(t *testing.T) {
	for _, version := range []string{preparedVersionV1, PreparedVersion} {
		t.Run(version, func(t *testing.T) { testPreparedRoutes(t, version) })
	}
}
func testPreparedRoutes(t *testing.T, version string) {
	fixtures := []Data{uniformCosts(fixture()), cellFixture()}
	for version := 1; version <= 3; version++ {
		d := fixture()
		d.Metadata.Version = version
		d.Metadata.Profile = fmt.Sprintf("driving-distance-v%d", version)
		fixtures = append(fixtures, d)
	}
	for _, d := range fixtures {
		ref, path, r := preparedFixtureVersion(t, d, version)
		s, e := openPreparedArtifact(context.Background(), path, r)
		if e != nil {
			t.Fatal(e)
		}
		for _, a := range d.Nodes {
			for _, b := range d.Nodes {
				x, xe := s.Route(context.Background(), a.Point, b.Point)
				y, ye := ref.RouteReferenceEndpoints(context.Background(), Endpoint{Point: a.Point}, Endpoint{Point: b.Point})
				if !s.HasDuration() && math.Abs(x.Distance-y.Distance) > 1e-6 || errorText(xe) != errorText(ye) || math.Abs(x.Duration-y.Duration) > math.Max(1e-6, math.Abs(y.Duration)*1e-10) || !reflect.DeepEqual(x.Origin, y.Origin) || !reflect.DeepEqual(x.Destination, y.Destination) {
					t.Fatalf("prepared reference mismatch: %v %v", xe, ye)
				}
			}
		}
		// A returned result owns all strings/geometry even after mapping retirement.
		result, _ := s.Route(context.Background(), d.Nodes[0].Point, d.Nodes[len(d.Nodes)-1].Point)
		before, _ := json.Marshal(result)
		s.Close()
		after, _ := json.Marshal(result)
		if string(before) != string(after) {
			t.Fatal("result borrows mapping")
		}
		if s.MappedBytes() != 0 {
			t.Fatal("mapping not retired")
		}
		if _, e = s.Route(context.Background(), d.Nodes[0].Point, d.Nodes[1].Point); e == nil {
			t.Fatal("closed store accepted request")
		}
	}
}
func TestPreparedReproducible(t *testing.T) {
	for _, version := range []string{preparedVersionV1, PreparedVersion} {
		t.Run(version, func(t *testing.T) { testPreparedReproducible(t, version) })
	}
}
func testPreparedReproducible(t *testing.T, version string) {
	_, a, _ := preparedFixtureVersion(t, cellFixture(), version)
	_, b, _ := preparedFixtureVersion(t, cellFixture(), version)
	x, _ := os.ReadFile(a)
	y, _ := os.ReadFile(b)
	if !reflect.DeepEqual(x, y) {
		t.Fatal("independent preparation differs")
	}
}
func TestPreparedRejects(t *testing.T) {
	for _, version := range []string{preparedVersionV1, PreparedVersion} {
		t.Run(version, func(t *testing.T) { testPreparedRejects(t, version) })
	}
}
func testPreparedRejects(t *testing.T, version string) {
	for _, mode := range []string{"truncate", "corrupt", "self checksum", "foreign", "version", "dimensions", "overflow", "edge reference", "recursive path", "spatial cycle", "node table", "dense endpoint", "receipt mismatch", "preprocessing", "graph version", "cost model", "CSR", "padding", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			_, path, r := preparedFixtureVersion(t, cellFixture(), version)
			raw, _ := os.ReadFile(path)
			n := binary.LittleEndian.Uint32(raw)
			var h flatHeader
			json.Unmarshal(raw[4:4+n], &h)
			switch mode {
			case "truncate":
				raw = raw[:len(raw)-1]
			case "corrupt", "self checksum":
				raw[len(raw)-1] ^= 1
				if mode == "self checksum" {
					h.PayloadSHA256 = Digest(raw[flatHeaderBytes:])
				}
			case "foreign":
				r.GraphSHA256 = "foreign"
			case "version":
				h.Version = "future"
			case "preprocessing":
				h.Preprocessing = "future"
			case "graph version":
				section := h.Sections[25]
				part := raw[section.Offset:]
				changed := bytes.Replace(part, []byte(`"version":4`), []byte(`"version":9`), 1)
				copy(part, changed)
			case "cost model":
				section := h.Sections[25]
				part := raw[section.Offset:]
				changed := bytes.Replace(part, []byte(CostModel), []byte("estimated-driving-v9"), 1)
				copy(part, changed)
			case "CSR":
				binary.LittleEndian.PutUint32(raw[h.Sections[2].Offset:], math.MaxUint32)
			case "padding":
				raw[flatHeaderBytes-1] = 1
			case "dimensions":
				h.Sections[0].Count++
			case "overflow":
				h.Sections[0].Count = math.MaxInt64
			case "receipt mismatch":
				if version == PreparedVersion {
					r.Version = preparedVersionV1
				} else {
					r.Version = PreparedVersion
				}
			case "dense endpoint":
				if version == PreparedVersion {
					binary.LittleEndian.PutUint32(raw[h.Sections[1].Offset:], math.MaxUint32)
				} else {
					binary.LittleEndian.PutUint64(raw[h.Sections[1].Offset:], math.MaxUint64)
				}
			case "edge reference":
				if version == PreparedVersion {
					binary.LittleEndian.PutUint32(raw[h.Sections[1].Offset+8:], math.MaxInt32)
				} else {
					binary.LittleEndian.PutUint64(raw[h.Sections[1].Offset+16:], math.MaxInt64)
				}
			case "recursive path":
				binary.LittleEndian.PutUint32(raw[h.Sections[12].Offset:], 0)
			case "spatial cycle":
				binary.LittleEndian.PutUint32(raw[h.Sections[17].Offset+32:], 0)
			case "node table":
				h.Sections[15].Count--
			}
			if mode != "truncate" {
				header, _ := json.Marshal(h)
				clear(raw[:flatHeaderBytes])
				binary.LittleEndian.PutUint32(raw, uint32(len(header)))
				copy(raw[4:], header)
			}
			if mode == "padding" {
				raw[flatHeaderBytes-1] = 1
			}
			os.Chmod(path, 0600)
			if e := os.WriteFile(path, raw, 0400); e != nil {
				t.Fatal(e)
			}
			// Re-sign only structural fixtures to independently exercise reader defenses.
			// Corruption and a self-consistent artifact cannot change the trusted receipt.
			if mode != "corrupt" && mode != "self checksum" && mode != "truncate" {
				r.ArtifactSHA256 = Digest(raw)
			}
			ctx := context.Background()
			if mode == "cancel" {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			s, e := openPreparedArtifact(ctx, path, r)
			if e == nil {
				s.Close()
				t.Fatal("accepted", mode)
			}
		})
	}
}

func TestPreparedCancellationStages(t *testing.T) {
	for _, version := range []string{preparedVersionV1, PreparedVersion} {
		t.Run(version, func(t *testing.T) { testPreparedCancellationStages(t, version) })
	}
}
func testPreparedCancellationStages(t *testing.T, version string) {
	for _, stage := range []string{"mapping", "artifact integrity", "ancillary decoding", "runtime structural validation"} {
		t.Run(stage, func(t *testing.T) {
			_, path, r := preparedFixtureVersion(t, cellFixture(), version)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = WithLoadObserver(ctx, func(p LoadPhase) {
				if p.Name == stage {
					cancel()
				}
			})
			if s, e := openPreparedArtifact(ctx, path, r); e == nil {
				s.Close()
				t.Fatal("cancelled load succeeded")
			}
			// Failed loading cannot poison the immutable artifact or a later reader.
			s, e := openPreparedArtifact(context.Background(), path, r)
			if e != nil {
				t.Fatal(e)
			}
			s.Close()
		})
	}
}

func TestPreparedConcurrentClose(t *testing.T) {
	for _, version := range []string{preparedVersionV1, PreparedVersion} {
		t.Run(version, func(t *testing.T) { testPreparedConcurrentClose(t, version) })
	}
}
func testPreparedConcurrentClose(t *testing.T, version string) {
	d := cellFixture()
	_, path, r := preparedFixtureVersion(t, d, version)
	s, e := openPreparedArtifact(context.Background(), path, r)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan struct{}, 4)
	for range 4 {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 10 {
				s.Route(context.Background(), d.Nodes[0].Point, d.Nodes[len(d.Nodes)-1].Point)
			}
		}()
	}
	s.Close()
	for range 4 {
		<-done
	}
	s.Close()
}

func TestPreparedSourcePublication(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("mapping platform")
	}
	ctx := context.Background()
	db := writeFixture(t, persistedFixture())
	var seq int
	var name, path string
	if e := db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); e != nil {
		t.Fatal(e)
	}
	dir := t.TempDir()
	if _, e := OpenRuntime(ctx, path, dir, false, ""); e == nil {
		t.Fatal("missing prepared artifact fell back")
	}
	sum, e := hashFile(ctx, path)
	if e != nil {
		t.Fatal(e)
	}
	s, _, e := Load(ctx, db)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	if _, e = s.PublishPrepared(ctx, path, Digest([]byte("foreign")), dir); e == nil {
		t.Fatal("foreign source digest accepted")
	}
	r, e := s.PublishPrepared(ctx, path, sum, dir)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.PublishPrepared(ctx, path, sum, dir); e == nil {
		t.Fatal("publication overwrote receipt")
	}
	loaded, e := OpenPrepared(ctx, path, dir)
	if e != nil {
		t.Fatal(e)
	}
	defer loaded.Close()
	// Changing only the trusted-looking artifact metadata cannot bless topology.
	artifact := filepath.Join(dir, r.Artifact)
	raw, _ := os.ReadFile(artifact)
	raw[len(raw)-1] ^= 1
	os.Chmod(artifact, 0600)
	// No live mapping may be modified. Retire the reader first.
	loaded.Close()
	if e = os.WriteFile(artifact, raw, 0400); e != nil {
		t.Fatal(e)
	}
	if _, e = OpenPrepared(ctx, path, dir); e == nil {
		t.Fatal("corrupt artifact accepted")
	}
	// Changing source bytes selects no existing receipt, even if routing is intact.
	if _, e = db.Exec("CREATE TABLE changed_snapshot (value INTEGER)"); e != nil {
		t.Fatal(e)
	}
	if _, e = OpenPrepared(ctx, path, dir); e == nil {
		t.Fatal("foreign snapshot accepted")
	}
}

func TestVerifyPreparedPublication(t *testing.T) {
	for _, mode := range []string{"valid", "cancel before", "cancel source", "cancel artifact", "corrupt", "foreign source", "foreign artifact", "missing receipt", "receipt version", "graph version", "cost version", "summary", "partial schema"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			db := writeFixture(t, persistedFixture())
			var seq int
			var name, path string
			if err := db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
				t.Fatal(err)
			}
			s, _, err := Load(ctx, db)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			sum, err := hashFile(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			r, err := s.PublishPrepared(ctx, path, sum, dir)
			if err != nil {
				t.Fatal(err)
			}
			artifact := filepath.Join(dir, r.Artifact)
			// Hold the exact publication live. Validation must not borrow or close it.
			live, err := OpenPrepared(ctx, path, dir)
			if err != nil {
				t.Fatal(err)
			}
			defer live.Close()
			before := live.MappedBytes()
			switch mode {
			case "corrupt":
				// Replace the pathname, never modify the inode held by a live map.
				raw, _ := os.ReadFile(artifact)
				raw[len(raw)-1] ^= 1
				if err := os.Remove(artifact); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(artifact, raw, 0400); err != nil {
					t.Fatal(err)
				}
			case "foreign source":
				sum = Digest([]byte("foreign"))
			case "foreign artifact":
				r.ArtifactSHA256 = Digest([]byte("foreign"))
			case "receipt version":
				r.Version = "future"
			case "graph version":
				r.Summary.Metadata.Version = 99
			case "cost version":
				r.Summary.Metadata.CostModel = "future"
			case "summary":
				r.Summary = nil
			case "partial schema":
				if _, err := db.Exec("DROP TABLE routing_graph"); err != nil {
					t.Fatal(err)
				}
				sum, _ = hashFile(ctx, path)
			}
			if mode == "missing receipt" {
				os.Remove(filepath.Join(dir, r.SnapshotSHA256+".json"))
			} else {
				raw, _ := json.Marshal(r)
				os.Chmod(filepath.Join(dir, r.SnapshotSHA256+".json"), 0600)
				if err := os.WriteFile(filepath.Join(dir, r.SnapshotSHA256+".json"), raw, 0400); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			if mode == "cancel before" {
				cancel()
			}
			ctx = WithLoadObserver(ctx, func(p LoadPhase) {
				if p.Name == "mapping" {
					t.Fatal("publication verifier created a mapping")
				}
				if mode == "cancel source" && p.Name == "publication source integrity" || mode == "cancel artifact" && p.Name == "publication artifact integrity" {
					cancel()
				}
			})
			has, err := VerifyPreparedPublication(ctx, path, sum, dir)
			if mode == "valid" && (err != nil || !has) {
				t.Fatalf("valid publication: %t %v", has, err)
			}
			if mode != "valid" && err == nil {
				t.Fatal("accepted", mode)
			}
			if live.MappedBytes() != before {
				t.Fatal("verifier changed serving mapping ownership")
			}
			d := persistedFixture()
			if _, err := live.Route(context.Background(), d.Nodes[0].Point, d.Nodes[1].Point); err != nil {
				t.Fatal("serving snapshot invalidated", err)
			}
		})
	}
}
