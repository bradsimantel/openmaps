package importer

import (
	"context"
	"math"
	"sort"

	"github.com/paulmach/osm"
	"openmaps/internal/routing"
)

// Retain bounded endpoint evidence from the pinned PBF, independently of lookup
// identities. OSM elements are ordered nodes/ways/relations in this extract.
// Keep only complete local geometries; no clipped ring may imply containment.
func readRoutingAccess(ctx context.Context, path string, d *routing.Data) error {
	nodes := map[int64]routing.Point{}
	b := d.Metadata.EndpointBounds
	sources := map[string]map[int64]bool{"node": {}, "way": {}}
	for _, s := range d.Sources {
		if sources[s.Kind] != nil {
			sources[s.Kind][s.ID] = true
		}
	}
	addSource := func(kind string, id int64, version int, raw any) {
		if !sources[kind][id] {
			d.Sources = append(d.Sources, routing.Source{Kind: kind, ID: id, Version: version, Raw: rawValue(raw), Decision: "endpoint association evidence"})
			sources[kind][id] = true
		}
	}
	err := scanPBF(ctx, path, false, false, true, func(o osm.Object) error {
		switch v := o.(type) {
		case *osm.Node:
			if v.Lon < b[0]-.002 || v.Lon > b[2]+.002 || v.Lat < b[1]-.002 || v.Lat > b[3]+.002 {
				return nil
			}
			p := routing.Point{math.Round(v.Lon*1e7) / 1e7, math.Round(v.Lat*1e7) / 1e7}
			nodes[int64(v.ID)] = p
			if v.Tags.Find("amenity") == "parking_entrance" {
				d.Access.Entrances = append(d.Access.Entrances, routing.AccessEntrance{Node: int64(v.ID), Point: p})
				addSource("node", int64(v.ID), v.Version, v)
			}
		case *osm.Way:
			t := osmTags(v.Tags)
			_, _, _, reason := drivingWay(t)
			road := reason != "highway outside profile"
			area := (t["building"] != "" && t["building"] != "no" || t["amenity"] == "parking") && len(v.Nodes) >= 4 && v.Nodes[0].ID == v.Nodes[len(v.Nodes)-1].ID
			if !road && !area {
				return nil
			}
			ids := []int64{}
			geometry := []routing.Point{}
			for _, n := range v.Nodes {
				p, ok := nodes[int64(n.ID)]
				if !ok {
					if road && t["name"] != "" && t["service"] != "driveway" {
						d.Access.Ways = append(d.Access.Ways, routing.AccessWay{Way: int64(v.ID), Name: t["name"]})
					}
					return nil
				}
				ids = append(ids, int64(n.ID))
				geometry = append(geometry, p)
			}
			if len(ids) < 2 {
				return nil
			}
			if road {
				d.Access.Ways = append(d.Access.Ways, routing.AccessWay{Way: int64(v.ID), Name: t["name"], Driveway: t["highway"] == "service" && t["service"] == "driveway", Nodes: ids, Geometry: geometry})
			}
			if area {
				d.Access.Areas = append(d.Access.Areas, routing.AccessArea{Restricted: t["amenity"] == "parking" && (!allowed(directionAccess(t, "forward")) || !allowed(directionAccess(t, "backward")) || barrierBlocked(t)), Way: int64(v.ID), Parking: t["amenity"] == "parking", Number: t["addr:housenumber"], Street: t["addr:street"], Nodes: ids, Geometry: geometry})
				addSource("way", int64(v.ID), v.Version, v)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(d.Access.Ways, func(i, j int) bool { return d.Access.Ways[i].Way < d.Access.Ways[j].Way })
	sort.Slice(d.Access.Areas, func(i, j int) bool { return d.Access.Areas[i].Way < d.Access.Areas[j].Way })
	sort.Slice(d.Access.Entrances, func(i, j int) bool { return d.Access.Entrances[i].Node < d.Access.Entrances[j].Node })
	d.Metadata.Counts["access_ways"] = len(d.Access.Ways)
	d.Metadata.Counts["access_areas"] = len(d.Access.Areas)
	d.Metadata.Counts["parking_entrances"] = len(d.Access.Entrances)
	return nil
}
