package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func refreshFixture(t *testing.T) Bundle {
	t.Helper()
	raw, e := os.ReadFile("testdata/small.json")
	if e != nil {
		t.Fatal(e)
	}
	var b Bundle
	if e = json.Unmarshal(raw, &b); e != nil {
		t.Fatal(e)
	}
	return b
}
func refreshBaseline(t *testing.T) (Bundle, Snapshot, string) {
	t.Helper()
	b := refreshFixture(t)
	p := filepath.Join(t.TempDir(), "base.sqlite")
	if e := Build(context.Background(), p, b); e != nil {
		t.Fatal(e)
	}
	s, e := ReadSnapshot(context.Background(), p)
	if e != nil {
		t.Fatal(e)
	}
	return b, s, p
}
func renameSource(b *Bundle, old, next string) {
	for i := range b.Records {
		if b.Records[i].Key() == old {
			b.Records[i].SourceID = next
		}
	}
	for i := range b.Relationships {
		if b.Relationships[i].From == old {
			b.Relationships[i].From = "fixture:place:" + next
		}
	}
}
func TestRefreshReplacementAndHistory(t *testing.T) {
	b, base, path := refreshBaseline(t)
	old := "fixture:place:tavern"
	next := "fixture:place:replacement"
	renameSource(&b, old, "replacement")
	decisions := []Replacement{{Old: old, New: next, Evidence: "provider correction inspected", Reviewer: "fixture reviewer"}}
	b, h, e := Reconcile(b, base, decisions)
	if e != nil {
		t.Fatal(e)
	}
	if b.Identities[next] != old {
		t.Fatal("anchor lost")
	}
	p := filepath.Join(t.TempDir(), "next.sqlite")
	if e = Build(context.Background(), p, b); e != nil {
		t.Fatal(e)
	}
	if e = SaveRefreshMetadata(p, h, decisions, "fixture"); e != nil {
		t.Fatal(e)
	}
	report, e := Compare(context.Background(), path, p, []QueryCheck{{Input: "White Horse Tavern", FirstID: PublicID(old)}})
	if e != nil {
		t.Fatal(e)
	}
	if len(report.Violations) != 0 || report.ContinuingIDs != len(base.Entities) || len(report.RelationshipsAdded) != 0 || len(report.RelationshipsRemoved) != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	s, e := ReadSnapshot(context.Background(), p)
	if e != nil {
		t.Fatal(e)
	}
	// Dropped mapping and a second replacement must retain the original anchor.
	b.Identities = nil
	renameSource(&b, next, "third")
	b, history, e := Reconcile(b, s, []Replacement{{Old: next, New: "fixture:place:third", Evidence: "second correction", Reviewer: "fixture"}})
	if e != nil {
		t.Fatal(e)
	}
	if b.Identities["fixture:place:third"] != old || history[old].Anchor != old || history[next].Anchor != old {
		t.Fatal("chained refresh lost permanent anchors")
	}
}
func TestRefreshRejectsAmbiguousMappings(t *testing.T) {
	for _, scenario := range []string{"unreviewed", "cross-kind", "split", "merge", "retarget", "surviving-old", "no-evidence"} {
		t.Run(scenario, func(t *testing.T) {
			b, base, _ := refreshBaseline(t)
			old := "fixture:place:tavern"
			next := "fixture:place:replacement"
			renameSource(&b, old, "replacement")
			d := []Replacement{{Old: old, New: next, Evidence: "inspected", Reviewer: "fixture"}}
			switch scenario {
			case "unreviewed":
				b.Identities[next] = old
				d = nil
			case "cross-kind":
				b.Records[0].Kind = "address"
			case "split":
				r := b.Records[0]
				r.SourceID = "second"
				b.Records = append(b.Records, r)
				d = append(d, Replacement{Old: old, New: r.Key(), Evidence: "split", Reviewer: "fixture"})
			case "merge":
				d = append(d, Replacement{Old: "fixture:place:cafe", New: next, Evidence: "merge", Reviewer: "fixture"})
			case "retarget":
				b.Identities["fixture:address:26"] = "new-anchor"
			case "surviving-old":
				r := b.Records[0]
				r.SourceID = "tavern"
				b.Records = append(b.Records, r)
			case "no-evidence":
				d[0].Evidence = ""
			}
			if _, _, e := Reconcile(b, base, d); e == nil {
				t.Fatal("unsafe identity change accepted")
			}
		})
	}
}
func TestRefreshSplitMergeAndAbsenceStayDistinct(t *testing.T) {
	b, base, path := refreshBaseline(t)
	old := "fixture:place:tavern"
	// One continuing entity gains a nearby same-name record: possible split.
	r := b.Records[0]
	r.SourceID = "split-child"
	b.Records = append(b.Records, r)
	b, h, e := Reconcile(b, base, nil)
	if e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(t.TempDir(), "split.sqlite")
	if e = Build(context.Background(), p, b); e != nil {
		t.Fatal(e)
	}
	if e = SaveRefreshMetadata(p, h, nil, "fixture"); e != nil {
		t.Fatal(e)
	}
	report, e := Compare(context.Background(), path, p, []QueryCheck{{Input: "Café Bellevue", FirstID: PublicID("fixture:place:cafe")}})
	if e != nil {
		t.Fatal(e)
	}
	if len(report.Violations) != 0 || len(report.Added) != 1 || len(report.ReviewMatches) != 1 || report.ReviewMatches[0].BeforeID != PublicID(old) {
		t.Fatalf("split was not surfaced: %+v", report.ReviewMatches)
	}
	// Removing that extra record is a possible merge, never an alias/redirect.
	s, e := ReadSnapshot(context.Background(), p)
	if e != nil {
		t.Fatal(e)
	}
	b.Records = b.Records[:len(b.Records)-1]
	b, h, e = Reconcile(b, s, nil)
	if e != nil {
		t.Fatal(e)
	}
	p2 := filepath.Join(t.TempDir(), "merge.sqlite")
	if e = Build(context.Background(), p2, b); e != nil {
		t.Fatal(e)
	}
	if e = SaveRefreshMetadata(p2, h, nil, "fixture"); e != nil {
		t.Fatal(e)
	}
	report, e = Compare(context.Background(), p, p2, []QueryCheck{{Input: "Café Bellevue", FirstID: PublicID("fixture:place:cafe")}})
	if e != nil {
		t.Fatal(e)
	}
	if len(report.Violations) != 0 || len(report.Removed) != 1 || len(report.ReviewMatches) != 1 {
		t.Fatalf("merge/absence not surfaced: %+v", report)
	}
	// Absent source history prevents later reinterpretation across entity kinds.
	next, e := ReadSnapshot(context.Background(), p2)
	if e != nil {
		t.Fatal(e)
	}
	r.Kind = "address"
	b.Records = append(b.Records, r)
	if _, _, e = Reconcile(b, next, nil); e == nil {
		t.Fatal("retired source reused across kinds")
	}
}
func TestRefreshDetectsSearchRegressionAndCorruption(t *testing.T) {
	_, _, path := refreshBaseline(t)
	r, e := Compare(context.Background(), path, path, []QueryCheck{{Input: "White Horse Tavern", Empty: true}})
	if e != nil {
		t.Fatal(e)
	}
	if len(r.Violations) == 0 {
		t.Fatal("search regression accepted")
	}
	db, e := openSnapshot(path)
	if e != nil {
		t.Fatal(e)
	}
	db.Close()
	// Write only our disposable fixture, then prove incomplete FTS is rejected.
	db, e = sql.Open("sqlite", path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("DELETE FROM entity_fts WHERE rowid=(SELECT rowid FROM entities LIMIT 1)"); e != nil {
		t.Fatal(e)
	}
	db.Close()
	if _, e = ReadSnapshot(context.Background(), path); e == nil {
		t.Fatal("incomplete index accepted")
	}
}
