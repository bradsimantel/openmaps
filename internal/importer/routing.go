package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/paulmach/osm"
	"github.com/paulmach/osm/osmpbf"
	"openmaps/internal/routing"
)

// AddRouting is only for a new unpublished build, never a retained snapshot.
// It verifies the PBF against the bundle's existing source pin before parsing.
func AddRouting(ctx context.Context, dbPath, pbf string, manifest json.RawMessage) error {
	var m Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		return err
	}
	var source Input
	for _, v := range m.Inputs {
		if v.File == filepath.Base(pbf) && strings.HasSuffix(v.File, ".osm.pbf") {
			if source.File != "" {
				return fmt.Errorf("multiple routing sources")
			}
			source = v
		}
	}
	if source.File == "" || source.File != filepath.Base(pbf) || source.Release == "" || source.Attribution == "" || !validBounds(m.BBox) {
		return fmt.Errorf("routing requires the manifest's pinned PBF and valid endpoint bounds")
	}
	if err := Verify(pbf, source.SHA256); err != nil {
		return err
	}
	d, err := readRoutingScope(ctx, pbf, source, m.BBox, !m.RoutingOnly)
	if err != nil {
		return err
	}
	if _, err = routing.New(d); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = routing.WriteGraph(ctx, tx, d); err != nil {
		return err
	}
	return tx.Commit()
}

func drivingConditional(t map[string]string) bool {
	for k := range t {
		unrelated := false
		for _, part := range strings.Split(k, ":") {
			switch part {
			case "hgv", "hgv_articulated", "bus", "psv", "emergency", "bicycle", "foot":
				unrelated = true
			}
		}
		if unrelated {
			continue
		}
		if !strings.Contains(k, ":conditional") {
			continue
		}
		for _, prefix := range []string{"access", "vehicle", "motor_vehicle", "motorcar", "oneway", "maxheight", "maxwidth", "maxweight", "maxlength", "maxaxleload"} {
			if k == prefix+":conditional" || strings.HasPrefix(k, prefix+":") {
				return true
			}
		}
	}
	return false
}
func access(t map[string]string, suffix string) string {
	for _, k := range []string{"motorcar", "motor_vehicle", "vehicle", "access"} {
		if v := t[k+suffix]; v != "" {
			return v
		}
	}
	return ""
}
func directionAccess(t map[string]string, direction string) string {
	for _, key := range []string{"motorcar", "motor_vehicle", "vehicle", "access"} {
		if v := t[key+":"+direction]; v != "" {
			return v
		}
		if v := t[key]; v != "" {
			return v
		}
	}
	return ""
}
func allowed(v string) bool { return v == "" || v == "yes" || v == "permissive" || v == "designated" }
func barrierBlocked(t map[string]string) bool {
	if drivingConditional(t) || !allowed(access(t, "")) || !dimensionsAllowed(t, "forward") || !dimensionsAllowed(t, "backward") {
		return true
	}
	b := t["barrier"]
	if b == "" || b == "no" || b == "toll_booth" || b == "cattle_grid" {
		return false
	}
	if b == "height_restrictor" && (t["maxheight"] != "" || t["maxheight:physical"] != "") {
		return false
	}
	if b == "kerb" && (t["kerb"] == "flush" || t["kerb"] == "lowered") {
		return false
	}
	if (b == "gate" || b == "lift_gate" || b == "swing_gate") && access(t, "") != "" && allowed(access(t, "")) && (t["locked"] == "" || t["locked"] == "no") {
		return false
	}
	return true
}

// Destination access is retained per direction for endpoint-aware routing.
// Other restricted access and incompatible/unknown dimensions remain closed.
func drivingWay(t map[string]string) (forward, backward, snap bool, reason string) {
	switch t["highway"] {
	case "motorway", "motorway_link", "trunk", "trunk_link", "primary", "primary_link", "secondary", "secondary_link", "tertiary", "tertiary_link", "residential", "unclassified", "living_street", "service":
	default:
		return false, false, false, "highway outside profile"
	}
	if t["area"] == "yes" || t["impassable"] == "yes" || t["status"] == "closed" || t["construction"] != "" || t["proposed"] != "" {
		return false, false, false, "non-road or closed"
	}
	if drivingConditional(t) {
		return false, false, false, "conditional access/direction/dimension closure"
	}

	if t["barrier"] != "" && barrierBlocked(t) {
		return false, false, false, "access/barrier closure"
	}
	forward, backward = true, true
	v := t["oneway"]
	for _, key := range []string{"oneway:vehicle", "oneway:motor_vehicle", "oneway:motorcar"} {
		if t[key] != "" {
			v = t[key]
		}
	}
	if v == "" && (t["junction"] == "roundabout" || t["highway"] == "motorway" || t["highway"] == "motorway_link") {
		v = "yes"
	}
	switch v {
	case "", "no", "0", "false":
	case "yes", "1", "true":
		backward = false
	case "-1":
		forward = false
	default:
		return false, false, false, "unsupported one-way closure"
	}
	forward = forward && (allowed(directionAccess(t, "forward")) || directionAccess(t, "forward") == "destination") && dimensionsAllowed(t, "forward")
	backward = backward && (allowed(directionAccess(t, "backward")) || directionAccess(t, "backward") == "destination") && dimensionsAllowed(t, "backward")
	if !forward && !backward {
		return false, false, false, "access/dimension closure"
	}
	snap = t["highway"] != "motorway" && t["highway"] != "motorway_link" && t["highway"] != "trunk" && t["highway"] != "trunk_link"
	return forward, backward, snap, "included"
}

type road struct {
	way                     *osm.Way
	forward, backward, snap bool
}
type arc struct {
	from, to int64
	way      int64
	ref      routing.EdgeRef
}

func restrictionValue(t map[string]string) (string, bool) {
	for _, except := range strings.Split(t["except"], ";") {
		switch strings.TrimSpace(except) {
		case "motorcar", "motor_vehicle", "vehicle":
			return "", false
		}
	}
	key := "restriction"
	for _, k := range []string{"restriction:vehicle", "restriction:motor_vehicle", "restriction:motorcar"} {
		if t[k] != "" || t[k+":conditional"] != "" {
			key = k
		}
	}
	v := t[key]
	conditional := t[key+":conditional"]
	if conditional != "" {
		if v != "" || strings.Count(conditional, "@") != 1 {
			return "unsupported", true
		}
		v = strings.TrimSpace(strings.SplitN(conditional, "@", 2)[0])
	}
	if v == "" { // A restriction for an unrelated transport mode is not a car restriction.
		if t["type"] != "restriction" {
			return "", false
		}
		for k := range t {
			if strings.HasPrefix(k, "restriction:") {
				return "", false
			}
		}
		return "unsupported", true
	}
	switch v {
	case "no_left_turn", "no_right_turn", "no_straight_on", "no_u_turn", "only_left_turn", "only_right_turn", "only_straight_on", "only_u_turn":
		return v, true
	}
	return "unsupported", true
}
func scanPBF(ctx context.Context, path string, skipNodes, skipWays, skipRelations bool, visit func(osm.Object) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	s := osmpbf.New(ctx, f, 2)
	defer s.Close()
	s.SkipNodes = skipNodes
	s.SkipWays = skipWays
	s.SkipRelations = skipRelations
	for s.Scan() {
		if err := visit(s.Object()); err != nil {
			return err
		}
	}
	return s.Err()
}

func readRouting(ctx context.Context, path string, source Input, bounds [4]float64) (routing.Data, error) {
	return readRoutingScope(ctx, path, source, bounds, true)
}
func readRoutingScope(ctx context.Context, path string, source Input, bounds [4]float64, addressEvidence bool) (routing.Data, error) {
	d := routing.Data{Metadata: routing.Metadata{Version: routing.GraphVersion, CostModel: routing.CostModel, Profile: routing.Profile, EndpointBounds: bounds, Release: source.Release, URL: source.URL, SourceSHA256: source.SHA256, Attribution: source.Attribution, Counts: map[string]int{}}, Nodes: []routing.Node{}, Segments: []routing.Segment{}, Bans: []routing.Ban{}, Sources: []routing.Source{}}
	roads := map[int64]road{}
	motorWays := map[int64]*osm.Way{}
	relations := []*osm.Relation{}
	need := map[int64]bool{}
	blocked := map[int64]bool{}
	// First pass needs no 5.8-million-node coordinate map; resolve only highway refs.
	err := scanPBF(ctx, path, true, false, false, func(o osm.Object) error {
		switch v := o.(type) {
		case *osm.Way:
			if v.Tags.Find("highway") == "" {
				return nil
			}
			d.Metadata.Counts["highway_ways"]++
			t := osmTags(v.Tags)
			f, b, s, reason := drivingWay(t)
			d.Sources = append(d.Sources, routing.Source{Kind: "way", ID: int64(v.ID), Version: v.Version, Raw: rawValue(v), Decision: reason})
			if reason != "highway outside profile" {
				motorWays[int64(v.ID)] = v
				for _, n := range v.Nodes {
					need[int64(n.ID)] = true
				}
			}
			if f || b {
				roads[int64(v.ID)] = road{v, f, b, s}
				for _, n := range v.Nodes {
					need[int64(n.ID)] = true
				}
			}
		case *osm.Relation:
			if strings.HasPrefix(v.Tags.Find("type"), "restriction") {
				relations = append(relations, v)
			}
		}
		return nil
	})
	if err != nil {
		return d, err
	}
	nodes := map[int64]routing.Node{}
	speedNodes := map[int64]map[string]string{}
	err = scanPBF(ctx, path, false, true, true, func(o osm.Object) error {
		n, ok := o.(*osm.Node)
		if !ok || !need[int64(n.ID)] {
			return nil
		}
		id := int64(n.ID)
		nodes[id] = routing.Node{ID: id, Point: routing.Point{math.Round(n.Lon*1e7) / 1e7, math.Round(n.Lat*1e7) / 1e7}, Version: n.Version}
		if len(n.Tags) > 0 {
			t := osmTags(n.Tags)
			decision := "node tags retained"
			for k := range t {
				if k == "maxspeed" || strings.HasPrefix(k, "maxspeed:") {
					speedNodes[id] = t
					decision = "point speed retained; conservative incident-way estimate, zone extent unknown"
					break
				}
			}
			if barrierBlocked(t) {
				blocked[id] = true
				decision = "blocked node"
			}
			d.Sources = append(d.Sources, routing.Source{Kind: "node", ID: id, Version: n.Version, Raw: rawValue(n), Decision: decision})
		}
		return nil
	})
	if err != nil {
		return d, err
	}
	// Unsupported or malformed relations close their via node, or all member ways
	// if no via node is known. Closures are explicit source decisions, not omissions.
	valid := []*osm.Relation{}
	decisions := map[int64]string{}
	for _, r := range relations {
		value, relevant := restrictionValue(osmTags(r.Tags))
		if !relevant {
			decisions[int64(r.ID)] = "other transport mode"
			continue
		}
		_, _, _, ok := restrictionMembers(r)
		if !ok || value == "unsupported" {
			closed := false
			for _, m := range r.Members {
				if m.Role == "via" && m.Type == osm.TypeNode && need[m.Ref] {
					blocked[m.Ref] = true
					closed = true
				}
			}
			if !closed {
				for _, m := range r.Members {
					if m.Type == osm.TypeWay {
						delete(roads, m.Ref)
					}
				}
			}
			decisions[int64(r.ID)] = "conservative closure: unsupported/malformed relation"
			continue
		}
		valid = append(valid, r)
	}
	ids := make([]int64, 0, len(roads))
	for id := range roads {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	outgoing := map[int64][]arc{}
	byWay := map[int64][]arc{}
	used := map[int64]bool{}
	for _, id := range ids {
		r := roads[id]
		included := false
		for i := 1; i < len(r.way.Nodes); i++ {
			a, b := int64(r.way.Nodes[i-1].ID), int64(r.way.Nodes[i].ID)
			na, aok := nodes[a]
			nb, bok := nodes[b]
			if !aok || !bok {
				return d, fmt.Errorf("routing way %d missing node", id)
			}
			if blocked[a] || blocked[b] || a == b || na.Point == nb.Point {
				continue
			}
			seg := routing.Segment{ID: strconv.FormatInt(id, 10) + ":" + strconv.Itoa(i-1), Way: id, From: a, To: b, Forward: r.forward, Backward: r.backward, Snap: r.snap, DestinationForward: directionAccess(osmTags(r.way.Tags), "forward") == "destination", DestinationBackward: directionAccess(osmTags(r.way.Tags), "backward") == "destination", Service: r.way.Tags.Find("highway") == "service", Elevated: r.way.Tags.Find("bridge") == "yes" || r.way.Tags.Find("tunnel") == "yes" || (r.way.Tags.Find("layer") != "" && r.way.Tags.Find("layer") != "0")}
			d.Segments = append(d.Segments, seg)
			used[a] = true
			used[b] = true
			included = true
			for dir, allow := range []bool{r.forward, r.backward} {
				if !allow {
					continue
				}
				v := arc{a, b, id, routing.EdgeRef{Segment: seg.ID, Reverse: dir == 1}}
				if dir == 1 {
					v.from, v.to = v.to, v.from
				}
				outgoing[v.from] = append(outgoing[v.from], v)
				byWay[id] = append(byWay[id], v)
			}
		}
		if included {
			t := osmTags(r.way.Tags)
			c := routing.WayCost{Way: id, Forward: drivingSpeed(t, "forward"), Backward: drivingSpeed(t, "backward")}
			for _, n := range r.way.Nodes {
				if nt, ok := speedNodes[int64(n.ID)]; ok {
					copyTags := map[string]string{"highway": t["highway"], "service": t["service"], "surface": t["surface"], "junction": t["junction"]}
					for k, v := range nt {
						if k == "maxspeed" || strings.HasPrefix(k, "maxspeed:") {
							copyTags[k] = v
						}
					}
					// A point's direction/zone extent is unresolved. Cap the
					// whole incident way in both directions, without asserting
					// that the point establishes a legal way-wide ceiling.
					f, b := drivingSpeed(copyTags, "forward"), drivingSpeed(copyTags, "backward")
					cap := math.Min(f.KPH, b.KPH)
					for _, v := range []*routing.Speed{&c.Forward, &c.Backward} {
						v.KPH = math.Min(v.KPH, cap)
						v.Notes = append(v.Notes, fmt.Sprintf("point speed node %d: conservative incident-way cap; zone/direction unknown", n.ID))
					}
				}
			}
			d.Costs = append(d.Costs, c)
			d.Metadata.Counts["included_ways"]++
			if r.way.Tags.Find("name") == "" {
				d.Metadata.Counts["included_unnamed_ways"]++
			}
		}
	}
	// Retain excluded motor-road pieces as snap guards, including barrier approaches.
	retained := map[string]bool{}
	for _, seg := range d.Segments {
		retained[seg.ID] = true
	}
	motorIDs := make([]int64, 0, len(motorWays))
	for id := range motorWays {
		motorIDs = append(motorIDs, id)
	}
	sort.Slice(motorIDs, func(i, j int) bool { return motorIDs[i] < motorIDs[j] })
	for _, id := range motorIDs {
		w := motorWays[id]
		for i := 1; i < len(w.Nodes); i++ {
			key := strconv.FormatInt(id, 10) + ":" + strconv.Itoa(i-1)
			if retained[key] {
				continue
			}
			a, aok := nodes[int64(w.Nodes[i-1].ID)]
			b, bok := nodes[int64(w.Nodes[i].ID)]
			if !aok || !bok {
				return d, fmt.Errorf("snap guard way %d missing node", id)
			}
			if a.Point != b.Point {
				d.Guards = append(d.Guards, routing.Guard{Segment: key, Way: id, From: a.Point, To: b.Point})
			}
		}
	}
	for _, r := range valid {
		value, _ := restrictionValue(osmTags(r.Tags))
		from, to, via, _ := restrictionMembers(r)
		paths, err := restrictionPaths(from, to, via, byWay, outgoing)
		if err != nil {
			return d, fmt.Errorf("relation %d: %w", r.ID, err)
		}
		if value == "only_u_turn" && from == to && len(via) == 1 && via[0].Type == osm.TypeNode {
			filtered := paths[:0]
			for _, path := range paths {
				if path[0].ref.Segment == path[1].ref.Segment {
					filtered = append(filtered, path)
				}
			}
			paths = filtered
		}
		if len(paths) == 0 {
			// An excluded only-turn target must not free the approach. Block all
			// departures after its from-way at the first via member's source nodes.
			if strings.HasPrefix(value, "only_") {
				entry := map[int64]bool{}
				if via[0].Type == osm.TypeNode {
					entry[via[0].Ref] = true
				} else if road, ok := roads[via[0].Ref]; ok {
					for _, n := range road.way.Nodes {
						entry[int64(n.ID)] = true
					}
				} else { // The via way itself was excluded: close any from-way departure.
					for _, a := range byWay[from] {
						entry[a.to] = true
					}
				}
				for _, a := range byWay[from] {
					if entry[a.to] {
						for _, b := range outgoing[a.to] {
							d.Bans = append(d.Bans, routing.Ban{Relation: int64(r.ID), Path: []routing.EdgeRef{a.ref, b.ref}})
						}
					}
				}
			}
			decisions[int64(r.ID)] = "no traversable maneuver; only-turn approaches remain blocked"
			continue
		}
		if strings.HasPrefix(value, "only_") { // Union valid continuations for each prefix of this relation.
			type prefix struct {
				path    []routing.EdgeRef
				node    int64
				allowed map[routing.EdgeRef]bool
			}
			prefixes := map[string]*prefix{}
			for _, path := range paths {
				for i := 1; i < len(path); i++ {
					refs := []routing.EdgeRef{}
					for _, a := range path[:i] {
						refs = append(refs, a.ref)
					}
					key := serialized(refs)
					p := prefixes[key]
					if p == nil {
						p = &prefix{refs, path[i-1].to, map[routing.EdgeRef]bool{}}
						prefixes[key] = p
					}
					p.allowed[path[i].ref] = true
				}
			}
			keys := sortedKeys(prefixes)
			for _, key := range keys {
				p := prefixes[key]
				for _, a := range outgoing[p.node] {
					if !p.allowed[a.ref] {
						seq := append(append([]routing.EdgeRef{}, p.path...), a.ref)
						d.Bans = append(d.Bans, routing.Ban{Relation: int64(r.ID), Path: seq})
					}
				}
			}
		} else {
			for _, path := range paths {
				if value == "no_u_turn" && from == to && len(path) == 2 && path[0].ref.Segment != path[1].ref.Segment {
					continue
				}
				refs := []routing.EdgeRef{}
				for _, a := range path {
					refs = append(refs, a.ref)
				}
				d.Bans = append(d.Bans, routing.Ban{Relation: int64(r.ID), Path: refs})
			}
		}
		decisions[int64(r.ID)] = "enforced"
		if strings.Contains(serialized(osmTags(r.Tags)), ":conditional") {
			decisions[int64(r.ID)] = "enforced at all times: condition not evaluated"
		}
		d.Metadata.Counts["enforced_relations"]++
	}
	for _, r := range relations {
		decision := decisions[int64(r.ID)]
		d.Sources = append(d.Sources, routing.Source{Kind: "relation", ID: int64(r.ID), Version: r.Version, Raw: rawValue(r), Decision: decision})
		d.Metadata.Counts[decision]++
	}
	nodeIDs := make([]int64, 0, len(used))
	for id := range used {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Slice(nodeIDs, func(i, j int) bool { return nodeIDs[i] < nodeIDs[j] })
	gb := [4]float64{180, 90, -180, -90}
	for _, id := range nodeIDs {
		n := nodes[id]
		d.Nodes = append(d.Nodes, n)
		gb[0] = math.Min(gb[0], n.Point[0])
		gb[1] = math.Min(gb[1], n.Point[1])
		gb[2] = math.Max(gb[2], n.Point[0])
		gb[3] = math.Max(gb[3], n.Point[1])
	}
	d.Metadata.GraphBounds = gb
	if addressEvidence {
		if err := readRoutingAccess(ctx, path, &d); err != nil {
			return d, err
		}
	}
	sort.Slice(d.Sources, func(i, j int) bool {
		a, b := d.Sources[i], d.Sources[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.ID < b.ID
	})
	d.Metadata.Counts["nodes"] = len(d.Nodes)
	d.Metadata.Counts["segments"] = len(d.Segments)
	d.Metadata.Counts["bans"] = len(d.Bans)
	d.Metadata.Counts["blocked_nodes"] = len(blocked)
	d.Metadata.Counts["snap_guards"] = len(d.Guards)
	for _, seg := range d.Segments {
		if seg.DestinationForward || seg.DestinationBackward {
			d.Metadata.Counts["destination_segments"]++
		}
	}
	return d, ctx.Err()
}

func restrictionMembers(r *osm.Relation) (from, to int64, via []osm.Member, ok bool) {
	ok = true
	for _, m := range r.Members {
		switch m.Role {
		case "from":
			if m.Type != osm.TypeWay || from != 0 {
				ok = false
			}
			from = m.Ref
		case "to":
			if m.Type != osm.TypeWay || to != 0 {
				ok = false
			}
			to = m.Ref
		case "via":
			via = append(via, m)
		default:
			ok = false
		}
	}
	if from == 0 || to == 0 || len(via) == 0 {
		ok = false
	}
	for _, m := range via {
		if m.Type != osm.TypeWay && (m.Type != osm.TypeNode || len(via) != 1) {
			ok = false
		}
	}
	return
}

// Enumerate directed contiguous paths through ordered via ways, retaining every
// intermediate segment. No geometry intersection or coordinate equality creates a junction.
func restrictionPaths(from, to int64, via []osm.Member, byWay, outgoing map[int64][]arc) ([][]arc, error) {
	paths := [][]arc{}
	if len(via) == 1 && via[0].Type == osm.TypeNode {
		for _, a := range byWay[from] {
			if a.to != via[0].Ref {
				continue
			}
			for _, b := range outgoing[a.to] {
				if b.way == to {
					paths = append(paths, []arc{a, b})
				}
			}
		}
		return paths, nil
	}
	steps := 0
	var walk func([]arc, int, map[routing.EdgeRef]bool) error
	walk = func(path []arc, stage int, seen map[routing.EdgeRef]bool) error {
		steps++
		if steps > 100000 || len(path) > 4096 {
			return fmt.Errorf("restriction path enumeration limit")
		}
		last := path[len(path)-1]
		for _, a := range outgoing[last.to] {
			if a.ref.Segment == last.ref.Segment || seen[a.ref] {
				continue
			}
			if a.way == via[stage].Ref {
				seen[a.ref] = true
				if err := walk(append(path, a), stage, seen); err != nil {
					return err
				}
				delete(seen, a.ref)
			}
			if last.way == via[stage].Ref {
				if stage == len(via)-1 && a.way == to {
					p := append(append([]arc{}, path...), a)
					paths = append(paths, p)
				} else if stage+1 < len(via) && a.way == via[stage+1].Ref {
					seen[a.ref] = true
					if err := walk(append(path, a), stage+1, seen); err != nil {
						return err
					}
					delete(seen, a.ref)
				}
			}
		}
		return nil
	}
	for _, a := range byWay[from] {
		if err := walk([]arc{a}, 0, map[routing.EdgeRef]bool{a.ref: true}); err != nil {
			return nil, err
		}
	}
	return paths, nil
}
