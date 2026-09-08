package routing

// This is the bottom level of a topology hierarchy, not full CH. A directed
// edge can skip the unique non-reversing continuation only away from every
// prohibited-path node and destination zone. Branches, merges with alternatives,
// access transitions and query endpoints remain explicit search states.
func (s *Store) buildChains() {
	s.continuation = make([]int32, len(s.edges))
	for i, in := range s.edges {
		s.continuation[i] = -1
		if s.restrictedNodes[in.to] || s.zones[in.segment] != 0 {
			continue
		}
		next := -1
		for _, id := range s.out(in.to) {
			out := s.edges[id]
			if out.segment == in.segment {
				continue
			}
			if next >= 0 {
				next = -1
				break
			}
			next = id
		}
		if next >= 0 && s.zones[s.edges[next].segment] == 0 {
			s.continuation[i] = int32(next)
		}
		if in.seconds > 0 {
			speed := in.length / in.seconds
			if speed > s.maxMetersPerSecond {
				s.maxMetersPerSecond = speed
			}
		}
	}
	// Break each functional-graph cycle at a reproducible anchor. This bounds
	// expansion even on closed one-way rings with no junctions.
	color := make([]uint8, len(s.edges))
	for start := range s.edges {
		if color[start] != 0 {
			continue
		}
		path := []int{}
		v := start
		for v >= 0 && color[v] == 0 {
			color[v] = 1
			path = append(path, v)
			v = int(s.continuation[v])
		}
		if v >= 0 && color[v] == 1 {
			anchor := v
			for w := int(s.continuation[v]); w != v; w = int(s.continuation[w]) {
				if w < anchor {
					anchor = w
				}
			}
			s.continuation[anchor] = -1
		}
		for _, id := range path {
			color[id] = 2
		}
	}
	// Include edges at restriction nodes too when determining the global speed.
	for _, e := range s.edges {
		if e.seconds > 0 && e.length/e.seconds > s.maxMetersPerSecond {
			s.maxMetersPerSecond = e.length / e.seconds
		}
	}
}
