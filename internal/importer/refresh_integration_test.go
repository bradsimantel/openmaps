//go:build integration

package importer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestRefreshRebuild checks a retained real candidate using only archived inputs.
func TestRefreshRebuild(t *testing.T) {
	baseline := os.Getenv("OPENMAPS_BASELINE")
	candidate := os.Getenv("OPENMAPS_CANDIDATE")
	bundle := os.Getenv("OPENMAPS_BUNDLE")
	config := os.Getenv("OPENMAPS_CONFIG")
	replacements := os.Getenv("OPENMAPS_REPLACEMENTS")
	if baseline == "" || candidate == "" || bundle == "" || config == "" {
		t.Fatal("set absolute OPENMAPS_BASELINE, OPENMAPS_CANDIDATE, OPENMAPS_BUNDLE, OPENMAPS_CONFIG; optionally OPENMAPS_REPLACEMENTS")
	}
	manifest, e := ReadManifest(config)
	if e != nil {
		t.Fatal(e)
	}
	if e = Verify(bundle, manifest.BundleSHA256); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(bundle)
	if e != nil {
		t.Fatal(e)
	}
	var b Bundle
	if e = json.Unmarshal(raw, &b); e != nil {
		t.Fatal(e)
	}
	decisions := []Replacement{}
	if replacements != "" {
		raw, e = os.ReadFile(replacements)
		if e != nil {
			t.Fatal(e)
		}
		if e = json.Unmarshal(raw, &decisions); e != nil {
			t.Fatal(e)
		}
	}
	base, e := ReadSnapshot(context.Background(), baseline)
	if e != nil {
		t.Fatal(e)
	}
	b, h, e := Reconcile(b, base, decisions)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "rebuilt.sqlite")
	if e = Build(context.Background(), path, b); e != nil {
		t.Fatal(e)
	}
	if e = SaveRefreshMetadata(path, h, decisions, manifest.BundleSHA256); e != nil {
		t.Fatal(e)
	}
	old, e := openSnapshot(candidate)
	if e != nil {
		t.Fatal(e)
	}
	defer old.Close()
	next, e := openSnapshot(path)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	for _, q := range []string{"SELECT * FROM entities ORDER BY id", "SELECT * FROM source_records ORDER BY source_key", "SELECT * FROM attribute_provenance ORDER BY entity_id,attribute", "SELECT * FROM relationships ORDER BY from_id,to_id,kind", "SELECT * FROM metadata ORDER BY key", "SELECT rowid,* FROM entity_fts ORDER BY rowid"} {
		a, b := sqlRows(t, old, q), sqlRows(t, next, q)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("rebuild changed %s", q)
		}
		t.Logf("%d identical rows: %s", len(a), q)
	}
}
