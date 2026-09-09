package valhallatiles

import "fmt"

// incoming enumerates actual ordinary source records. Provider opposing indexes
// can be many-to-one for duplicate edges, so reversing a single opposing record
// loses legal incoming identities. Every source edge still has an opposing edge
// with the correct endpoints (checked by the full graph audit); those outgoing
// endpoints therefore enumerate all neighboring source nodes, including when an
// opposing record is a shortcut. No shortcut is itself traversed.
func (s *Router) incoming(n Node, allowMissing bool, visit func(Edge, ID) error) (uint64, error) {
	if s.reverseSupport != nil {
		exception, err := s.reverseSupport.exception(n.ID)
		if err != nil {
			return 0, err
		}
		if !exception {
			var missing uint64
			for i := 0; i < n.EdgeCount; i++ {
				e, err := s.Reader.Edge(n.ID.WithIndex(n.EdgeIndex + i))
				if err != nil {
					return missing, err
				}
				if e.Shortcut {
					continue
				}
				if _, ok := s.Reader.index[e.End.Base()]; !ok && allowMissing {
					missing++
					continue
				}
				opp, err := s.opposite(e)
				if err != nil {
					return missing, err
				}
				if opp.Shortcut {
					continue
				}
				if err := visit(opp, e.End); err != nil {
					return missing, err
				}
			}
			return missing, nil
		}
	}
	var neighbors [128]ID
	count := 0
	var missing uint64
	for i := 0; i < n.EdgeCount; i++ {
		e, err := s.Reader.Edge(n.ID.WithIndex(n.EdgeIndex + i))
		if err != nil {
			return missing, err
		}
		seen := false
		for j := 0; j < count; j++ {
			if neighbors[j] == e.End {
				seen = true
				break
			}
		}
		if seen {
			continue
		}
		if count == len(neighbors) {
			return missing, fmt.Errorf("incoming neighbor budget exceeded")
		}
		neighbors[count] = e.End
		count++
		if _, ok := s.Reader.index[e.End.Base()]; !ok && allowMissing {
			missing++
			continue
		}
		source, err := s.Reader.Node(e.End)
		if err != nil {
			return missing, err
		}
		for j := 0; j < source.EdgeCount; j++ {
			incoming, err := s.Reader.Edge(source.ID.WithIndex(source.EdgeIndex + j))
			if err != nil {
				return missing, err
			}
			if incoming.End != n.ID || incoming.Shortcut {
				continue
			}
			if err := visit(incoming, source.ID); err != nil {
				return missing, err
			}
		}
	}
	return missing, nil
}
