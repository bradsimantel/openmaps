package routing

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestReverseSupportMatchesFullEnumerationAndRejectsCorruption(t *testing.T) {
	ctx := context.Background()
	dir := syntheticPrepared(t)
	if err := PrepareReverseSupport(ctx, dir); err != nil {
		t.Fatal(err)
	}
	s, err := OpenPreparedRouter(ctx, dir, pageSize)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Reader.Close()
	if err := s.loadReverseSupport(ctx, dir); err != nil {
		t.Fatal(err)
	}
	certificate := s.reverseSupport
	for _, id := range s.Reader.TileIDs() {
		tile, _ := s.Reader.get(id)
		for i := 0; i < tile.nodes; i++ {
			n, _ := s.Reader.Node(id.WithIndex(i))
			var sets []map[ID]ID
			for _, support := range []*reverseSupport{nil, certificate} {
				s.reverseSupport = support
				set := map[ID]ID{}
				if _, err := s.incoming(n, false, func(e Edge, from ID) error { set[e.ID] = from; return nil }); err != nil {
					t.Fatal(err)
				}
				sets = append(sets, set)
			}
			if !reflect.DeepEqual(sets[0], sets[1]) {
				t.Fatal("certificate differs from actual incoming enumeration")
			}
		}
	}
	f, err := os.OpenFile(filepath.Join(dir, "reverse-support.bin"), os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{255}, 64); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := s.loadReverseSupport(ctx, dir); err == nil {
		t.Fatal("corrupt reverse certificate accepted")
	}
}
