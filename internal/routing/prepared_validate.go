package routing

import (
	"context"
	"fmt"
	"math"
	"unsafe"
)

// Runtime validates extents and references with constant scan scratch. Semantic
// equivalence of topology, forced chains and cheapest cell paths is established
// by canonical offline preparation and the separately trusted publication digest.
func (s *Store) validatePrepared(ctx context.Context) error {
	bad := func(what string) error { return fmt.Errorf("invalid prepared %s", what) }
	if unsafe.Sizeof(spatialNode{}) != 48 || unsafe.Offsetof(spatialNode{}.left) != 32 || unsafe.Offsetof(spatialNode{}.right) != 36 || unsafe.Offsetof(spatialNode{}.start) != 40 || unsafe.Offsetof(spatialNode{}.end) != 44 {
		return bad("spatial ABI")
	}
	n, m, k := len(s.points), len(s.edges), s.segmentCount()
	if n == 0 || n > math.MaxInt32 || m == 0 || m > math.MaxInt32 || k == 0 || len(s.offsets) != n+1 || len(s.adjacency) != m || len(s.directions) != k || len(s.continuation) != m || len(s.zones) != k || len(s.components) != n || len(s.junctions) != n {
		return bad("array dimensions")
	}
	if e := validatePreparedMetadata(s.meta); e != nil {
		return e
	}
	slots := s.prepared.nodes
	if len(slots) == 0 || len(slots)&(len(slots)-1) != 0 || len(slots) < 2*n {
		return bad("node table dimensions")
	}
	// Every occupied slot must be reachable in its probe sequence. A maximum probe
	// length bounds malformed-input work and query lookup; preparation uses 50% fill.
	occupied := 0
	for i, v := range slots {
		if i%4096 == 0 {
			if e := ctx.Err(); e != nil {
				return e
			}
		}
		if v[0] == 0 {
			if v[1] != 0 {
				return bad("empty node slot")
			}
			continue
		}
		occupied++
		if v[0] > math.MaxInt64 || uint32(v[1]) >= uint32(n) || v[1]>>34 != 0 {
			return bad("node reference")
		}
		j := int(nodeHash(v[0])) & (len(slots) - 1)
		steps := 0
		for j != i {
			if slots[j][0] == 0 || slots[j][0] == v[0] || steps >= 1024 {
				return bad("node probe")
			}
			j = (j + 1) & (len(slots) - 1)
			steps++
		}
	}
	if occupied != n {
		return bad("node population")
	}
	stringRange := func(off, count uint64) bool {
		return off <= uint64(len(s.prepared.strings)) && count > 0 && count <= uint64(len(s.prepared.strings))-off
	}
	for i, v := range s.prepared.segments {
		if i%4096 == 0 {
			if e := ctx.Err(); e != nil {
				return e
			}
		}
		if v[0] == 0 || v[0] > math.MaxInt64 || !stringRange(v[3], v[4]) || v[5] > 127 || v[5]&3 == 0 {
			return bad("segment fields")
		}
		if _, ok := s.findNode(int64(v[1])); !ok {
			return bad("segment from")
		}
		if _, ok := s.findNode(int64(v[2])); !ok || v[1] == v[2] {
			return bad("segment to")
		}
		if s.zones[i] < 0 || s.zones[i] > k {
			return bad("destination zone")
		}
		for dir, id := range s.directions[i] {
			if id < -1 || id >= m {
				return bad("direction reference")
			}
			if id >= 0 && (s.edges[id].segment != i || s.edges[id].reverse != (dir == 1)) {
				return bad("direction identity")
			}
		}
	}
	for i, v := range s.prepared.guards {
		if i%4096 == 0 {
			if e := ctx.Err(); e != nil {
				return e
			}
		}
		if v[0] == 0 || v[0] > math.MaxInt64 || !stringRange(v[5], v[6]) || !(Point{math.Float64frombits(v[1]), math.Float64frombits(v[2])}).Valid() || !(Point{math.Float64frombits(v[3]), math.Float64frombits(v[4])}).Valid() {
			return bad("guard")
		}
	}
	if s.offsets[0] != 0 || int(s.offsets[n]) != m {
		return bad("CSR extent")
	}
	for i, p := range s.points {
		if i%4096 == 0 {
			if e := ctx.Err(); e != nil {
				return e
			}
		}
		if !p.Valid() || s.offsets[i] > s.offsets[i+1] || int(s.offsets[i+1]) > m || s.components[i] < 0 || int(s.components[i]) >= n || s.junctions[i] > 2 {
			return bad("node/CSR")
		}
		for _, id := range s.adjacency[s.offsets[i]:s.offsets[i+1]] {
			if id < 0 || id >= m {
				return bad("adjacency reference")
			}
			ordinal, ok := s.findNode(s.edges[id].from)
			if !ok || int(ordinal) != i {
				return bad("adjacency source")
			}
		}
	}
	validEdge := func(id int32) bool { return id >= 0 && int(id) < m }
	for i, v := range s.edges {
		if i%4096 == 0 {
			if e := ctx.Err(); e != nil {
				return e
			}
		}
		if v.segment < 0 || v.segment >= k || !(v.length > 0) || math.IsInf(v.length, 0) || math.IsNaN(v.seconds) || math.IsInf(v.seconds, 0) || s.HasDuration() && v.seconds <= 0 || v.cellEntry < -2 || int(v.cellEntry) >= len(s.cellEntries) {
			return bad("edge")
		}
		seg := s.prepared.segments[v.segment]
		a, b := int64(seg[1]), int64(seg[2])
		dir := 0
		if v.reverse {
			a, b = b, a
			dir = 1
		}
		if v.from != a || v.to != b || s.directions[v.segment][dir] != i {
			return bad("edge endpoints")
		}
		next := s.continuation[i]
		if next < -1 || int(next) >= m || next >= 0 && s.edges[next].from != v.to {
			return bad("continuation")
		}
	}
	for i, p := range s.cellPaths {
		if i%4096 == 0 {
			if e := ctx.Err(); e != nil {
				return e
			}
		}
		if p.parent < -1 || int(p.parent) >= i || !validEdge(p.first) || !validEdge(p.last) {
			return bad("recursive path")
		}
	}
	for i, e := range s.cellEntries {
		if i%4096 == 0 {
			if e := ctx.Err(); e != nil {
				return e
			}
		}
		if !validEdge(e.edge) || e.cell < 0 || int(e.cell) >= len(s.cellBounds) || e.offset < 0 || e.count < 0 || int64(e.offset)+int64(e.count) > int64(len(s.cellTransfers)) {
			return bad("cell entrance")
		}
	}
	for i, t := range s.cellTransfers {
		if i%4096 == 0 {
			if e := ctx.Err(); e != nil {
				return e
			}
		}
		if !(t.cost > 0) || math.IsInf(t.cost, 0) || t.path < 0 || int(t.path) >= len(s.cellPaths) || !validEdge(t.last) || s.cellPaths[t.path].last != t.last {
			return bad("cell transfer")
		}
	}
	for _, b := range s.cellBounds {
		if !(Point{b.minX, b.minY}).Valid() || !(Point{b.maxX, b.maxY}).Valid() || b.minX > b.maxX || b.minY > b.maxY {
			return bad("cell bounds")
		}
	}
	for i, index := range []spatialIndex{s.segmentIndex, s.guardIndex, s.areaIndex, s.accessIndex} {
		count := []int{k, len(s.prepared.guards), len(s.access.Areas), len(s.access.Ways)}[i]
		if len(index.ids) != count || (count == 0) != (len(index.nodes) == 0) {
			return bad("spatial dimensions")
		}
		for _, id := range index.ids {
			if id < 0 || int(id) >= count {
				return bad("spatial feature")
			}
		}
		for j, v := range index.nodes {
			if j%4096 == 0 {
				if e := ctx.Err(); e != nil {
					return e
				}
			}
			if v.start < 0 || v.end < v.start || int(v.end) > count {
				return bad("spatial range")
			}
			if v.left < 0 {
				if v.left != -1 || v.right != -1 || v.end-v.start > 16 {
					return bad("spatial leaf")
				}
			} else if int(v.left) <= j || int(v.right) <= j || int(v.left) >= len(index.nodes) || int(v.right) >= len(index.nodes) {
				return bad("recursive spatial node")
			}
		}
	}
	if len(s.trie) == 0 || s.trie[0].fail != 0 || s.trie[0].banned {
		return bad("restriction root")
	}
	for i, t := range s.trie {
		if i%4096 == 0 {
			if e := ctx.Err(); e != nil {
				return e
			}
		}
		if t.fail < 0 || t.fail >= len(s.trie) {
			return bad("restriction failure")
		}
		for edge, next := range t.next {
			if edge < 0 || edge >= m || next <= 0 || next >= len(s.trie) {
				return bad("restriction transition")
			}
		}
		v := i
		for depth := 0; v != 0; depth++ {
			if depth%4096 == 0 {
				if e := ctx.Err(); e != nil {
					return e
				}
			}
			if depth >= len(s.trie) {
				return bad("restriction failure cycle")
			}
			v = s.trie[v].fail
		}
	}
	// Access geometry was source-validated offline. Retain inexpensive dimensions
	// and coordinate checks, avoiding rebuilding graph-sized association indexes.
	for _, w := range s.access.Ways {
		if len(w.Nodes) != len(w.Geometry) {
			return bad("access way dimensions")
		}
		for _, p := range w.Geometry {
			if !p.Valid() {
				return bad("access geometry")
			}
		}
	}
	for _, a := range s.access.Areas {
		if len(a.Nodes) != len(a.Geometry) || len(a.Nodes) < 4 {
			return bad("access area dimensions")
		}
		for _, p := range a.Geometry {
			if !p.Valid() {
				return bad("access area geometry")
			}
		}
	}
	for _, e := range s.access.Entrances {
		if !e.Point.Valid() {
			return bad("access entrance")
		}
	}
	for _, links := range s.driveways {
		for _, v := range links {
			if v.to <= 0 || v.length < 0 || math.IsNaN(v.length) || math.IsInf(v.length, 0) {
				return bad("driveway link")
			}
		}
	}
	if math.IsNaN(s.maxMetersPerSecond) || math.IsInf(s.maxMetersPerSecond, 0) || s.HasDuration() && s.maxMetersPerSecond <= 0 {
		return bad("heuristic speed")
	}
	return ctx.Err()
}

func validatePreparedMetadata(meta Metadata) error {
	if !((meta.Version == 1 && meta.Profile == "driving-distance-v1") || (meta.Version == 2 && meta.Profile == "driving-distance-v2") || (meta.Version == 3 && meta.Profile == "driving-distance-v3") || (meta.Version == GraphVersion && meta.Profile == Profile && meta.CostModel == CostModel)) {
		return fmt.Errorf("invalid prepared graph/profile/cost version")
	}
	if meta.Version < GraphVersion && meta.CostModel != "" {
		return fmt.Errorf("invalid prepared legacy cost model")
	}
	bounds := meta.EndpointBounds
	if !(Point{bounds[0], bounds[1]}).Valid() || !(Point{bounds[2], bounds[3]}).Valid() || bounds[0] >= bounds[2] || bounds[1] >= bounds[3] {
		return fmt.Errorf("invalid prepared endpoint bounds")
	}
	return nil
}
