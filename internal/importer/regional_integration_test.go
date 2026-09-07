//go:build integration

package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// This check deliberately requires real prepared inputs and an existing baseline
// database; it never downloads data and is excluded from the routine suite.
func TestRegionalRebuild(t *testing.T) {
	dir := os.Getenv("OPENMAPS_DATA")
	if dir == "" {
		t.Fatal("set OPENMAPS_DATA to prepared regional inputs")
	}
	baseline := os.Getenv("OPENMAPS_BASELINE")
	if baseline == "" {
		t.Fatal("set OPENMAPS_BASELINE to a regional SQLite baseline")
	}
	root := filepath.Join("..", "..")
	m, e := ReadManifest(filepath.Join(root, "imports/newport.lock.json"))
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(root, "imports/identities.json"))
	if e != nil {
		t.Fatal(e)
	}
	ids := map[string]string{}
	if e = json.Unmarshal(raw, &ids); e != nil {
		t.Fatal(e)
	}
	b, _, e := Prepare(context.Background(), m, dir, ids)
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "rebuilt.sqlite")
	if e = Build(context.Background(), path, b); e != nil {
		t.Fatal(e)
	}
	old, e := sql.Open("sqlite", baseline)
	if e != nil {
		t.Fatal(e)
	}
	defer old.Close()
	next, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer next.Close()
	for _, query := range []string{
		"SELECT * FROM entities ORDER BY id",
		"SELECT * FROM attribute_provenance ORDER BY entity_id,attribute",
		"SELECT * FROM relationships ORDER BY from_id,to_id,kind",
		"SELECT source_key,entity_id,source,source_id,release,priority,attributes,paths FROM source_records ORDER BY source_key",
	} {
		a := sqlRows(t, old, query)
		c := sqlRows(t, next, query)
		if !reflect.DeepEqual(a, c) {
			t.Fatalf("rebuild changed %s", query)
		}
		t.Logf("%d identical rows: %s", len(a), query)
	}
	a, c := sqlRows(t, old, "SELECT source_key,raw FROM source_records ORDER BY source_key"), sqlRows(t, next, "SELECT source_key,raw FROM source_records ORDER BY source_key")
	if len(a) != len(c) {
		t.Fatal("source count changed")
	}
	for i := range a {
		var x, y any
		if e = json.Unmarshal([]byte(a[i][1].(string)), &x); e != nil {
			t.Fatal(e)
		}
		if e = json.Unmarshal([]byte(c[i][1].(string)), &y); e != nil {
			t.Fatal(e)
		}
		if !reflect.DeepEqual(sourceSemantics(x), sourceSemantics(y)) {
			t.Fatalf("raw source meaning changed: %s", a[i][0])
		}
	}
	t.Log("All raw source records retain equivalent content (JSON numbers and map-pair order normalized)")
}
func sqlRows(t *testing.T, db *sql.DB, query string) [][]any {
	t.Helper()
	rows, e := db.Query(query)
	if e != nil {
		t.Fatal(e)
	}
	defer rows.Close()
	columns, e := rows.Columns()
	if e != nil {
		t.Fatal(e)
	}
	out := [][]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if e = rows.Scan(pointers...); e != nil {
			t.Fatal(e)
		}
		out = append(out, values)
	}
	if e = rows.Err(); e != nil {
		t.Fatal(e)
	}
	return out
}
func sourceSemantics(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			x[k] = sourceSemantics(c)
		}
		return x
	case []any:
		pairs := len(x) > 0
		for i, c := range x {
			x[i] = sourceSemantics(c)
			p, ok := c.([]any)
			if !ok || len(p) != 2 {
				pairs = false
			} else if _, ok := p[0].(string); !ok {
				pairs = false
			}
		}
		if pairs {
			sort.Slice(x, func(i, j int) bool { return x[i].([]any)[0].(string) < x[j].([]any)[0].(string) })
		}
		return x
	default:
		return v
	}
}
