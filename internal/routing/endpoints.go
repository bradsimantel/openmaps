package routing

import (
	"context"
	"fmt"
	"math"
	"sort"
)

// Access data is geometry and source evidence, never a text-search index. API
// code compares labels and supplies the matching source way/area identifiers.
type AccessWay struct {
	Way      int64   `json:"way"`
	Name     string  `json:"name,omitempty"`
	Driveway bool    `json:"driveway,omitempty"`
	Nodes    []int64 `json:"nodes"`
	Geometry []Point `json:"geometry"`
}
type AccessArea struct {
	Restricted bool    `json:"restricted,omitempty"` // Parking area requires rights or conditions this profile cannot establish.
	Way        int64   `json:"way"`
	Parking    bool    `json:"parking,omitempty"`
	Number     string  `json:"number,omitempty"`
	Street     string  `json:"street,omitempty"`
	Nodes      []int64 `json:"nodes"`
	Geometry   []Point `json:"geometry"`
}
type AccessEntrance struct {
	Node  int64 `json:"node"`
	Point Point `json:"point"`
}
type AccessData struct {
	Ways      []AccessWay      `json:"ways"`
	Areas     []AccessArea     `json:"areas"`
	Entrances []AccessEntrance `json:"entrances"`
}

// Endpoint carries coordinates plus evidence selected by the API from the
// loaded snapshot. Address is a mode flag, not permission to use private roads.
type Endpoint struct {
	Point      Point
	Address    bool
	StreetWays map[int64]bool
	Areas      map[int64]bool
}

func (s *Store) AccessEvidence() AccessData {
	if s == nil {
		return AccessData{}
	}
	return s.access
}

const AddressSnapLimit = 50.0
const AccessPointLimit = 100.0
const DestinationAddressLimit = 40.0

func containsPoint(p Point, ring []Point) bool {
	inside := false
	for i := 1; i < len(ring); i++ {
		a, b := ring[i-1], ring[i]
		q, _ := project(p, a, b)
		if a != b && Distance(p, q) <= .001 {
			return true
		}
		if (a[1] > p[1]) != (b[1] > p[1]) && p[0] < (b[0]-a[0])*(p[1]-a[1])/(b[1]-a[1])+a[0] {
			inside = !inside
		}
	}
	return inside
}
func (s *Store) validateAccess() error {
	if s.meta.Version < 3 && (len(s.access.Ways)+len(s.access.Areas)+len(s.access.Entrances) > 0) {
		return fmt.Errorf("access evidence requires graph version 3")
	}
	coordinates := map[int64]Point{}
	check := func(ids []int64, ps []Point) bool {
		for i, id := range ids {
			if old, ok := coordinates[id]; ok && old != ps[i] {
				return false
			}
			if old, ok := s.lookupPoint(id); ok && old != ps[i] {
				return false
			}
			coordinates[id] = ps[i]
		}
		return true
	}
	ids := map[int64]bool{}
	for _, w := range s.access.Ways {
		if w.Way <= 0 || ids[w.Way] || (len(w.Nodes) < 2 && (w.Driveway || w.Name == "" || len(w.Nodes) != 0)) || len(w.Nodes) != len(w.Geometry) {
			return fmt.Errorf("invalid access way")
		}
		ids[w.Way] = true
		if !check(w.Nodes, w.Geometry) {
			return fmt.Errorf("conflicting access coordinates")
		}
		for i, p := range w.Geometry {
			if !p.Valid() || w.Nodes[i] <= 0 {
				return fmt.Errorf("invalid access way geometry")
			}
			if q, ok := s.lookupPoint(w.Nodes[i]); ok && q != p {
				return fmt.Errorf("access/graph node mismatch")
			}
		}
	}
	ids = map[int64]bool{}
	for _, a := range s.access.Areas {
		if a.Way <= 0 || ids[a.Way] || len(a.Nodes) < 4 || len(a.Nodes) != len(a.Geometry) || a.Nodes[0] != a.Nodes[len(a.Nodes)-1] || a.Geometry[0] != a.Geometry[len(a.Geometry)-1] {
			return fmt.Errorf("invalid access area")
		}
		ids[a.Way] = true
		if !check(a.Nodes, a.Geometry) {
			return fmt.Errorf("conflicting access coordinates")
		}
		for i, p := range a.Geometry {
			if !p.Valid() || a.Nodes[i] <= 0 {
				return fmt.Errorf("invalid access area geometry")
			}
		}
	}
	ids = map[int64]bool{}
	for _, e := range s.access.Entrances {
		if e.Node <= 0 || ids[e.Node] || !e.Point.Valid() {
			return fmt.Errorf("invalid access entrance")
		}
		ids[e.Node] = true
		if p, ok := s.lookupPoint(e.Node); ok && p != e.Point {
			return fmt.Errorf("entrance/graph node mismatch")
		}
	}
	return nil
}
func (s *Store) endpointSnap(ctx context.Context, e Endpoint, role string) (Snap, error) {
	if err := ctx.Err(); err != nil {
		return Snap{}, err
	}
	if !e.Address {
		return s.snap(ctx, e.Point, role)
	}
	fail := func() (Snap, error) { return Snap{Requested: e.Point}, &Error{"endpoint_association_failed", role} }
	if s.meta.Version < 3 {
		return Snap{Requested: e.Point}, &Error{"address_routing_unavailable", role}
	}
	b := s.meta.EndpointBounds
	p := e.Point
	if !p.Valid() || p[0] < b[0] || p[0] > b[2] || p[1] < b[1] || p[1] > b[3] {
		return Snap{Requested: p}, &Error{"outside_coverage", role}
	}
	// Explicit containment plus shared source node topology takes priority over
	// fallback projections. The selected access point is fixed before Dijkstra.
	if candidates := s.associatedPoints(e); len(candidates) > 0 {
		sortSnaps(candidates)
		// Different equally near access nodes are not an evidenced preference.
		if len(candidates) > 1 && candidates[0].node != candidates[1].node && candidates[1].Distance-candidates[0].Distance <= 1 {
			return fail()
		}
		return candidates[0], nil
	}
	candidates := []Snap{}
	nearest := math.Inf(1)
	guard := math.Inf(1)
	for k, i := range s.segmentIndex.query(nearBox(p, AddressSnapLimit)) {
		v := s.segment(i)
		if k%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return Snap{}, err
			}
		}
		a, b := s.point(v.From), s.point(v.To)
		if !nearBounds(p, a, b, AddressSnapLimit) {
			continue
		}
		q, t := project(p, a, b)
		d := Distance(p, q)
		nearest = math.Min(nearest, d)
		if !v.Snap || v.Elevated || s.restrictedParkingPoint(p, q) {
			guard = math.Min(guard, d)
			continue
		}
		restricted := s.zones[i] != 0
		if restricted && (!e.StreetWays[v.Way] || d > DestinationAddressLimit) {
			guard = math.Min(guard, d)
			continue
		}
		if d > AddressSnapLimit {
			continue
		}
		c := s.makeSnap(p, i, q, t)
		c.Method = "nearest_road"
		if e.StreetWays[v.Way] {
			c.Method = "address_street"
		}
		if restricted {
			c.Method = "destination_address_street"
		}
		candidates = append(candidates, c)
	}
	for _, i := range s.guardIndex.query(nearBox(p, AddressSnapLimit)) {
		g := s.guard(i)
		if nearBounds(p, g.From, g.To, AddressSnapLimit) {
			q, _ := project(p, g.From, g.To)
			guard = math.Min(guard, Distance(p, q))
		}
	}
	if len(candidates) == 0 {
		return fail()
	}
	sortSnaps(candidates)
	if len(candidates) > SnapCandidates {
		candidates = candidates[:SnapCandidates]
	}
	best := candidates[0]
	// Never skip a closer closed road. A named destination road must also be
	// geometrically nearest; street spelling alone cannot jump another road.
	if guard+.1 < best.Distance {
		return fail()
	}
	if s.zones[best.index] != 0 && nearest+.1 < best.Distance {
		return fail()
	}
	// A source street name may beat a nearby service road only under the already
	// established same-junction safety checks, not across an unrelated road.
	for _, c := range candidates[1:] {
		if c.Distance > best.Distance+10 || c.Distance > 30 {
			break
		}
		if e.StreetWays[s.segment(c.index).Way] && s.safeStreetCandidate(best, c) {
			best = c
			break
		}
	}
	// Reject competing carriageways/components at effectively equal distance.
	for _, c := range candidates {
		if c.index == best.index || c.Distance > best.Distance+1 {
			continue
		}
		x, y := s.segment(c.index), s.segment(best.index)
		if x.Way != y.Way && !(c.node != 0 && c.node == best.node) && !s.localSharedJunction(c, best) && !s.safeStreetCandidate(c, best) {
			return fail()
		}
	}
	if s.connectorCrossesRoad(best, best) {
		return fail()
	}
	best.NearestDistance = nearest
	best.Evidence = []string{fmt.Sprintf("osm:way:%d", s.segment(best.index).Way)}
	best.Uncertainty = "Property entrance and off-road access are unverified; displacement is a straight-line gap, not a walking connection or permission to enter property."
	return best, nil
}
func nearBounds(p, a, b Point, meters float64) bool {
	return p[1] >= math.Min(a[1], b[1])-meters/110000 && p[1] <= math.Max(a[1], b[1])+meters/110000 && p[0] >= math.Min(a[0], b[0])-meters/(110000*math.Max(.01, math.Cos(p[1]*math.Pi/180))) && p[0] <= math.Max(a[0], b[0])+meters/(110000*math.Max(.01, math.Cos(p[1]*math.Pi/180)))
}
func sortSnaps(c []Snap) {
	sort.Slice(c, func(i, j int) bool {
		if math.Abs(c[i].Distance-c[j].Distance) > 1e-8 {
			return c[i].Distance < c[j].Distance
		}
		return c[i].Segment < c[j].Segment
	})
}
func (s *Store) makeSnap(p Point, i int, q Point, t float64) Snap {
	v := s.segment(i)
	c := Snap{Point: q, Requested: p, Distance: Distance(p, q), NearestDistance: Distance(p, q), Segment: v.ID, index: i, fraction: t, DestinationAccess: s.zones[i] != 0}
	if t == 0 {
		c.node = v.From
	}
	if t == 1 {
		c.node = v.To
	}
	if c.node != 0 && s.isPublic(c.node) {
		c.DestinationAccess = false
	}
	return c
}
func (s *Store) nodeSnap(p Point, n int64, public bool, streetWays map[int64]bool) (Snap, bool) {
	best := Snap{}
	found := false
	for _, i := range s.segmentIndex.query(segmentBox(s.point(n), s.point(n))) {
		v := s.segment(i)
		if !v.Snap || v.Elevated || v.From != n && v.To != n || public && (s.zones[i] != 0 || v.Service || !streetWays[v.Way]) {
			continue
		}
		t := 0.0
		if v.To == n {
			t = 1
		}
		c := s.makeSnap(p, i, s.point(n), t)
		if !found || c.Segment < best.Segment {
			best = c
			found = true
		}
	}
	return best, found
}
func (s *Store) associatedPoints(e Endpoint) []Snap {
	out := []Snap{}
	seen := map[int64]bool{}
	add := func(n int64, method string, area, way int64, public bool) {
		if seen[n] {
			return
		}
		c, ok := s.nodeSnap(e.Point, n, public, e.StreetWays)
		if !ok || c.Distance > AccessPointLimit || s.restrictedParkingPoint(e.Point, c.Point) {
			return
		}
		// Only an explicitly matching addressed street can anchor a driveway boundary.
		if public && !e.StreetWays[s.segment(c.index).Way] {
			return
		}
		c.Method = method
		c.Evidence = []string{fmt.Sprintf("osm:way:%d", area), fmt.Sprintf("osm:way:%d", way), fmt.Sprintf("osm:node:%d", n)}
		c.Uncertainty = "Source geometry associates this road point with the address site; property entrance, off-road connection and property access rights remain unverified."
		out = append(out, c)
		seen[n] = true
	}
	for _, i := range s.areaIndex.query(nearBox(e.Point, .001)) {
		a := s.access.Areas[i]
		if !e.Areas[a.Way] || !containsPoint(e.Point, a.Geometry) {
			continue
		}
		if a.Parking && !a.Restricted {
			for _, ent := range s.access.Entrances {
				shared := false
				for _, n := range a.Nodes {
					shared = shared || n == ent.Node
				}
				if shared {
					add(ent.Node, "mapped_parking_entrance", a.Way, a.Way, false)
				}
			}
		}
		for _, i := range s.accessIndex.query(geometryBox(a.Geometry).padMeters(.001)) {
			w := s.access.Ways[i]
			if !w.Driveway {
				continue
			}
			last := len(w.Nodes) - 1
			for _, end := range []int{0, last} {
				if !containsPoint(w.Geometry[end], a.Geometry) {
					continue
				}
				// Follow only source-connected driveway geometry, at most 150 m
				// and 32 settled nodes. This selects a public boundary; none of
				// these private/closed edges is added to the driving graph.
				for _, n := range s.drivewayBoundaries(w.Nodes[end]) {
					add(n, "mapped_driveway_public_junction", a.Way, w.Way, true)
				}
			}
		}
	}
	return out
}

func (s *Store) localSharedJunction(a, b Snap) bool {
	x, y := s.segment(a.index), s.segment(b.index)
	for _, n := range []int64{x.From, x.To} {
		if (n == y.From || n == y.To) && Distance(a.Point, s.point(n))+Distance(b.Point, s.point(n)) <= 20 {
			return true
		}
	}
	return false
}
func (s *Store) drivewayBoundaries(start int64) []int64 {
	adj := s.driveways
	pending := map[int64]float64{start: 0}
	seen := map[int64]bool{}
	out := []int64{}
	for len(pending) > 0 && len(seen) < 32 {
		n := int64(0)
		dist := math.Inf(1)
		for id, d := range pending {
			if d < dist || d == dist && id < n {
				n, dist = id, d
			}
		}
		delete(pending, n)
		if dist > 150 {
			break
		}
		seen[n] = true
		if s.isPublic(n) {
			out = append(out, n)
			continue
		}
		for _, l := range adj[n] {
			if seen[l.to] {
				continue
			}
			d := dist + l.length
			if old, ok := pending[l.to]; !ok || d < old {
				pending[l.to] = d
			}
		}
	}
	if len(seen) >= 32 {
		for _, distance := range pending {
			if distance <= 150 {
				return nil
			}
		}
	}
	return out
}

func (s *Store) restrictedParkingPoint(address, road Point) bool {
	for _, i := range s.areaIndex.query(nearBox(address, .001)) {
		a := s.access.Areas[i]
		if a.Parking && a.Restricted && containsPoint(address, a.Geometry) && containsPoint(road, a.Geometry) {
			return true
		}
	}
	return false
}
