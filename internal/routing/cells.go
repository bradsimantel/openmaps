package routing

import (
	"context"
	"fmt"
	"math"
	"sort"
)

// Bounded junction cells add a second overlay above forced geometry chains.
// Local Dijkstra labels are incoming directed edges, NEVER just source nodes.
// All prohibited-path nodes and access-zone incident nodes remain outside cells.
// Thus every transferred edge is absent from the restriction alphabet, resets
// any incoming automaton history to zero, and permits only public phases 0/1.
const junctionCellLimit = 32

// A terminal approach has only its prohibited immediate reversal as an exit.
// Query endpoints reopen it; it is never a through-route overlay boundary.
const terminalCellEntry int32 = -2

type cellBounds struct{ minX, minY, maxX, maxY float64 }

func (b cellBounds) contains(p Point) bool {
	return p[0] >= b.minX && p[0] <= b.maxX && p[1] >= b.minY && p[1] <= b.maxY
}
func (b *cellBounds) include(p Point) {
	b.minX = math.Min(b.minX, p[0])
	b.minY = math.Min(b.minY, p[1])
	b.maxX = math.Max(b.maxX, p[0])
	b.maxY = math.Max(b.maxY, p[1])
}

type cellEntry struct{ edge, cell, offset, count int32 }
type cellTransfer struct {
	cost       float64
	path, last int32
}

// Paths form an acyclic recursive DAG: parent always precedes child. Each leaf
// range expands a forced chain from first through last, in original source order.
type cellPath struct{ parent, first, last int32 }
type cellArc struct {
	first, last int
	cost        float64
}

func (s *Store) buildCells(ctx context.Context) error {
	// Dense scratch identifiers exist only for eligible junctions, not geometry.
	nodes := []int64{}
	index := map[int64]int{}
	for ordinal, e := range s.edges {
		if ordinal%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if s.continuation[ordinal] >= 0 {
			continue
		}
		if _, ok := index[e.to]; ok {
			continue
		}
		out := s.out(e.to)
		degree := len(out)
		if degree == 0 || degree == 1 && s.edges[out[0]].segment == e.segment {
			s.edges[ordinal].cellEntry = terminalCellEntry
		}
		if degree < 2 || degree > 4 || s.restrictedNodes[e.to] {
			continue
		}
		safe := true
		for _, id := range s.out(e.to) {
			if s.zones[s.edges[id].segment] != 0 {
				safe = false
				break
			}
		}
		if safe {
			index[e.to] = len(nodes)
			nodes = append(nodes, e.to)
		}
	}
	// Incoming-only destination edges must also pin a junction.
	for _, e := range s.edges {
		if s.zones[e.segment] != 0 {
			delete(index, e.to)
			delete(index, e.from)
		}
	}
	parent, size := make([]int, len(nodes)), make([]int, len(nodes))
	arcs := make([][]cellArc, len(nodes))
	bounds := make([]cellBounds, len(nodes))
	root := func(v int) int {
		for parent[v] != v {
			parent[v] = parent[parent[v]]
			v = parent[v]
		}
		return v
	}
	for i, node := range nodes {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		parent[i] = i
		size[i] = 1
		bounds[i] = cellBounds{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
		if _, ok := index[node]; !ok {
			continue
		}
		bounds[i].include(s.point(node))
		for _, first := range s.out(node) {
			a := cellArc{first: first, last: first}
			for id, steps := first, 0; ; id, steps = int(s.continuation[id]), steps+1 {
				if steps%4096 == 0 {
					if err := ctx.Err(); err != nil {
						return err
					}
				}
				e := s.edges[id]
				if s.HasDuration() {
					a.cost += e.length * e.seconds / e.length
				} else {
					a.cost += e.length
				}
				bounds[i].include(s.point(e.to))
				a.last = id
				if s.continuation[id] < 0 {
					break
				}
			}
			arcs[i] = append(arcs[i], a)
		}
	}
	// Deterministic bounded unions over source-ordered directed chain arcs. Cells
	// may contain cycles; local edge-state Dijkstra handles them without witnesses.
	for i := range nodes {
		for _, a := range arcs[i] {
			j, ok := index[s.edges[a.last].to]
			if !ok {
				continue
			}
			x, y := root(i), root(j)
			if x == y || size[x]+size[y] > junctionCellLimit {
				continue
			}
			if x > y {
				x, y = y, x
			}
			parent[y] = x
			size[x] += size[y]
		}
	}
	for i := range nodes {
		parent[i] = root(i)
	}
	cells := map[int]int32{}
	for i, node := range nodes {
		if _, ok := index[node]; !ok {
			continue
		}
		r := parent[i]
		c, ok := cells[r]
		if !ok {
			c = int32(len(s.cellBounds))
			cells[r] = c
			s.cellBounds = append(s.cellBounds, cellBounds{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)})
		}
		s.cellBounds[c].include(Point{bounds[i].minX, bounds[i].minY})
		s.cellBounds[c].include(Point{bounds[i].maxX, bounds[i].maxY})
	}
	internal := make(map[int]bool)
	for i := range nodes {
		for _, arc := range arcs[i] {
			if j, ok := index[s.edges[arc.last].to]; ok && parent[i] == parent[j] {
				internal[arc.last] = true
			}
		}
	}
	type localLabel struct {
		cost          float64
		parent, first int
	}
	// Group entrances deterministically so equal recursive prefixes can be
	// interned with cell-local scratch memory, then released before the next cell.
	incomingByCell := make([][]int, len(s.cellBounds))
	for incoming, e := range s.edges {
		if incoming%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		start, ok := index[e.to]
		if ok && !internal[incoming] {
			c := cells[parent[start]]
			incomingByCell[c] = append(incomingByCell[c], incoming)
		}
	}
	for _, incomingEdges := range incomingByCell {
		intern := map[cellPath]int32{}
		for _, incoming := range incomingEdges {
			if err := ctx.Err(); err != nil {
				return err
			}
			start := index[s.edges[incoming].to]
			cell := parent[start]
			labels := map[int]localLabel{incoming: {parent: -1, first: -1}}
			q := &queue{}
			q.push(item{state: state{edge: incoming}})
			exits := []int{}
			for q.Len() > 0 {
				cur := q.pop()
				best := cur.state.edge
				if cur.distance != labels[best].cost {
					continue
				}
				v := labels[best]
				node, inside := index[s.edges[best].to]
				if !inside || parent[node] != cell {
					if s.edges[best].cellEntry != terminalCellEntry {
						exits = append(exits, best)
					}
					continue
				}
				for _, a := range arcs[node] {
					if s.edges[a.first].segment == s.edges[best].segment {
						continue
					}
					cost := v.cost + a.cost
					old, seen := labels[a.last]
					if !seen || cost < old.cost {
						labels[a.last] = localLabel{cost: cost, parent: best, first: a.first}
						q.push(item{state: state{edge: a.last}, distance: cost, priority: cost})
					}
				}
			}
			// Recursive paths and transfer offsets are signed 32-bit persisted indices.
			// Bound before appending this entry; never permit silent index wrapping.
			if len(s.cellPaths)+len(labels) > math.MaxInt32-2 || len(s.cellTransfers)+len(exits) > math.MaxInt32 || len(s.cellEntries) >= math.MaxInt32 {
				return fmt.Errorf("routing cell capacity exceeded")
			}
			entry := cellEntry{int32(incoming), cells[cell], int32(len(s.cellTransfers)), int32(len(exits))}
			paths := map[int]int32{incoming: -1}
			var save func(int) int32
			save = func(id int) int32 {
				if p, ok := paths[id]; ok {
					return p
				}
				v := labels[id]
				p := save(v.parent)
				record := cellPath{p, int32(v.first), int32(id)}
				next, exists := intern[record]
				if !exists {
					next = int32(len(s.cellPaths))
					s.cellPaths = append(s.cellPaths, record)
					intern[record] = next
				}
				paths[id] = next
				return next
			}
			sort.Ints(exits)
			for _, id := range exits {
				p := save(id)
				s.cellTransfers = append(s.cellTransfers, cellTransfer{labels[id].cost, p, int32(id)})
			}
			s.edges[incoming].cellEntry = int32(len(s.cellEntries))
			s.cellEntries = append(s.cellEntries, entry)
		}
	}
	return nil
}

func (s *Store) cellEscapes(edge int, a, b Point) ([]cellTransfer, bool) {
	i := s.edges[edge].cellEntry
	if i < 0 {
		return nil, false
	}
	e := s.cellEntries[i]
	bounds := s.cellBounds[e.cell]
	// Conservatively open an entire cell near either target-segment vertex. Its
	// bounds include all interior AND exit chain geometry. False positives only
	// increase work; no partial endpoint can be hidden by an aggregate transfer.
	if bounds.contains(a) || bounds.contains(b) {
		return nil, false
	}
	return s.cellTransfers[e.offset : e.offset+e.count], true
}
