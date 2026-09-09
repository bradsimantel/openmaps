package dataset

import (
	"context"
	"database/sql"
	"os"
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

func TestPreparedRollbackLookupAndFailureAtomicity(t *testing.T) {
	base, next, state, _ := fixture(t)
	ctx := context.Background()
	bf, err := Describe(base)
	if err != nil {
		t.Fatal(err)
	}
	nf, err := Describe(next)
	if err != nil {
		t.Fatal(err)
	}
	if err = Change(state, func(s *State) error { s.Current = nf; s.Previous = &bf; return nil }); err != nil {
		t.Fatal(err)
	}
	live, err := OpenPrepared(ctx, state, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()
	before, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := RollbackPrepared(cancelled, state, t.TempDir()); err == nil {
		t.Fatal("cancelled rollback published")
	}
	after, _ := os.ReadFile(state)
	if string(before) != string(after) {
		t.Fatal("failed rollback changed selection")
	}
	if status, _ := serve(live, "/healthz"); status != 200 {
		t.Fatal("failure invalidated serving snapshot")
	}
	if err := RollbackPrepared(ctx, state, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	selected, err := Read(state)
	if err != nil || selected.Current != bf || *selected.Previous != nf {
		t.Fatal("lookup rollback did not exchange snapshots", err)
	}
	if status, _ := serve(live, "/healthz"); status != 200 {
		t.Fatal("lookup rollback failed")
	}
}
