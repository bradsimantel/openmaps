package routing

// JunctionPreprocessingVersion fixes chains, the independent-junction fallback,
// bounded 32-junction cells, edge-state transfers and terminal-approach pruning.
// Restriction/access boundaries remain explicit in this partial hierarchy.
const JunctionPreprocessingVersion = "junction-cells-v1"

// buildJunctions stores a factorized shortcut table. At each selected junction,
// every incoming/outgoing pair except immediate segment reversal is represented
// by its two original edges. Queries expand that pair without queueing the
// intermediate junction. Original continuations unpack the geometry on each side.
// No turn-history or destination state is collapsed into a node label.
func (s *Store) buildJunctions() {
	s.junctions = make([]uint8, len(s.points))
	s.components = make([]int32, len(s.points))
	for i := range s.components {
		s.components[i] = int32(i)
	}
	root := func(v int32) int32 {
		for s.components[v] != v {
			s.components[v] = s.components[s.components[v]]
			v = s.components[v]
		}
		return v
	}
	// Weak connectivity is only a negative filter, never proof of a legal route.
	for _, e := range s.edges {
		a, b := root(s.nodeIndex[e.from]), root(s.nodeIndex[e.to])
		if a != b {
			if a > b {
				a, b = b, a
			}
			s.components[b] = a
		}
	}
	for i := range s.components {
		s.components[i] = root(int32(i))
	}
	// Candidates have real branching and no incident restriction/access boundary.
	for node, i := range s.nodeIndex {
		degree := len(s.out(node))
		if degree >= 3 && degree <= 4 && !s.restrictedNodes[node] {
			s.junctions[i] = 1
		}
	}
	for _, e := range s.edges {
		if s.zones[e.segment] != 0 {
			s.junctions[s.nodeIndex[e.from]] = 0
			s.junctions[s.nodeIndex[e.to]] = 0
		}
	}
	// Stable directed-edge order picks an independent set on the chain core.
	// Marking neighbors also across one-way chains keeps the shortcut product small.
	blocked := make([]bool, len(s.points))
	ends := make([]int32, len(s.edges))
	for i := range ends {
		ends[i] = -1
	}
	for start := range s.edges {
		if ends[start] >= 0 {
			continue
		}
		path := []int{}
		v := start
		for ends[v] < 0 && s.continuation[v] >= 0 {
			path = append(path, v)
			v = int(s.continuation[v])
		}
		end := v
		if ends[v] >= 0 {
			end = int(ends[v])
		} else {
			ends[v] = int32(v)
		}
		for _, id := range path {
			ends[id] = int32(end)
		}
	}
	// First block both endpoints of all core connections after selecting one.
	neighbors := make(map[int32][]int32)
	for id, e := range s.edges {
		a := s.nodeIndex[e.from]
		if s.junctions[a] == 0 {
			continue
		}
		b := s.nodeIndex[s.edges[ends[id]].to]
		if s.junctions[b] != 0 {
			neighbors[a] = append(neighbors[a], b)
			neighbors[b] = append(neighbors[b], a)
		}
	}
	for _, e := range s.edges {
		i := s.nodeIndex[e.from]
		if s.junctions[i] != 1 {
			continue
		}
		if blocked[i] {
			s.junctions[i] = 0
			continue
		}
		s.junctions[i] = 2
		for _, n := range neighbors[i] {
			blocked[n] = true
		}
	}
}
