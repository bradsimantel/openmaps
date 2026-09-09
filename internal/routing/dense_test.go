package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDenseSourceIdentityAndPartialGeometry(t *testing.T) {
	d := uniformCosts(fixture())
	// Non-monotonic, sparse source IDs must never become array indices. Include
	// a value exceeding exact float64 integer representation.
	ids := map[int64]int64{}
	for i := range d.Nodes {
		ids[d.Nodes[i].ID] = math.MaxInt64 - int64(i)*103
	}
	for i := range d.Nodes {
		d.Nodes[i].ID = ids[d.Nodes[i].ID]
	}
	for i := range d.Segments {
		d.Segments[i].From = ids[d.Segments[i].From]
		d.Segments[i].To = ids[d.Segments[i].To]
	}
	ref, path, receipt := preparedFixture(t, d)
	s, err := openPreparedArtifact(context.Background(), path, receipt)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if !s.dense() || s.nodeIndex != nil || len(s.edges) != 0 {
		t.Fatal("dense load reconstructed source edges or node map")
	}
	for i := range ref.edges {
		if got := s.sourceEdge(i); got != ref.edges[i] {
			t.Fatal("source edge identity changed", i, got, ref.edges[i])
		}
	}
	for i, seg := range d.Segments {
		if got := s.segment(i); !reflect.DeepEqual(got, seg) {
			t.Fatal("source segment identity changed")
		}
		a, b := ref.point(seg.From), ref.point(seg.To)
		origin := Point{a[0]*.75 + b[0]*.25, a[1]*.75 + b[1]*.25}
		destination := Point{a[0]*.25 + b[0]*.75, a[1]*.25 + b[1]*.75}
		want, we := ref.Route(context.Background(), origin, destination)
		got, ge := s.Route(context.Background(), origin, destination)
		if errorText(we) != errorText(ge) || !reflect.DeepEqual(got, want) {
			t.Fatal("partial source path or geometry changed", i, ge, we)
		}
	}
}

func TestDenseWriterRejectsOverflowAndMissingNodes(t *testing.T) {
	for _, mode := range []string{"segment", "from", "to", "ordinal", "version", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s := store(t, uniformCosts(fixture()))
			switch mode {
			case "segment":
				s.edges[0].segment = math.MaxInt32 + 1
			case "from":
				s.edges[0].from = math.MaxInt64
			case "to":
				s.edges[0].to = math.MaxInt64
			case "ordinal":
				s.nodeIndex[s.edges[0].from] = -1
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			version := PreparedVersion
			if mode == "version" {
				version = "future"
			}
			path := filepath.Join(t.TempDir(), "invalid.bin")
			if _, err := s.writePreparedVersion(ctx, path, version); err == nil {
				t.Fatal("invalid encoding published")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("failed write left published artifact", err)
			}
		})
	}
}

func TestPreparedVersionCycle(t *testing.T) {
	ctx := context.Background()
	db := writeFixture(t, persistedFixture())
	var seq int
	var name, snapshot string
	if err := db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &snapshot); err != nil {
		t.Fatal(err)
	}
	sum, err := hashFile(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	source, _, err := Load(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	dirs := map[string]string{}
	for _, version := range []string{preparedVersionV1, PreparedVersion} {
		dir := t.TempDir()
		dirs[version] = dir
		artifact := sum + "-" + version + ".bin"
		h, err := source.writePreparedVersion(ctx, filepath.Join(dir, artifact), version)
		if err != nil {
			t.Fatal(err)
		}
		digest, err := hashFile(ctx, filepath.Join(dir, artifact))
		if err != nil {
			t.Fatal(err)
		}
		r := PreparedReceipt{Version: version, SnapshotSHA256: sum, ArtifactSHA256: digest, GraphSHA256: h.GraphSHA256, Artifact: artifact, Summary: &Summary{Metadata: source.meta, SHA256: source.graphSHA}}
		raw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := publishBytes(filepath.Join(dir, sum+".json"), raw); err != nil {
			t.Fatal(err)
		}
	}
	d := persistedFixture()
	expected, err := source.Route(ctx, d.Nodes[0].Point, d.Nodes[1].Point)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(expected)
	var old *Store
	for _, version := range []string{preparedVersionV1, PreparedVersion, preparedVersionV1} {
		next, err := OpenPrepared(ctx, snapshot, dirs[version])
		if err != nil {
			t.Fatal(err)
		}
		if old != nil {
			old.Close()
		}
		before := next.MappedBytes()
		verifyCtx := WithLoadObserver(ctx, func(p LoadPhase) {
			if p.Name == "mapping" {
				t.Fatal("rollback mapped query data")
			}
		})
		if has, err := VerifyPreparedPublication(verifyCtx, snapshot, sum, dirs[version]); err != nil || !has {
			t.Fatal("version rollback", err)
		}
		if next.MappedBytes() != before {
			t.Fatal("verification changed mapping ownership")
		}
		got, err := next.Route(ctx, d.Nodes[0].Point, d.Nodes[1].Point)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(got)
		if !bytes.Equal(raw, want) {
			t.Fatal("version changed output")
		}
		// An invalid subsequent load leaves next usable.
		if failed, err := OpenPrepared(ctx, snapshot, t.TempDir()); err == nil {
			failed.Close()
			t.Fatal("accepted missing publication")
		}
		next.Close()
		raw, _ = json.Marshal(got)
		if !bytes.Equal(raw, want) {
			t.Fatal("retirement invalidated output")
		}
		old = next
	}
}

func TestDenseAddressAndRestrictionBoundaries(t *testing.T) {
	ctx := context.Background()
	association, address := associationFixture()
	through := timedFixture()
	through.Bans = []Ban{{Relation: 10, Path: []EdgeRef{{Segment: "a"}, {Segment: "c"}, {Segment: "d"}, {Segment: "e"}}}}
	for _, tc := range []struct {
		data      Data
		endpoints []Endpoint
	}{
		{association, []Endpoint{address, {Point: Point{.003, 0}}}},
		{endpointFixture(), []Endpoint{addr(Point{.0021, .0005}, 2), addr(Point{.001, .0001}, 1), addr(Point{.003, .0011}, 3), {Point: Point{.0021, .0005}}}},
		{through, []Endpoint{{Point: Point{.001, 0}}, {Point: Point{.008, 0}}}},
	} {
		for _, version := range []string{preparedVersionV1, PreparedVersion} {
			ref, path, r := preparedFixtureVersion(t, tc.data, version)
			s, err := openPreparedArtifact(ctx, path, r)
			if err != nil {
				t.Fatal(err)
			}
			for _, a := range tc.endpoints {
				for _, b := range tc.endpoints {
					want, we := ref.RouteEndpoints(ctx, a, b)
					got, ge := s.RouteEndpoints(ctx, a, b)
					if errorText(we) != errorText(ge) || !reflect.DeepEqual(got, want) {
						t.Fatal("address evidence, destination phase or restriction changed", version, we, ge, got, want)
					}
				}
			}
			s.Close()
		}
	}
}
