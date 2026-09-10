package routing

import "context"

// forcedContinuation bypasses up to 64 uniquely legal departures. It preserves
// every underlying edge and turn state, stops at endpoint nodes and hierarchy
// transitions, and never removes a choice. The finite bound also breaks cycles
// without a per-walk visited map. Ordinary Dijkstra never uses this optimization.
func (s *Router) forcedContinuation(ctx context.Context, st state, protected map[ID]bool, budget float64) (state, float64, []ID, error) {
	var edges []ID
	cost := 0.0
	for len(edges) < 64 {
		if err := ctx.Err(); err != nil {
			return st, cost, nil, err
		}
		if protected[st.node] || cost >= budget {
			break
		}
		node, err := s.Reader.Node(st.node)
		if err != nil {
			return st, cost, nil, err
		}
		if node.Access&1 == 0 || node.TransitionCount != 0 {
			break
		}
		previous, err := s.Reader.Edge(st.last)
		if err != nil {
			return st, cost, nil, err
		}
		var selected Edge
		selectedHistory, count := 0, 0
		for i := 0; i < node.EdgeCount; i++ {
			e, err := s.Reader.Edge(node.ID.WithIndex(node.EdgeIndex + i))
			if err != nil {
				return st, cost, nil, err
			}
			if previous.OppLocalIndex == e.LocalIndex || e.LocalIndex < 8 && previous.Restrictions&(1<<e.LocalIndex) != 0 {
				continue
			}
			allowed, err := s.Allowed(e)
			if err != nil {
				return st, cost, nil, err
			}
			if !allowed {
				continue
			}
			history, banned, err := s.advanceChecked(st.restriction, e.ID)
			if err != nil {
				return st, cost, nil, err
			}
			if banned {
				continue
			}
			count++
			if count > 1 {
				break
			}
			selected, selectedHistory = e, history
		}
		if count != 1 {
			break
		}
		cost += selected.Length * 3.6 / float64(selected.Speed)
		edges = append(edges, selected.ID)
		st = state{selected.End, selected.ID, selectedHistory}
	}
	return st, cost, edges, nil
}
