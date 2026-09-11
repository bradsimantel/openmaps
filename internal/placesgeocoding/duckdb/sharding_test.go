package duckdb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParquetShardsRotateAtRowGroupWithoutSplittingEvidenceGroup(t *testing.T) {
	previous := targetParquetShardBytes
	targetParquetShardBytes = 1
	t.Cleanup(func() { targetParquetShardBytes = previous })
	sink, err := newParquetSink[sourceRow](filepath.Join(t.TempDir(), SourcesName))
	if err != nil {
		t.Fatal(err)
	}
	first := make([]sourceRow, maxRowsPerRowGroup)
	for i := range first {
		first[i] = sourceRow{EntityID: "first", SourceKey: "first"}
	}
	if err = sink.addGroup(first); err != nil {
		t.Fatal(err)
	}
	second := []sourceRow{{EntityID: "second", SourceKey: "second-a"}, {EntityID: "second", SourceKey: "second-b"}}
	if err = sink.addGroup(second); err != nil {
		t.Fatal(err)
	}
	if err = sink.close(); err != nil {
		t.Fatal(err)
	}
	if len(sink.files) != 2 || sink.files[0].rows != maxRowsPerRowGroup || sink.files[1].rows != 2 {
		t.Fatalf("unexpected shard rows: %+v", sink.files)
	}
	for _, shard := range sink.files {
		if stat, statErr := os.Stat(filepath.Join(filepath.Dir(sink.basePath), shard.name)); statErr != nil || stat.Size() == 0 {
			t.Fatalf("invalid shard %s: %v", shard.name, statErr)
		}
	}
}
