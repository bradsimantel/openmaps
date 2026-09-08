package importer

import (
	"github.com/paulmach/osm"
	"testing"
)

func TestImportedAccessEvidence(t *testing.T) {
	n, w := sourceGraphFixture()
	n[3].Tags = osm.Tags{{Key: "amenity", Value: "parking_entrance"}}
	w[2].Tags = append(w[2].Tags, osm.Tag{Key: "name", Value: "Fixture Road"})
	n = append(n, &osm.Node{ID: 6, Lon: .003, Lat: .003}, &osm.Node{ID: 7, Lon: .005, Lat: .003})
	w = append(w, &osm.Way{ID: 10, Tags: osm.Tags{{Key: "amenity", Value: "parking"}, {Key: "addr:housenumber", Value: "7"}, {Key: "addr:street", Value: "Fixture Road"}}, Nodes: osm.WayNodes{{ID: 6}, {ID: 7}, {ID: 4}, {ID: 6}}})
	d, _ := importSourceGraph(t, n, w)
	if len(d.Access.Areas) != 1 || d.Access.Areas[0].Number != "7" || len(d.Access.Entrances) != 1 || d.Access.Entrances[0].Node != 4 {
		t.Fatal(d.Access)
	}
	found := false
	for _, s := range d.Sources {
		if s.Kind == "way" && s.ID == 10 {
			found = true
		}
	}
	if !found {
		t.Fatal("lost source/provenance")
	}
	w[len(w)-1].Tags = append(w[len(w)-1].Tags, osm.Tag{Key: "access", Value: "customers"})
	d, _ = importSourceGraph(t, n, w)
	if !d.Access.Areas[0].Restricted {
		t.Fatal("parking customer permission lost")
	}
	// Missing polygon vertex outside retained area cannot create a clipped ring.
	n[5].Lon = 2
	d, _ = importSourceGraph(t, n, w)
	if len(d.Access.Areas) != 0 {
		t.Fatal("clipped area retained")
	}
}
