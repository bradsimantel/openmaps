package valhallatiles

import "fmt"

type TileAudit struct {
	Package, Version                              string
	ID                                            ID
	Bytes, Nodes, Edges, Transitions, AccessRules int
	DatasetID, SourceChecksum                     uint64
	CreatedDaysSince2014                          uint32
}
type Audit struct {
	GeometryMaxEndpointGapEdge                                                                                                                    ID
	NodeTypes, RoadClasses, RoadUses, Surfaces, Speeds                                                                                            map[uint8]int
	PrivateNodes, TaggedAccessNodes, AutoBlockedNodes                                                                                             int
	MissingReferences                                                                                                                             map[ID]int
	CrossPackageEdges, CrossPackageTransitions                                                                                                    int
	Tiles                                                                                                                                         []TileAudit
	Nodes, Edges, Shortcuts, Transitions, CrossTileEdges, SimpleMasks, ComplexRestrictions, TimedTurns, AllowedEdges, AutoEdges, DestinationEdges int
	AccessTypes                                                                                                                                   map[uint8]int
	UniqueEdgeInfoTagTypes                                                                                                                        map[uint8]int
	GeometryMaxEndpointGapMeters                                                                                                                  float64
}

// Audit scans bounded tiles and resolves every node, edge, opposing edge and
// transition reference. It does not establish OSM source fidelity or legality.
func (s *Router) Audit() (Audit, error) { return s.AuditSample(false) }

// AuditSample permits external dependencies only when explicitly requested.
// Present records still undergo validation. MissingReferences is never a pass
// for dataset completeness.
func (s *Router) AuditSample(allowMissing bool) (Audit, error) {
	r := s.Reader
	out := Audit{NodeTypes: map[uint8]int{}, RoadClasses: map[uint8]int{}, RoadUses: map[uint8]int{}, Surfaces: map[uint8]int{}, Speeds: map[uint8]int{}, MissingReferences: map[ID]int{}, AccessTypes: map[uint8]int{}, UniqueEdgeInfoTagTypes: map[uint8]int{}, ComplexRestrictions: s.RestrictionCount, TimedTurns: s.TimedRestrictionCount}
	for _, id := range r.TileIDs() {
		t, err := r.get(id)
		if err != nil {
			return out, err
		}
		summary := TileAudit{r.Package(id), r.version, id, t.size, t.nodes, t.edges, t.transitions, t.access, u64(t.b, 32), u64(t.b, 88), u32(t.b, 112)}
		// Check bin edge references and restriction edge references separately
		// from node endpoints; IDs share a namespace but record kinds differ.
		refs := []ID{}
		for i := 0; i < int(u32(t.b, 212)); i++ {
			b, err := t.span(t.binStart+i*8, 8)
			if err != nil {
				return out, err
			}
			refs = append(refs, ID(u64(b, 0)))
		}
		rs, err := r.Restrictions(id)
		if err != nil {
			return out, err
		}
		for _, rule := range rs {
			refs = append(refs, rule.Path...)
		}
		for _, ref := range refs {
			if uint64(ref) > idMask || ref.Level() > 2 {
				return out, fmt.Errorf("invalid reference %s", ref)
			}
			if _, ok := r.index[ref.Base()]; !ok && allowMissing {
				out.MissingReferences[ref.Base()]++
				continue
			}
			if _, err := r.Edge(ref); err != nil {
				return out, err
			}
		}
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
			out.NodeTypes[n.Type]++
			if n.Private {
				out.PrivateNodes++
			}
			if n.TaggedAccess {
				out.TaggedAccessNodes++
			}
			if n.Access&1 == 0 {
				out.AutoBlockedNodes++
			}
			trans, err := r.Transitions(n)
			if err != nil {
				return out, err
			}
			for _, target := range trans {
				if _, ok := r.index[target.Base()]; !ok && allowMissing {
					out.MissingReferences[target.Base()]++
					continue
				}
				if r.Package(target) != r.Package(id) {
					out.CrossPackageTransitions++
				}
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
				_, endPresent := r.index[e.End.Base()]
				var end Node
				if !endPresent && allowMissing {
					out.MissingReferences[e.End.Base()]++
				} else {
					end, err = r.Node(e.End)
					if err != nil {
						return out, err
					}
				}
				if endPresent {
					opp, err := s.opposite(e)
					if err != nil {
						return out, err
					}
					if opp.End != n.ID {
						return out, fmt.Errorf("opposing endpoint mismatch: %s", e.ID)
					}
				}
				if e.End.Base() != id {
					out.CrossTileEdges++
					if endPresent && r.Package(e.End) != r.Package(id) {
						out.CrossPackageEdges++
					}
				}
				if e.Shortcut {
					out.Shortcuts++
					continue
				}
				out.RoadClasses[e.Class]++
				out.RoadUses[e.Use]++
				out.Surfaces[e.Surface]++
				out.Speeds[e.Speed]++
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
				gap := Distance(n.Point, shape.Points[0])
				if endPresent {
					gap = max(gap, Distance(end.Point, shape.Points[len(shape.Points)-1]))
				}
				if gap > out.GeometryMaxEndpointGapMeters {
					out.GeometryMaxEndpointGapMeters = gap
					out.GeometryMaxEndpointGapEdge = e.ID
				}
				if gap > 1 {
					return out, fmt.Errorf("geometry endpoint mismatch %s: %f m", e.ID, gap)
				}
			}
		}
	}
	return out, nil
}
