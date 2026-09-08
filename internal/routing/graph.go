// Package routing owns a static driving graph, snapping and estimated-time
// routes. Coordinates are WGS84 longitude,latitude; all distances are metres.
package routing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"
	"time"
)

const Profile = "driving-time-v4"
const GraphVersion = 4

// Loaded ordinary passenger car, including mirrors; no trailer or roof load.
const CarHeight, CarWidth, CarLength = 1.9, 2.0, 5.0 // metres
const CarWeight, CarAxleLoad = 1.8, 1.1              // metric tonnes
const DestinationTolerance = 0.1                     // numerical on-road tolerance, not property proximity
const SnapCandidates = 8
const SnapLimit = 100.0

type Point [2]float64

func (p Point) Valid() bool {
	return !math.IsNaN(p[0]) && !math.IsNaN(p[1]) && !math.IsInf(p[0], 0) && !math.IsInf(p[1], 0) && p[0] >= -180 && p[0] <= 180 && p[1] >= -90 && p[1] <= 90
}
func Distance(a, b Point) float64 {
	const r = math.Pi / 180
	h := math.Pow(math.Sin((b[1]-a[1])*r/2), 2) + math.Cos(a[1]*r)*math.Cos(b[1]*r)*math.Pow(math.Sin((b[0]-a[0])*r/2), 2)
	return 6371008.8 * 2 * math.Asin(math.Min(1, math.Sqrt(h)))
}

type Node struct {
	ID      int64 `json:"id"`
	Point   Point `json:"point"`
	Version int   `json:"version"`
}

// Segment IDs derive from source way ID and original adjacent-node ordinal.
// Forward/backward follow source node order. Snap=false excludes motorway access.
type Segment struct {
	ID                  string `json:"id"`
	Way                 int64  `json:"way"`
	From                int64  `json:"from"`
	To                  int64  `json:"to"`
	Forward             bool   `json:"forward"`
	Backward            bool   `json:"backward"`
	Snap                bool   `json:"snap"`
	DestinationForward  bool   `json:"destination_forward,omitempty"`
	DestinationBackward bool   `json:"destination_backward,omitempty"`
	Service             bool   `json:"service,omitempty"`
	Elevated            bool   `json:"elevated,omitempty"`
}

// EdgeRef names a directed segment; '-' means reverse source order.
type EdgeRef struct {
	Segment string `json:"segment"`
	Reverse bool   `json:"reverse"`
}
type Ban struct {
	Relation int64     `json:"relation"`
	Path     []EdgeRef `json:"path"`
}
type Source struct {
	Kind     string          `json:"kind"`
	ID       int64           `json:"id"`
	Version  int             `json:"version"`
	Raw      json.RawMessage `json:"raw"`
	Decision string          `json:"decision"`
}
type Metadata struct {
	CostModel      string         `json:"cost_model,omitempty"`
	Version        int            `json:"version"`
	Profile        string         `json:"profile"`
	EndpointBounds [4]float64     `json:"endpoint_bounds"`
	GraphBounds    [4]float64     `json:"graph_bounds"`
	Release        string         `json:"release"`
	URL            string         `json:"url"`
	SourceSHA256   string         `json:"source_sha256"`
	Attribution    string         `json:"attribution"`
	Counts         map[string]int `json:"counts"`
}

// Guard preserves excluded motor-road geometry so snapping cannot jump over it.
type Guard struct {
	Segment string `json:"segment"`
	Way     int64  `json:"way"`
	From    Point  `json:"from"`
	To      Point  `json:"to"`
}
type Data struct {
	Costs    []WayCost  `json:"costs,omitempty"`
	Access   AccessData `json:"access,omitempty"`
	Metadata Metadata   `json:"metadata"`
	Guards   []Guard    `json:"guards,omitempty"`
	Nodes    []Node     `json:"nodes"`
	Segments []Segment  `json:"segments"`
	Bans     []Ban      `json:"bans"`
	Sources  []Source   `json:"sources"`
}
type Summary struct {
	Layout        string   `json:"layout,omitempty"`
	Preprocessing string   `json:"preprocessing,omitempty"`
	Metadata      Metadata `json:"metadata"`
	SHA256        string   `json:"sha256"`
}

func Digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type edge struct {
	from, to  int64
	segment   int
	reverse   bool
	cellEntry int32
	length    float64
	seconds   float64
}
type trieNode struct {
	next   map[int]int
	fail   int
	banned bool
}

// Store is an immutable graph with owned mapping lifetime. Do not copy a Store.
type Store struct {
	life                                             sync.RWMutex
	closed                                           bool
	mapping                                          *queryMapping
	mappingCleanup                                   runtime.Cleanup
	prepared                                         *preparedViews
	sourceLayout, sourcePreprocessing                string
	graphSHA                                         string
	preprocessingSeconds                             float64
	cellBounds                                       []cellBounds
	cellEntries                                      []cellEntry
	cellTransfers                                    []cellTransfer
	cellPaths                                        []cellPath
	junctions                                        []uint8
	components                                       []int32
	continuation                                     []int32
	maxMetersPerSecond                               float64
	segmentIndex, guardIndex, areaIndex, accessIndex spatialIndex
	driveways                                        map[int64][]drivewayLink
	access                                           AccessData
	meta                                             Metadata
	nodeIndex                                        map[int64]int32
	points                                           []Point
	offsets                                          []uint32
	adjacency                                        []int
	segments                                         []Segment
	edges                                            []edge
	directions                                       [][2]int
	trie                                             []trieNode
	guards                                           []Guard
	zones                                            []int
	restrictedNodes                                  map[int64]bool
	publicNodes                                      map[int64]bool
}

func New(d Data) (*Store, error) { return newStore(d, false) }
func newStore(d Data, owned bool) (*Store, error) {
	return newStoreContext(context.Background(), d, owned)
}
func newStoreContext(ctx context.Context, d Data, owned bool) (*Store, error) {
	done := loadPhase(ctx, "semantic validation and graph construction")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(d.Nodes) > math.MaxInt32 || len(d.Segments) > math.MaxInt32/2 || len(d.Guards) > math.MaxInt32 {
		return nil, fmt.Errorf("routing array capacity exceeded")
	}
	if d.Metadata.Version == GraphVersion {
		if !owned {
			d.Segments = append([]Segment(nil), d.Segments...)
		}
		sort.Slice(d.Segments, func(i, j int) bool { return d.Segments[i].ID < d.Segments[j].ID })
	}
	costs := make(map[int64]WayCost, len(d.Costs))
	if d.Metadata.Version == GraphVersion {
		if d.Metadata.CostModel != CostModel {
			return nil, fmt.Errorf("unsupported routing cost model %q", d.Metadata.CostModel)
		}
		for _, c := range d.Costs {
			if _, ok := costs[c.Way]; ok || c.Way <= 0 {
				return nil, fmt.Errorf("invalid or duplicate routing cost")
			}
			for _, v := range []Speed{c.Forward, c.Backward} {
				if !(v.KPH > 0 && v.KPH <= 300) || math.IsNaN(v.LimitKPH) || math.IsInf(v.LimitKPH, 0) || v.LimitKPH < 0 || v.LimitKPH > 300 || v.LimitKPH > 0 && v.KPH > v.LimitKPH || len(v.Notes) == 0 {
					return nil, fmt.Errorf("invalid routing speed for way %d", c.Way)
				}
			}
			costs[c.Way] = c
		}
	} else if d.Metadata.CostModel != "" || len(d.Costs) != 0 {
		return nil, fmt.Errorf("legacy graph contains unsupported costs")
	}
	s := &Store{meta: d.Metadata, nodeIndex: make(map[int64]int32, len(d.Nodes)), points: make([]Point, 0, len(d.Nodes)), segments: d.Segments, directions: make([][2]int, len(d.Segments)), trie: []trieNode{{next: map[int]int{}}}}
	b := d.Metadata.EndpointBounds
	if !((d.Metadata.Version == 1 && d.Metadata.Profile == "driving-distance-v1") || (d.Metadata.Version == 2 && d.Metadata.Profile == "driving-distance-v2") || (d.Metadata.Version == 3 && d.Metadata.Profile == "driving-distance-v3") || (d.Metadata.Version == GraphVersion && d.Metadata.Profile == Profile)) || !(Point{b[0], b[1]}).Valid() {
		return nil, fmt.Errorf("unsupported routing graph version/profile: %d/%q", d.Metadata.Version, d.Metadata.Profile)
	}
	if b[0] >= b[2] || b[1] >= b[3] || !(Point{b[2], b[3]}).Valid() || len(d.Nodes) == 0 || len(d.Segments) == 0 {
		return nil, fmt.Errorf("invalid routing graph")
	}
	for _, n := range d.Nodes {
		if _, ok := s.nodeIndex[n.ID]; ok || n.ID <= 0 || !n.Point.Valid() {
			return nil, fmt.Errorf("invalid routing node %d", n.ID)
		}
		s.nodeIndex[n.ID] = int32(len(s.points))
		s.points = append(s.points, n.Point)
	}
	s.offsets = make([]uint32, len(s.points)+1)
	edgeCount := 0
	for _, v := range d.Segments {
		if v.Forward {
			edgeCount++
		}
		if v.Backward {
			edgeCount++
		}
	}
	s.edges = make([]edge, 0, edgeCount)
	ids := make(map[string]int, len(d.Segments)+len(d.Guards))
	lookupEdge := func(ref EdgeRef) (int, bool) {
		i := ids[ref.Segment] - 1
		if i < 0 || i >= len(s.directions) {
			return 0, false
		}
		dir := 0
		if ref.Reverse {
			dir = 1
		}
		id := s.directions[i][dir]
		return id, id >= 0
	}
	usedCosts := map[int64]bool{}
	for i, v := range d.Segments {
		a, ok := s.lookupPoint(v.From)
		z, yes := s.lookupPoint(v.To)
		if !ok || !yes || v.From == v.To || v.Way <= 0 || v.ID == "" || ids[v.ID] != 0 || (!v.Forward && !v.Backward) {
			return nil, fmt.Errorf("invalid routing segment %s", v.ID)
		}
		ids[v.ID] = i + 1
		usedCosts[v.Way] = true
		l := Distance(a, z)
		if l <= 0 {
			return nil, fmt.Errorf("zero routing segment")
		}
		s.directions[i] = [2]int{-1, -1}
		for dir, allow := range []bool{v.Forward, v.Backward} {
			if !allow {
				continue
			}
			e := edge{from: v.From, to: v.To, segment: i, reverse: dir == 1, cellEntry: -1, length: l}
			if d.Metadata.Version == GraphVersion {
				c, ok := costs[v.Way]
				if !ok {
					return nil, fmt.Errorf("missing routing cost for way %d", v.Way)
				}
				speed := c.Forward.KPH
				if dir == 1 {
					speed = c.Backward.KPH
				}
				e.seconds = l * 3.6 / speed
				if math.IsInf(e.seconds, 0) || e.seconds <= 0 {
					return nil, fmt.Errorf("nonfinite routing duration for way %d", v.Way)
				}
			}
			if dir == 1 {
				e.from, e.to = e.to, e.from
			}
			id := len(s.edges)
			s.edges = append(s.edges, e)
			s.offsets[s.nodeIndex[e.from]+1]++
			s.directions[i][dir] = id
		}
	}
	for way := range costs {
		if !usedCosts[way] {
			return nil, fmt.Errorf("routing cost without included way %d", way)
		}
	}
	for _, ban := range d.Bans {
		if len(ban.Path) < 2 {
			return nil, fmt.Errorf("invalid routing ban")
		}
		t := 0
		last := -1
		for _, ref := range ban.Path {
			id, ok := lookupEdge(ref)
			if !ok || last >= 0 && s.edges[last].to != s.edges[id].from {
				return nil, fmt.Errorf("invalid routing ban relation %d", ban.Relation)
			}
			next, ok := s.trie[t].next[id]
			if !ok {
				next = len(s.trie)
				s.trie = append(s.trie, trieNode{next: map[int]int{}})
				s.trie[t].next[id] = next
			}
			t = next
			last = id
		}
		s.trie[t].banned = true
	}
	queue := []int{}
	for _, v := range s.trie[0].next {
		queue = append(queue, v)
	}
	for i := 0; i < len(queue); i++ {
		v := queue[i]
		for e, u := range s.trie[v].next {
			f := s.trie[v].fail
			for f != 0 {
				if _, ok := s.trie[f].next[e]; ok {
					break
				}
				f = s.trie[f].fail
			}
			if n, ok := s.trie[f].next[e]; ok {
				s.trie[u].fail = n
			}
			s.trie[u].banned = s.trie[u].banned || s.trie[s.trie[u].fail].banned
			queue = append(queue, u)
		}
	}

	s.guards = d.Guards
	for _, g := range d.Guards {
		if g.Segment == "" || g.Way <= 0 || ids[g.Segment] != 0 || !g.From.Valid() || !g.To.Valid() {
			return nil, fmt.Errorf("invalid snap guard")
		}
		ids[g.Segment] = -1
	}
	s.restrictedNodes = map[int64]bool{}
	for _, ban := range d.Bans {
		for _, ref := range ban.Path {
			id, _ := lookupEdge(ref)
			e := s.edges[id]
			s.restrictedNodes[e.from] = true
			s.restrictedNodes[e.to] = true
		}
	}
	s.zones = make([]int, len(s.segments))
	byNode := map[int64][]int{}
	s.publicNodes = make(map[int64]bool, len(d.Nodes))
	for _, v := range s.segments {
		if v.Forward && !v.DestinationForward || v.Backward && !v.DestinationBackward {
			s.publicNodes[v.From] = true
			s.publicNodes[v.To] = true
		}
	}
	for i, v := range s.segments {
		if v.DestinationForward || v.DestinationBackward {
			byNode[v.From] = append(byNode[v.From], i)
			byNode[v.To] = append(byNode[v.To], i)
		}
	}
	for i, v := range s.segments {
		if s.zones[i] != 0 || !(v.DestinationForward || v.DestinationBackward) {
			continue
		}
		queue := []int{i}
		s.zones[i] = i + 1
		for j := 0; j < len(queue); j++ {
			v := s.segment(queue[j])
			for _, n := range []int64{v.From, v.To} {
				for _, k := range byNode[n] {
					if s.zones[k] == 0 {
						s.zones[k] = i + 1
						queue = append(queue, k)
					}
				}
			}
		}
	}
	s.access = d.Access
	if err := s.validateAccess(); err != nil {
		return nil, err
	}
	s.packAdjacency()
	done()
	done = loadPhase(ctx, "spatial indexing")
	s.buildSpatial()
	done()
	done = loadPhase(ctx, "hierarchy preprocessing")
	preprocessingStarted := time.Now()
	s.buildChains()
	s.buildJunctions()
	if err := s.buildCells(ctx); err != nil {
		return nil, err
	}
	s.preprocessingSeconds = time.Since(preprocessingStarted).Seconds()
	done()
	return s, nil
}
func (s *Store) advance(t, e int) (int, bool) {
	for t != 0 {
		if _, ok := s.trie[t].next[e]; ok {
			break
		}
		t = s.trie[t].fail
	}
	t = s.trie[t].next[e]
	return t, !s.trie[t].banned
}
func (s *Store) Metadata() Metadata { return s.meta }
func (s *Store) HasDuration() bool  { return s != nil && s.meta.CostModel == CostModel }

type Error struct{ Outcome, Endpoint string }

func (e *Error) Error() string { return e.Endpoint + ": " + e.Outcome }

type Snap struct {
	Method            string   `json:"selection_method,omitempty"`
	Evidence          []string `json:"evidence,omitempty"`
	Uncertainty       string   `json:"uncertainty,omitempty"`
	Point             Point    `json:"point"`
	Distance          float64  `json:"distance_meters"`
	Segment           string   `json:"segment"`
	Requested         Point    `json:"requested"`
	SelectionReason   string   `json:"selection_reason,omitempty"`
	NearestDistance   float64  `json:"nearest_distance_meters"`
	DestinationAccess bool     `json:"destination_access,omitempty"`
	index             int
	fraction          float64
	node              int64
}

func project(p, a, b Point) (Point, float64) {
	x := math.Cos(p[1] * math.Pi / 180)
	dx, dy := (b[0]-a[0])*x, b[1]-a[1]
	t := ((p[0]-a[0])*x*dx + (p[1]-a[1])*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return Point{a[0] + t*(b[0]-a[0]), a[1] + t*(b[1]-a[1])}, t
}
func (s *Store) snap(ctx context.Context, p Point, endpoint string) (Snap, error) {
	if err := ctx.Err(); err != nil {
		return Snap{}, err
	}
	b := s.meta.EndpointBounds
	if !p.Valid() {
		return Snap{}, &Error{"invalid_coordinate", endpoint}
	}
	if p[0] < b[0] || p[0] > b[2] || p[1] < b[1] || p[1] > b[3] {
		return Snap{}, &Error{"outside_coverage", endpoint}
	}
	best := Snap{Distance: math.Inf(1)}
	candidates := []Snap{}
	restrictedDistance := math.Inf(1)
	for k, i := range s.segmentIndex.query(coordinateBox(p)) {
		v := s.segment(i)
		if k%4096 == 0 {
			if e := ctx.Err(); e != nil {
				return Snap{}, e
			}
		}
		if !v.Snap {
			continue
		}
		a, z := s.point(v.From), s.point(v.To) // Cheap bounds rejection before projection/haversine.
		if !coordinateBox(p).intersects(segmentBox(a, z)) {
			continue
		}
		q, t := project(p, a, z)
		dist := Distance(p, q)
		if s.meta.Version >= 2 && s.zones[i] != 0 && dist > DestinationTolerance {
			restrictedDistance = math.Min(restrictedDistance, dist)
			continue
		}
		if dist <= SnapLimit {
			c := Snap{Point: q, Distance: dist, Segment: v.ID, index: i, fraction: t, Requested: p, NearestDistance: dist, DestinationAccess: s.zones[i] != 0}
			if t == 0 {
				c.node = v.From
			}
			if t == 1 {
				c.node = v.To
			}
			if c.node != 0 && s.isPublic(c.node) {
				c.DestinationAccess = false
			}
			candidates = append(candidates, c)
		}
		if dist < best.Distance-1e-8 || math.Abs(dist-best.Distance) <= 1e-8 && v.ID < best.Segment {
			best = Snap{Point: q, Distance: dist, Segment: v.ID, index: i, fraction: t}
			if t == 0 {
				best.node = v.From
			}
			if t == 1 {
				best.node = v.To
			}
		}
	}
	if best.Distance > SnapLimit {
		return Snap{}, &Error{"unsnappable", endpoint}
	}

	if s.meta.Version == 1 {
		best.Requested = p
		best.NearestDistance = best.Distance
		return best, nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if math.Abs(candidates[i].Distance-candidates[j].Distance) > 1e-8 {
			return candidates[i].Distance < candidates[j].Distance
		}
		return candidates[i].Segment < candidates[j].Segment
	})
	best = candidates[0]
	for _, i := range s.guardIndex.query(coordinateBox(p)) {
		g := s.guard(i)
		if !coordinateBox(p).intersects(segmentBox(g.From, g.To)) {
			continue
		}
		q, _ := project(p, g.From, g.To)
		restrictedDistance = math.Min(restrictedDistance, Distance(p, q))
	}
	if restrictedDistance+.1 < best.Distance {
		return Snap{}, &Error{"unsnappable", endpoint}
	}
	if len(candidates) > SnapCandidates {
		candidates = candidates[:SnapCandidates]
	}
	// Prefer a nearby ordinary street to a service spur only at a shared,
	// unrestricted surface junction. Selection never consults the other endpoint.
	for _, c := range candidates[1:] {
		if c.Distance > 30 || c.Distance > best.Distance+10 {
			break
		}
		if s.safeStreetCandidate(best, c) {
			c.NearestDistance = best.Distance
			c.SelectionReason = "Nearby street selected over service road at the same unrestricted junction (within 20 m along source roads); property entrance is unverified."
			return c, nil
		}
	}
	return best, nil
}

func (s *Store) safeStreetCandidate(a, b Snap) bool {
	x, y := s.segment(a.index), s.segment(b.index)
	if !x.Service || y.Service || !x.Forward || !x.Backward || !y.Forward || !y.Backward || x.Elevated || y.Elevated || s.zones[a.index] != 0 || s.zones[b.index] != 0 {
		return false
	}
	// Source ways often have several short geometry pieces before the junction.
	// Walk only this same unrestricted service way, within 20 m and 32 nodes.
	type visit struct {
		node     int64
		distance float64
	}
	queue := []visit{{x.From, Distance(a.Point, s.point(x.From))}, {x.To, Distance(a.Point, s.point(x.To))}}
	seen := map[int64]float64{}
	for i := 0; i < len(queue) && i < 32; i++ {
		v := queue[i]
		if v.distance > 20 || s.isRestricted(v.node) {
			continue
		}
		if old, ok := seen[v.node]; ok && old <= v.distance {
			continue
		}
		seen[v.node] = v.distance
		if (v.node == y.From || v.node == y.To) && v.distance+Distance(s.point(v.node), b.Point) <= 20 {
			return !s.connectorCrossesRoad(a, b)
		}
		for _, id := range s.out(v.node) {
			e := s.edges[id]
			seg := s.segment(e.segment)
			if seg.Way == x.Way && seg.Forward && seg.Backward && !seg.Elevated && s.zones[e.segment] == 0 {
				queue = append(queue, visit{e.to, v.distance + e.length})
			}
		}
	}
	return false
}

// Reject a farther connector crossing a third mapped road (including a divided
// carriageway), excluded approach or barrier guard. Unmapped off-road obstacles
// still cannot be inferred, and the browser labels every connector unverified.
func (s *Store) connectorCrossesRoad(a, b Snap) bool {
	crosses := func(c, d Point) bool {
		p, q := a.Requested, b.Point
		cross := func(x, y, z Point) float64 { return (y[0]-x[0])*(z[1]-x[1]) - (y[1]-x[1])*(z[0]-x[0]) }
		return cross(p, q, c)*cross(p, q, d) < 0 && cross(c, d, p)*cross(c, d, q) < 0
	}
	for _, i := range s.segmentIndex.query(segmentBox(a.Requested, b.Point)) {
		v := s.segment(i)
		if v.Way == s.segment(a.index).Way || v.Way == s.segment(b.index).Way {
			continue
		}
		if crosses(s.point(v.From), s.point(v.To)) {
			return true
		}
	}
	for _, i := range s.guardIndex.query(segmentBox(a.Requested, b.Point)) {
		g := s.guard(i)
		if crosses(g.From, g.To) {
			return true
		}
	}
	return false
}

type Result struct {
	Geometry            []Point
	Distance            float64
	Duration            float64 // estimated road seconds; only available with CostModel
	Origin, Destination Snap
	Segments            []string
}
type state struct{ edge, trie, phase int }
type item struct {
	state    state
	distance float64
	priority float64
}
type queue []item

func (q queue) Len() int { return len(q) }
func (q queue) Less(i, j int) bool {
	if q[i].priority != q[j].priority {
		return q[i].priority < q[j].priority
	}
	if q[i].distance != q[j].distance {
		return q[i].distance < q[j].distance
	}
	if q[i].state.edge != q[j].state.edge {
		return q[i].state.edge < q[j].state.edge
	}
	if q[i].state.trie != q[j].state.trie {
		return q[i].state.trie < q[j].state.trie
	}
	return q[i].state.phase < q[j].state.phase
}
func (q queue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *queue) push(x item) {
	*q = append(*q, x)
	for i := len(*q) - 1; i > 0; {
		p := (i - 1) / 2
		if !q.Less(i, p) {
			break
		}
		q.Swap(i, p)
		i = p
	}
}
func (q *queue) pop() item {
	x := (*q)[0]
	last := len(*q) - 1
	(*q)[0] = (*q)[last]
	*q = (*q)[:last]
	for i := 0; ; {
		child := 2*i + 1
		if child >= last {
			break
		}
		if child+1 < last && q.Less(child+1, child) {
			child++
		}
		if !q.Less(child, i) {
			break
		}
		q.Swap(child, i)
		i = child
	}
	return x
}
func (s *Store) Route(ctx context.Context, origin, destination Point) (Result, error) {
	return s.RouteEndpoints(ctx, Endpoint{Point: origin}, Endpoint{Point: destination})
}
func (s *Store) RouteEndpoints(ctx context.Context, origin, destination Endpoint) (Result, error) {
	return s.routeEndpoints(ctx, origin, destination, false, true)
}

// RouteDistanceEndpoints is an internal benchmark comparator using the same
// endpoints, restrictions and elapsed-time model, while minimizing road distance.
func (s *Store) RouteDistanceEndpoints(ctx context.Context, origin, destination Endpoint) (Result, error) {
	return s.routeEndpoints(ctx, origin, destination, true, true)
}

// RouteReferenceEndpoints retains unaccelerated Dijkstra for offline correctness checks.
func (s *Store) RouteReferenceEndpoints(ctx context.Context, origin, destination Endpoint) (Result, error) {
	return s.routeEndpoints(ctx, origin, destination, false, false)
}
func (s *Store) routeEndpoints(ctx context.Context, origin, destination Endpoint, distanceOnly, accelerated bool) (Result, error) {
	metrics, _ := ctx.Value(searchMetricsKey{}).(*SearchMetrics)
	return s.routeMeasured(ctx, origin, destination, distanceOnly, accelerated, metrics)
}

// SearchMetrics isolates search from endpoint selection and path materialization.
// Counters are request-local; byte counts for Go maps must be measured separately.
type searchMetricsKey struct{}

// WithSearchMetrics enables request-local diagnostic counters for internal tools.
// The caller must not share metrics between concurrent requests.
func WithSearchMetrics(ctx context.Context, metrics *SearchMetrics) context.Context {
	return context.WithValue(ctx, searchMetricsKey{}, metrics)
}

type SearchMetrics struct {
	Snap, Search, Geometry                                         time.Duration
	Expanded, Pushes, ChainEdges, States, QueuePeak, QueueCapacity int
	JunctionShortcuts, CellShortcuts                               int
}

func (s *Store) routeMeasured(ctx context.Context, origin, destination Endpoint, distanceOnly, accelerated bool, metrics *SearchMetrics) (Result, error) {
	var started time.Time
	snapped := false
	if metrics != nil {
		*metrics = SearchMetrics{}
		started = time.Now()
		defer func() {
			if !snapped {
				metrics.Snap = time.Since(started)
			}
		}()
	}
	if s == nil {
		return Result{}, &Error{"unavailable", "routing"}
	}
	s.life.RLock()
	defer s.life.RUnlock()
	defer runtime.KeepAlive(s)
	if s.closed {
		return Result{}, fmt.Errorf("routing store closed")
	}
	cost := func(id int, length float64) float64 {
		if !distanceOnly && s.meta.CostModel == CostModel {
			return length * s.edges[id].seconds / s.edges[id].length
		}
		return length
	}
	a, e := s.endpointSnap(ctx, origin, "origin")
	if e != nil {
		return Result{Origin: a}, e
	}
	b, e := s.endpointSnap(ctx, destination, "destination")
	if e != nil {
		return Result{Origin: a, Destination: b}, e
	}
	if metrics != nil {
		metrics.Snap = time.Since(started)
		started = time.Now()
		snapped = true
	}
	result := Result{Origin: a, Destination: b}
	targetFrom, targetTo := s.point(s.segmentFields(b.index).From), s.point(s.segmentFields(b.index).To)
	if accelerated && s.components[s.nodeOrdinal(s.segmentFields(a.index).From)] != s.components[s.nodeOrdinal(s.segmentFields(b.index).From)] {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		if metrics != nil {
			metrics.Search = time.Since(started)
		}
		return result, &Error{"unreachable", "destination"}
	}
	aZone, bZone := 0, 0
	if a.DestinationAccess {
		aZone = s.zones[a.index]
	}
	if b.DestinationAccess {
		bZone = s.zones[b.index]
	}
	// A restricted zone may only be an origin prefix or destination suffix.
	// Once a destination zone is entered from public travel, no public exit is legal.
	transition := func(phase, id int) (int, bool) {
		edge := s.edges[id]
		v := s.segmentFields(edge.segment)
		restricted := v.DestinationForward
		if edge.reverse {
			restricted = v.DestinationBackward
		}
		if !restricted {
			return 1, phase != 2
		}
		zone := s.zones[edge.segment]
		if phase == 0 && aZone == zone {
			return 0, true
		}
		if bZone == zone {
			return 2, true
		}
		return phase, false
	}
	best := math.Inf(1)
	direct := false
	if a.index == b.index {
		v := s.segmentFields(a.index)
		forward := v.Forward && (!v.DestinationForward || aZone != 0 || bZone != 0)
		backward := v.Backward && (!v.DestinationBackward || aZone != 0 || bZone != 0)
		if a.fraction <= b.fraction && forward || a.fraction >= b.fraction && backward {
			dir := 0
			if a.fraction > b.fraction || !forward {
				dir = 1
			}
			best = cost(s.directions[a.index][dir], Distance(a.Point, b.Point))
			direct = true
		}
	}
	if a.node != 0 && a.node == b.node {
		best = 0
		direct = true
	}
	type trace struct {
		parent state
		first  int // initial source edge, or -1 for a transfer directly from parent
		via    int // -1: chain; >=0: junction departure; <=-2: recursive path (-2-via)
	}
	type label struct {
		distance float64
		trace    trace
	}
	labels := map[state]label{}
	q := &queue{}
	if metrics != nil {
		defer func() {
			metrics.States = len(labels)
			metrics.QueueCapacity = cap(*q)
			if metrics.Search == 0 {
				metrics.Search = time.Since(started)
			}
		}()
	}
	root := state{-1, 0, 0}
	if aZone == 0 {
		root.phase = 1
	}
	walk := func(st state, d float64) (state, float64) {
		if accelerated {
			for steps := 0; s.continuation[st.edge] >= 0; steps++ {
				in := s.edges[st.edge]
				if in.to == s.segmentFields(b.index).From || in.to == s.segmentFields(b.index).To {
					break
				}
				if steps%1024 == 0 && ctx.Err() != nil {
					return st, d
				}
				id := int(s.continuation[st.edge])
				tr, ok := s.advance(st.trie, id)
				phase, allowed := transition(st.phase, id)
				if !ok || !allowed {
					break
				}
				if metrics != nil {
					metrics.ChainEdges++
				}
				d += cost(id, s.edges[id].length)
				st = state{id, tr, phase}
			}
		}

		return st, d
	}
	enqueue := func(st state, d float64, prev state, first, via int) {
		if accelerated && s.edges[st.edge].cellEntry == terminalCellEntry && s.edges[st.edge].to != s.segmentFields(b.index).From && s.edges[st.edge].to != s.segmentFields(b.index).To {
			return
		}
		if old, ok := labels[st]; !ok || d < old.distance {
			labels[st] = label{d, trace{prev, first, via}}
			priority := d
			if accelerated {
				lower := Distance(s.point(s.edges[st.edge].to), b.Point)
				if !distanceOnly && s.HasDuration() {
					lower /= s.maxMetersPerSecond
				}
				// Downward margin protects pruning against floating-point roundoff.
				priority += math.Max(0, lower*(1-1e-12)-1e-8)
			}
			q.push(item{state: st, distance: d, priority: priority})
			if metrics != nil {
				metrics.Pushes++
				metrics.QueuePeak = max(metrics.QueuePeak, q.Len())
			}
		}
	}
	add := func(st state, d float64, prev state) {
		first := st.edge
		st, d = walk(st, d)
		in := s.edges[st.edge]
		if accelerated && (!distanceOnly || !s.HasDuration()) && st.phase != 2 {
			if transfers, ok := s.cellEscapes(st.edge, targetFrom, targetTo); ok {
				for _, transfer := range transfers {
					if metrics != nil {
						metrics.JunctionShortcuts++
						metrics.CellShortcuts++
					}
					enqueue(state{int(transfer.last), 0, 1}, d+transfer.cost, prev, first, -2-int(transfer.path))
				}
				return
			}
		}
		if accelerated && s.junctions[s.nodeOrdinal(in.to)] == 2 && in.to != s.segmentFields(b.index).From && in.to != s.segmentFields(b.index).To {
			for _, id := range s.out(in.to) {
				if s.edges[id].segment == in.segment {
					continue
				}
				tr, ok := s.advance(st.trie, id)
				phase, allowed := transition(st.phase, id)
				if !ok || !allowed {
					continue
				}
				next, length := walk(state{id, tr, phase}, d+cost(id, s.edges[id].length))
				if metrics != nil {
					metrics.JunctionShortcuts++
				}
				enqueue(next, length, prev, first, id)
			}
		} else {
			enqueue(st, d, prev, first, -1)
		}
	}

	if a.node != 0 {
		for _, id := range s.out(a.node) {
			tr, ok := s.advance(0, id)
			if phase, allowed := transition(root.phase, id); ok && allowed {
				add(state{id, tr, phase}, cost(id, s.edges[id].length), root)
			}
		}
	} else {
		for _, id := range s.directions[a.index] {
			if id < 0 {
				continue
			}
			length := Distance(a.Point, s.point(s.edges[id].to))
			tr, ok := s.advance(0, id)
			if phase, allowed := transition(root.phase, id); ok && allowed {
				add(state{id, tr, phase}, cost(id, length), root)
			}
		}
	}
	// A destination inside an outgoing segment may be reached before its far node.
	endState := root
	endEdge := -1
	iterations := 0
	checkEnd := func(node int64, st state, d float64) {
		if b.node != 0 {
			if node == b.node && d < best {
				best = d
				direct = false
				endState = st
				endEdge = -1
			}
			return
		}
		for _, id := range s.directions[b.index] {
			if id < 0 || s.edges[id].from != node {
				continue
			}
			if st.edge >= 0 && s.edges[st.edge].segment == s.edges[id].segment {
				continue
			}
			_, ok := s.advance(st.trie, id)
			_, allowed := transition(st.phase, id)
			if !ok || !allowed {
				continue
			}
			v := d + cost(id, Distance(s.point(node), b.Point))
			if v < best {
				best = v
				direct = false
				endState = st
				endEdge = id
			}
		}
	}
	if a.node != 0 {
		checkEnd(a.node, root, 0)
	}
	for q.Len() > 0 {
		cur := q.pop()
		if cur.distance != labels[cur.state].distance {
			continue
		}
		if cur.priority >= best {
			break
		}
		iterations++
		if metrics != nil {
			metrics.Expanded++
		}
		if iterations%1024 == 0 {
			if e := ctx.Err(); e != nil {
				return Result{}, e
			}
		}
		in := s.edges[cur.state.edge]
		checkEnd(in.to, cur.state, cur.distance)
		if accelerated && (!distanceOnly || !s.HasDuration()) && cur.state.phase != 2 {
			if transfers, ok := s.cellEscapes(cur.state.edge, targetFrom, targetTo); ok {
				for _, transfer := range transfers {
					if metrics != nil {
						metrics.JunctionShortcuts++
						metrics.CellShortcuts++
					}
					enqueue(state{int(transfer.last), 0, 1}, cur.distance+transfer.cost, cur.state, -1, -2-int(transfer.path))
				}
				continue
			}
		}
		for _, id := range s.out(in.to) {
			out := s.edges[id]
			if out.segment == in.segment {
				continue
			}
			tr, ok := s.advance(cur.state.trie, id)
			if phase, allowed := transition(cur.state.phase, id); ok && allowed {
				add(state{id, tr, phase}, cur.distance+cost(id, out.length), cur.state)
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if math.IsInf(best, 1) {
		return result, &Error{"unreachable", "destination"}
	}
	if metrics != nil {
		metrics.Search = time.Since(started)
		started = time.Now()
		defer func() { metrics.Geometry = time.Since(started) }()
	}
	result.Geometry = []Point{a.Point}
	travel := []int{}
	if !direct {
		path := []int{}
		for st := endState; st.edge >= 0; st = labels[st].trace.parent {
			block := []int{}
			if first := labels[st].trace.first; first >= 0 {
				block = append(block, first)
			}
			if via := labels[st].trace.via; via < -1 {
				for len(block) > 0 && s.continuation[block[len(block)-1]] >= 0 {
					block = append(block, int(s.continuation[block[len(block)-1]]))
				}
				parts := []int32{}
				for p := int32(-2 - via); p >= 0; p = s.cellPaths[p].parent {
					parts = append(parts, p)
				}
				for i := len(parts) - 1; i >= 0; i-- {
					p := s.cellPaths[parts[i]]
					for id := p.first; ; id = s.continuation[id] {
						block = append(block, int(id))
						if id == p.last {
							break
						}
					}
				}
			}
			if via := labels[st].trace.via; via >= 0 {
				for s.continuation[block[len(block)-1]] >= 0 {
					block = append(block, int(s.continuation[block[len(block)-1]]))
				}
				block = append(block, via)
			}
			for block[len(block)-1] != st.edge {
				block = append(block, int(s.continuation[block[len(block)-1]]))
			}
			for i := len(block) - 1; i >= 0; i-- {
				path = append(path, block[i])
			}
		}
		for i := len(path) - 1; i >= 0; i-- {
			travel = append(travel, path[i])
			v := s.edges[path[i]]
			result.Geometry = append(result.Geometry, s.point(v.to))
			result.Segments = append(result.Segments, s.segment(v.segment).ID)
		}
		if endEdge >= 0 {
			travel = append(travel, endEdge)
			result.Segments = append(result.Segments, s.segment(s.edges[endEdge].segment).ID)
		}
	} else {
		result.Segments = []string{a.Segment}
		dir := 0
		if a.fraction > b.fraction || !s.segmentFields(a.index).Forward {
			dir = 1
		}
		travel = append(travel, s.directions[a.index][dir])
	}
	if result.Geometry[len(result.Geometry)-1] != b.Point || len(result.Geometry) == 1 {
		result.Geometry = append(result.Geometry, b.Point)
	}
	for i := 1; i < len(result.Geometry); i++ {
		length := Distance(result.Geometry[i-1], result.Geometry[i])
		result.Distance += length
		if length > 0 && s.meta.CostModel == CostModel {
			e := s.edges[travel[i-1]]
			result.Duration += length * e.seconds / e.length
		}
	}
	return result, ctx.Err()
}
