package valhallatiles

import "fmt"

type TileAudit struct {
	ID                                            ID
	Bytes, Nodes, Edges, Transitions, AccessRules int
	DatasetID, SourceChecksum                     uint64
	CreatedDaysSince2014                          uint32
}
type Audit struct {
	Tiles                                                                                                                                         []TileAudit
	Nodes, Edges, Shortcuts, Transitions, CrossTileEdges, SimpleMasks, ComplexRestrictions, TimedTurns, AllowedEdges, AutoEdges, DestinationEdges int
	AccessTypes                                                                                                                                   map[uint8]int
	UniqueEdgeInfoTagTypes                                                                                                                        map[uint8]int
	GeometryMaxEndpointGapMeters                                                                                                                  float64
}

// Audit scans bounded tiles and resolves every node, edge, opposing edge and
// transition reference. It does not establish OSM source fidelity or legality.
func (s *Router) Audit() (Audit, error) {
	r := s.Reader
	out := Audit{AccessTypes: map[uint8]int{}, UniqueEdgeInfoTagTypes: map[uint8]int{}, ComplexRestrictions: s.RestrictionCount, TimedTurns: s.TimedRestrictionCount}
	for _, id := range r.TileIDs() {
		t, err := r.get(id)
		if err != nil {
			return out, err
		}
		summary := TileAudit{id, len(t.b), t.nodes, t.edges, t.transitions, t.access, u64(t.b, 32), u64(t.b, 88), u32(t.b, 112)}
		seenInfo := map[int]bool{}
		out.Tiles = append(out.Tiles, summary)
		out.Nodes += summary.Nodes
		out.Edges += summary.Edges
		out.Transitions += summary.Transitions
		for i := 0; i < summary.Nodes; i++ {
			n, err := r.Node(id.WithIndex(i))
			if err != nil {
				return out, err
			}
			trans, err := r.Transitions(n)
			if err != nil {
				return out, err
			}
			for _, target := range trans {
				other, err := r.Node(target)
				if err != nil {
					return out, err
				}
				if Distance(n.Point, other.Point) > .1 {
					return out, fmt.Errorf("transition coordinates disagree: %s", n.ID)
				}
				back, err := r.Transitions(other)
				if err != nil {
					return out, err
				}
				found := false
				for _, v := range back {
					found = found || v == n.ID
				}
				if !found {
					return out, fmt.Errorf("nonreciprocal transition: %s", n.ID)
				}
			}
			for j := 0; j < n.EdgeCount; j++ {
				e, err := r.Edge(id.WithIndex(n.EdgeIndex + j))
				if err != nil {
					return out, err
				}
				end, err := r.Node(e.End)
				if err != nil {
					return out, err
				}
				opp, err := s.opposite(e)
				if err != nil {
					return out, err
				}
				if opp.End != n.ID {
					return out, fmt.Errorf("opposing endpoint mismatch: %s", e.ID)
				}
				if e.End.Base() != id {
					out.CrossTileEdges++
				}
				if e.Shortcut {
					out.Shortcuts++
					continue
				}
				if e.Restrictions != 0 {
					out.SimpleMasks++
				}
				if e.Access&1 != 0 {
					out.AutoEdges++
				}
				if e.Destination {
					out.DestinationEdges++
				}
				rules, err := r.AccessRules(e)
				if err != nil {
					return out, err
				}
				for _, rule := range rules {
					out.AccessTypes[rule.Type]++
				}
				allowed, err := s.Allowed(e)
				if err != nil {
					return out, err
				}
				if allowed {
					out.AllowedEdges++
				}
				shape, err := r.Shape(e)
				if err != nil {
					return out, err
				}
				if !seenInfo[e.Info] {
					seenInfo[e.Info] = true
					for _, tag := range shape.TagTypes {
						out.UniqueEdgeInfoTagTypes[tag]++
					}
				}
				gap := max(Distance(n.Point, shape.Points[0]), Distance(end.Point, shape.Points[len(shape.Points)-1]))
				out.GeometryMaxEndpointGapMeters = max(out.GeometryMaxEndpointGapMeters, gap)
				if gap > 1 {
					return out, fmt.Errorf("geometry endpoint mismatch %s: %f m", e.ID, gap)
				}
			}
		}
	}
	return out, nil
}
