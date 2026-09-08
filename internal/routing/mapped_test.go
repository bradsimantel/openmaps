//go:build darwin || linux

package routing

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestMappedRoutesAndLifetime(t *testing.T) {
	ctx := context.Background()
	d := uniformCosts(fixture())
	reference := store(t, d)
	s := store(t, d)
	dir := t.TempDir()
	if err := s.UseMappedQueryData(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if s.MappedBytes() == 0 {
		t.Fatal("no mapping")
	}
	for _, a := range d.Nodes {
		for _, b := range d.Nodes {
			x, e := s.Route(ctx, a.Point, b.Point)
			y, f := reference.RouteReferenceEndpoints(ctx, Endpoint{Point: a.Point}, Endpoint{Point: b.Point})
			if errorText(e) != errorText(f) || x.Duration != y.Duration || !reflect.DeepEqual(x.Origin, y.Origin) || !reflect.DeepEqual(x.Destination, y.Destination) {
				t.Fatalf("mapped reference mismatch %v %v", e, f)
			}
		}
	}
	// Reopening validates and reuses the immutable artifact without replacing it.
	again := store(t, d)
	if err := again.UseMappedQueryData(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s.MappedBytes() != 0 {
		t.Fatal("mapping retained after close")
	}
	if _, err := s.Route(ctx, d.Nodes[0].Point, d.Nodes[1].Point); err == nil {
		t.Fatal("route after close")
	}
	if _, err := again.Route(ctx, d.Nodes[0].Point, d.Nodes[1].Point); err != nil {
		t.Fatal("closing old mapping damaged another reader", err)
	}
	again.Close()
	s.Close()
}

func TestMappedCorruption(t *testing.T) {
	for _, mode := range []string{"version", "preprocessing", "checksum", "padding", "truncated", "foreign graph", "foreign cell path", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			d := cellFixture()
			s := store(t, d)
			dir := t.TempDir()
			if err := s.UseMappedQueryData(context.Background(), dir); err != nil {
				t.Fatal(err)
			}
			s.Close()
			files, err := filepath.Glob(filepath.Join(dir, "*.bin"))
			if err != nil || len(files) != 1 {
				t.Fatal(files, err)
			}
			path := files[0]
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "version", "preprocessing":
				n := binary.LittleEndian.Uint32(raw)
				var h flatHeader
				if err := json.Unmarshal(raw[4:4+n], &h); err != nil {
					t.Fatal(err)
				}
				if mode == "version" {
					h.Version = "future"
				} else {
					h.Preprocessing = "future"
				}
				b, _ := json.Marshal(h)
				clear(raw[:flatHeaderBytes])
				binary.LittleEndian.PutUint32(raw, uint32(len(b)))
				copy(raw[4:], b)
			case "checksum":
				raw[len(raw)-1] ^= 1
			case "padding":
				raw[flatHeaderBytes-1] = 1
			case "truncated":
				raw = raw[:len(raw)-1]
			case "foreign graph", "foreign cell path":
				if mode == "foreign cell path" {
					raw[len(raw)-1] ^= 1
				} else {
					raw[flatHeaderBytes+8] ^= 1
				}
				// A self-consistent checksum must not authorize changed graph data.
				n := binary.LittleEndian.Uint32(raw)
				var h flatHeader
				if err := json.Unmarshal(raw[4:4+n], &h); err != nil {
					t.Fatal(err)
				}
				h.PayloadSHA256 = Digest(raw[flatHeaderBytes:])
				b, err := json.Marshal(h)
				if err != nil {
					t.Fatal(err)
				}
				clear(raw[:flatHeaderBytes])
				binary.LittleEndian.PutUint32(raw, uint32(len(b)))
				copy(raw[4:], b)
			}
			if err := os.Chmod(path, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			next := store(t, d)
			ctx := context.Background()
			if mode == "cancelled" {
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			if err := next.UseMappedQueryData(ctx, dir); err == nil {
				t.Fatal("accepted corrupt/unsupported artifact")
			}
			if next.MappedBytes() != 0 {
				t.Fatal("failed mapping published")
			}
			// A failed map leaves the validated heap backend available.
			if _, err := next.Route(context.Background(), d.Nodes[0].Point, d.Nodes[1].Point); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMappedCloseWaitsForSearch(t *testing.T) {
	s := store(t, uniformCosts(fixture()))
	if err := s.UseMappedQueryData(context.Background(), t.TempDir()); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	ctx := &blockingSearchContext{Context: context.Background(), entered: entered, release: release}
	routed := make(chan struct{})
	go func() { s.Route(ctx, Point{.001, 0}, Point{.008, .004}); close(routed) }()
	<-entered
	closed := make(chan struct{})
	go func() { s.Close(); close(closed) }()
	select {
	case <-closed:
		t.Fatal("unmapped during routing")
	case <-time.After(10 * time.Millisecond):
	}
	close(release)
	<-routed
	<-closed
	if s.MappedBytes() != 0 {
		t.Fatal("mapping not released")
	}
}

type blockingSearchContext struct {
	context.Context
	entered, release chan struct{}
	once             sync.Once
}

func (c *blockingSearchContext) Err() error {
	c.once.Do(func() { close(c.entered); <-c.release })
	return nil
}
