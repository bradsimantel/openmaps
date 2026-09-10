package routing

import (
	"context"
	"errors"
	"fmt"
	"math"
)

type TileAudit struct {
	Package, Version                              string
	ID                                            ID
	Bytes, Nodes, Edges, Transitions, AccessRules int
	DatasetID, SourceChecksum                     uint64
	CreatedDaysSince2014                          uint32
}
type Audit struct {
	Snaps                                                                                                                                         []SnapAudit
	GeometryMismatches, AllowedGeometryMismatches                                                                                                 int
	GeometryExamples                                                                                                                              []GeometryMismatch
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

type SnapAudit struct {
	lonDegrees        float64
	Requested         Point
	Indexed, FullScan *Snap
	IndexedError      string `json:",omitempty"`
	NearestMatches    bool
}

type GeometryMismatch struct {
	Edge                             ID
	Way                              uint64
	Allowed                          bool
	GapMeters                        float64
	Start, End, ShapeStart, ShapeEnd Point
}

// Audit scans bounded tiles and resolves every node, edge, opposing edge and
// transition reference. It does not establish OSM source fidelity or legality.
func (s *Router) Audit() (Audit, error) { return s.AuditSample(false) }

// AuditSample permits external dependencies only when explicitly requested.
// Present records still undergo validation. MissingReferences is never a pass
// for dataset completeness.
func (s *Router) AuditSample(allowMissing bool) (Audit, error) {
	return s.AuditContext(context.Background(), allowMissing)
}
func (s *Router) AuditContext(ctx context.Context, allowMissing bool) (Audit, error) {
	return s.AuditWithSnaps(ctx, allowMissing, nil)
}

// AuditWithSnaps independently scans every retained shape for up to 16 probe
// points, comparing the nearest eligible result with the provider-bin lookup.
// An absent indexed tile remains incomplete even when the retained scan is empty.
func (s *Router) AuditWithSnaps(ctx context.Context, allowMissing bool, probes []Point) (Audit, error) {
	r := s.Reader
	out := Audit{NodeTypes: map[uint8]int{}, RoadClasses: map[uint8]int{}, RoadUses: map[uint8]int{}, Surfaces: map[uint8]int{}, Speeds: map[uint8]int{}, MissingReferences: map[ID]int{}, AccessTypes: map[uint8]int{}, UniqueEdgeInfoTagTypes: map[uint8]int{}, ComplexRestrictions: s.RestrictionCount, TimedTurns: s.TimedRestrictionCount}
	if len(probes) > 16 {
		return out, errors.New("audit snap probe budget exceeded")
	}
	for _, p := range probes {
		if !p.valid() {
			return out, errors.New("invalid audit probe")
		}
		row := SnapAudit{Requested: p, lonDegrees: math.Min(180, 100/110000.0/math.Cos(p[1]*math.Pi/180))}
		indexed, err := s.SnapContext(ctx, p)
		if err != nil {
			row.IndexedError = err.Error()
		} else {
			row.Indexed = &indexed
		}
		out.Snaps = append(out.Snaps, row)
	}
	for _, id := range r.TileIDs() {
		if err := ctx.Err(); err != nil {
			return out, err
		}
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
			if err := ctx.Err(); err != nil {
				return out, err
			}
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
				if len(out.Snaps) > 0 && e.Class >= 2 && !e.Bridge && !e.Tunnel {
					minY, maxY := shape.Points[0][1], shape.Points[0][1]
					anchor := shape.Points[0][0]
					minX, maxX := 0.0, 0.0
					for _, p := range shape.Points {
						minY = min(minY, p[1])
						maxY = max(maxY, p[1])
						x := wrapLongitude(p[0] - anchor)
						minX = min(minX, x)
						maxX = max(maxX, x)
					}
					for i := range out.Snaps {
						probe := &out.Snaps[i]
						p := probe.Requested
						if p[1] < minY-.001 || p[1] > maxY+.001 {
							continue
						}
						x := wrapLongitude(p[0] - anchor)
						if x < minX-probe.lonDegrees || x > maxX+probe.lonDegrees {
							continue
						}
						q, gap, f := project(p, shape.Points)
						if gap > 100 || (probe.FullScan != nil && (gap > probe.FullScan.GapMeters || gap == probe.FullScan.GapMeters && e.ID >= probe.FullScan.Edge)) {
							continue
						}
						ok := allowed
						if !ok {
							opposite, err := s.opposite(e)
							if err != nil {
								return out, err
							}
							ok, err = s.Allowed(opposite)
							if err != nil {
								return out, err
							}
						}
						if ok {
							probe.FullScan = &Snap{p, q, gap, e.ID, f}
						}
					}
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
					out.GeometryMismatches++
					if allowed {
						out.AllowedGeometryMismatches++
					}
					if len(out.GeometryExamples) < 128 {
						out.GeometryExamples = append(out.GeometryExamples, GeometryMismatch{e.ID, shape.Way, allowed, gap, n.Point, end.Point, shape.Points[0], shape.Points[len(shape.Points)-1]})
					}
				}
			}
		}
	}
	for i := range out.Snaps {
		probe := &out.Snaps[i]
		if probe.Indexed != nil && probe.FullScan != nil {
			probe.NearestMatches = math.Abs(probe.Indexed.GapMeters-probe.FullScan.GapMeters) < .00001 && Distance(probe.Indexed.Point, probe.FullScan.Point) < .01
		}
		if probe.Indexed == nil && probe.FullScan == nil && probe.IndexedError == ErrUnsnappable.Error() {
			probe.NearestMatches = true
		}
	}
	if out.GeometryMismatches != 0 {
		return out, fmt.Errorf("%d geometry endpoint mismatches (%d on permitted edges); bounded examples retained", out.GeometryMismatches, out.AllowedGeometryMismatches)
	}
	return out, nil
}
