package routing

// denseEdge is the prepared v2 32-byte encoding: zero-based node ordinals,
// a 31-bit segment ordinal plus reverse bit, signed cell entry, and unchanged
// float64 distance/seconds. It never contains or supplies a public identity.
// Legacy/source construction and prepared v1 keep the original edge layout.
type denseEdge struct {
	from, to, segmentReverse uint32
	cellEntry                int32
	length, seconds          float64
}

func (s *Store) dense() bool { return s.prepared != nil && s.prepared.denseEdges != nil }
func (s *Store) edgeCount() int {
	if s.dense() {
		return len(s.prepared.denseEdges)
	}
	return len(s.edges)
}

// queryEdge endpoints are keys in this Store's search representation. Consumers
// use searchPoint/searchOut/searchOrdinal, never source-ID lookup on these keys.
func (s *Store) queryEdge(i int) edge {
	if !s.dense() {
		return s.edges[i]
	}
	e := s.prepared.denseEdges[i]
	return edge{from: int64(e.from), to: int64(e.to), segment: int(e.segmentReverse & 0x7fffffff), reverse: e.segmentReverse>>31 != 0, cellEntry: e.cellEntry, length: e.length, seconds: e.seconds}
}

// sourceEdge is only for the bounded source-connected endpoint walk. Source
// identities come from the authoritative segment, with direction preserved.
func (s *Store) sourceEdge(i int) edge {
	e := s.queryEdge(i)
	if s.dense() {
		v := s.segmentFields(e.segment)
		e.from, e.to = v.From, v.To
		if e.reverse {
			e.from, e.to = e.to, e.from
		}
	}
	return e
}
func (s *Store) searchNode(source int64) int64 {
	if s.dense() {
		return int64(s.nodeOrdinal(source))
	}
	return source
}
func (s *Store) searchOrdinal(node int64) int32 {
	if s.dense() {
		return int32(node)
	}
	return s.nodeOrdinal(node)
}
func (s *Store) searchPoint(node int64) Point { return s.points[s.searchOrdinal(node)] }
func (s *Store) searchOut(node int64) []int {
	i := s.searchOrdinal(node)
	return s.adjacency[s.offsets[i]:s.offsets[i+1]]
}
