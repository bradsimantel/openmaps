package dataset

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"openmaps/internal/routing"
)

func TestPreparedRuntimeRejectsMissingAndPartial(t *testing.T) {
	// Missing source and missing receipts must never invoke legacy rebuilding.
	ctx := context.Background()
	dir := t.TempDir()
	if _, e := routing.OpenPrepared(ctx, filepath.Join(dir, "missing.sqlite"), dir); e == nil {
		t.Fatal("accepted absent source")
	}
	// A valid lookup-only snapshot is covered by deployment tests; a partial
	// routing table must not quietly become routing unavailable.
	path := filepath.Join(dir, "partial.sqlite")
	db, e := sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec("CREATE TABLE routing_chunks (kind TEXT)"); e != nil {
		t.Fatal(e)
	}
	if _, e = routing.OpenRuntime(ctx, path, dir, false, ""); e == nil {
		t.Fatal("partial routing snapshot accepted")
	}
}
