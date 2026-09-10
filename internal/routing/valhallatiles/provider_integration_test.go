//go:build integration

package valhallatiles

import "testing"

// Independent output walk: verify adjacency through explicit transitions, mode
// rules, turn masks and whole forbidden subsequences without the search trie.
func verifyRoute(t *testing.T, s *Router, result Result) {
	t.Helper()
	var ids []ID
	meters, seconds := 0.0, 0.0
	for i, step := range result.Steps {
		e, err := s.Reader.Edge(step.Edge)
		if err != nil {
			t.Fatal(err)
		}
		ok, err := s.Allowed(e)
		if err != nil || !ok {
			t.Fatalf("forbidden output edge %s: %v", e.ID, err)
		}
		if i > 0 {
			previous, _ := s.Reader.Edge(result.Steps[i-1].Edge)
			start, err := s.Reader.Start(e.ID)
			if err != nil {
				t.Fatal(err)
			}
			if start.ID != previous.End {
				n, err := s.Reader.Node(previous.End)
				if err != nil {
					t.Fatal(err)
				}
				trans, err := s.Reader.Transitions(n)
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, id := range trans {
					found = found || id == start.ID
				}
				if !found {
					t.Fatalf("disconnected route edges: %s %s", previous.ID, e.ID)
				}
			}
			if previous.OppLocalIndex == e.LocalIndex || e.LocalIndex < 8 && previous.Restrictions&(1<<e.LocalIndex) != 0 {
				t.Fatal("prohibited simple maneuver in output")
			}
		}
		ids = append(ids, e.ID)
		meters += e.Length * (step.To - step.From)
		seconds += e.Length * (step.To - step.From) * 3.6 / float64(e.Speed)
	}
	for _, tile := range s.Reader.TileIDs() {
		rs, err := s.Reader.Restrictions(tile)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rs {
			for i := 0; i+len(r.Path) <= len(ids); i++ {
				same := true
				for j := range r.Path {
					same = same && ids[i+j] == r.Path[j]
				}
				if same {
					t.Fatal("complex restricted sequence in output")
				}
			}
		}
	}
	if meters != result.Meters || seconds != result.Seconds {
		t.Fatal("cost reconstruction mismatch")
	}
	if Distance(result.Geometry[0], result.Origin.Point) > .1 || Distance(result.Geometry[len(result.Geometry)-1], result.Destination.Point) > .1 {
		t.Fatal("snap/geometry mismatch")
	}
}
