package routing

// Source node identities are kept at the boundary. Coordinates and adjacency
// occupy contiguous arrays; a single source-ID to dense-index map replaces
// separate coordinate and per-node adjacency maps and tiny slice allocations.
func (s *Store) point(id int64) Point { return s.points[s.nodeOrdinal(id)] }
func (s *Store) lookupPoint(id int64) (Point, bool) {
	i, ok := s.findNode(id)
	if !ok {
		return Point{}, false
	}
	return s.points[i], true
}
func (s *Store) out(id int64) []int {
	i, ok := s.findNode(id)
	if !ok {
		return nil
	}
	return s.adjacency[s.offsets[i]:s.offsets[i+1]]
}
func (s *Store) packAdjacency() {
	for i := 1; i < len(s.offsets); i++ {
		s.offsets[i] += s.offsets[i-1]
	}
	s.adjacency = make([]int, len(s.edges))
	cursor := append([]uint32(nil), s.offsets...)
	for id, e := range s.edges {
		i := s.nodeIndex[e.from]
		s.adjacency[cursor[i]] = id
		cursor[i]++
	}
}
