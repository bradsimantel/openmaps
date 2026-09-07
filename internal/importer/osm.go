package importer

import (
	"context"
	"encoding/json"

	"fmt"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"
	"openmaps/internal/places"
)

// readStreets keeps the small regional extract's node locations in memory, then
// resolves way references. Missing nodes fail the build instead of truncating roads.
func readStreets(ctx context.Context, path, release string, bounds [4]float64) ([]Record, []map[string]string, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, nil, e
	}
	defer f.Close()
	scanner := osmpbf.New(ctx, f, 2)
	defer scanner.Close()
	scanner.SkipRelations = true
	nodes := map[osm.NodeID][2]float64{}
	ways := []*osm.Way{}
	addresses := []map[string]string{}
	for scanner.Scan() {
		switch o := scanner.Object().(type) {
		case *osm.Node:
			// OSM's coordinate precision is 1e-7 degrees. Avoid decoder arithmetic noise.
			p := [2]float64{math.Round(o.Lon*1e7) / 1e7, math.Round(o.Lat*1e7) / 1e7}
			nodes[o.ID] = p
			if inside(p, bounds) && o.Tags.Find("addr:housenumber") != "" {
				addresses = append(addresses, osmTags(o.Tags))
			}
		case *osm.Way:
			if o.Tags.Find("highway") != "" && o.Tags.Find("name") != "" {
				ways = append(ways, o)
			}
		}
	}
	if e = scanner.Err(); e != nil {
		return nil, nil, e
	}
	records := []Record{}
	for _, w := range ways {
		if e = ctx.Err(); e != nil {
			return nil, nil, e
		}
		points := make([][2]float64, 0, len(w.Nodes))
		local := [][2]float64{}
		refs := make([]int64, 0, len(w.Nodes))
		for _, node := range w.Nodes {
			p, ok := nodes[node.ID]
			if !ok {
				return nil, nil, fmt.Errorf("way %d references missing node %d", w.ID, node.ID)
			}
			points = append(points, p)
			refs = append(refs, int64(node.ID))
			if inside(p, bounds) {
				local = append(local, p)
			}
		}
		if len(local) == 0 {
			continue
		}
		p := local[len(local)/2]
		tags := osmTags(w.Tags)
		aliases := []string{}
		for _, key := range []string{"alt_name", "official_name", "short_name"} {
			for _, v := range strings.Split(tags[key], ";") {
				if v != "" {
					aliases = append(aliases, v)
				}
			}
		}
		sort.Strings(aliases)
		aliases = unique(aliases)
		records = append(records, Record{Source: "osm:way", SourceID: strconv.FormatInt(int64(w.ID), 10), Release: release, Kind: "street", Priority: 100,
			Attributes:   map[string]json.RawMessage{"name": rawValue(tags["name"]), "location": rawValue(places.Location{Lat: p[1], Lng: p[0]}), "aliases": rawValue(aliases)},
			Paths:        map[string]string{"name": "/tags/name", "location": "/nodes (middle in-bounds vertex)", "aliases": "/tags/alt_name,official_name,short_name"},
			Raw:          rawValue(map[string]any{"id": w.ID, "version": w.Version, "timestamp": w.Timestamp.UTC().Format("2006-01-02 15:04:05+00:00"), "tags": tags, "nodes": refs, "coordinates": points}),
			Attributions: []places.Attribution{{Provider: "© OpenStreetMap contributors", URI: "https://www.openstreetmap.org/copyright"}}})
	}
	return records, addresses, nil
}
func osmTags(tags osm.Tags) map[string]string {
	out := map[string]string{}
	for _, t := range tags {
		out[t.Key] = t.Value
	}
	return out
}
