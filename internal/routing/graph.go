// Package routing owns a static driving graph, snapping and shortest-distance
// routes. Coordinates are WGS84 longitude,latitude; all distances are metres.
package routing

import (
	"container/heap"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

const Profile = "driving-distance-v2"
const GraphVersion = 2

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
	Metadata Metadata  `json:"metadata"`
	Guards   []Guard   `json:"guards,omitempty"`
	Nodes    []Node    `json:"nodes"`
	Segments []Segment `json:"segments"`
	Bans     []Ban     `json:"bans"`
	Sources  []Source  `json:"sources"`
}
type Summary struct {
	Metadata Metadata `json:"metadata"`
	SHA256   string   `json:"sha256"`
}

func Digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

type edge struct {
	from, to int64
	segment  int
	reverse  bool
	length   float64
}
type trieNode struct {
	next   map[int]int
	fail   int
	banned bool
}
type Store struct {
	meta            Metadata
	nodes           map[int64]Point
	segments        []Segment
	edges           []edge
	outgoing        map[int64][]int
	directions      [][2]int
	trie            []trieNode
	guards          []Guard
	zones           []int
	restrictedNodes map[int64]bool
	publicNodes     map[int64]bool
}

func New(d Data) (*Store, error) {
	s := &Store{meta: d.Metadata, nodes: map[int64]Point{}, segments: d.Segments, outgoing: map[int64][]int{}, directions: make([][2]int, len(d.Segments)), trie: []trieNode{{next: map[int]int{}}}}
	b := d.Metadata.EndpointBounds
	if !((d.Metadata.Version == 1 && d.Metadata.Profile == "driving-distance-v1") || (d.Metadata.Version == GraphVersion && d.Metadata.Profile == Profile)) || !(Point{b[0], b[1]}).Valid() {
		return nil, fmt.Errorf("unsupported routing metadata")
	}
	if b[0] >= b[2] || b[1] >= b[3] || !(Point{b[2], b[3]}).Valid() || len(d.Nodes) == 0 || len(d.Segments) == 0 {
		return nil, fmt.Errorf("invalid routing graph")
	}
	for _, n := range d.Nodes {
		if _, ok := s.nodes[n.ID]; ok || n.ID <= 0 || !n.Point.Valid() {
			return nil, fmt.Errorf("invalid routing node %d", n.ID)
		}
		s.nodes[n.ID] = n.Point
	}
	refs := map[EdgeRef]int{}
	ids := map[string]bool{}
	for i, v := range d.Segments {
		a, ok := s.nodes[v.From]
		z, yes := s.nodes[v.To]
		if !ok || !yes || v.From == v.To || v.Way <= 0 || v.ID == "" || ids[v.ID] || (!v.Forward && !v.Backward) {
			return nil, fmt.Errorf("invalid routing segment %s", v.ID)
		}
		ids[v.ID] = true
		l := Distance(a, z)
		if l <= 0 {
			return nil, fmt.Errorf("zero routing segment")
		}
		s.directions[i] = [2]int{-1, -1}
		for dir, allow := range []bool{v.Forward, v.Backward} {
			if !allow {
				continue
			}
			e := edge{v.From, v.To, i, dir == 1, l}
			if dir == 1 {
				e.from, e.to = e.to, e.from
			}
			id := len(s.edges)
			s.edges = append(s.edges, e)
			s.outgoing[e.from] = append(s.outgoing[e.from], id)
			s.directions[i][dir] = id
			refs[EdgeRef{v.ID, dir == 1}] = id
		}
	}
	for _, ban := range d.Bans {
		if len(ban.Path) < 2 {
			return nil, fmt.Errorf("invalid routing ban")
		}
		t := 0
		last := -1
		for _, ref := range ban.Path {
			id, ok := refs[ref]
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
		if g.Segment == "" || g.Way <= 0 || ids[g.Segment] || !g.From.Valid() || !g.To.Valid() {
			return nil, fmt.Errorf("invalid snap guard")
		}
		ids[g.Segment] = true
	}
	s.restrictedNodes = map[int64]bool{}
	for _, ban := range d.Bans {
		for _, ref := range ban.Path {
			e := s.edges[refs[ref]]
			s.restrictedNodes[e.from] = true
			s.restrictedNodes[e.to] = true
		}
	}
	s.zones = make([]int, len(s.segments))
	byNode := map[int64][]int{}
	s.publicNodes = map[int64]bool{}
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
			v := s.segments[queue[j]]
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

type Error struct{ Outcome, Endpoint string }

func (e *Error) Error() string { return e.Endpoint + ": " + e.Outcome }

type Snap struct {
	Point             Point   `json:"point"`
	Distance          float64 `json:"distance_meters"`
	Segment           string  `json:"segment"`
	Requested         Point   `json:"requested"`
	SelectionReason   string  `json:"selection_reason,omitempty"`
	NearestDistance   float64 `json:"nearest_distance_meters"`
	DestinationAccess bool    `json:"destination_access,omitempty"`
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
	for i, v := range s.segments {
		if i%4096 == 0 {
			if e := ctx.Err(); e != nil {
				return Snap{}, e
			}
		}
		if !v.Snap {
			continue
		}
		a, z := s.nodes[v.From], s.nodes[v.To] // Cheap bounds rejection before projection/haversine.
		if p[1] < math.Min(a[1], z[1])-.001 || p[1] > math.Max(a[1], z[1])+.001 || p[0] < math.Min(a[0], z[0])-.002 || p[0] > math.Max(a[0], z[0])+.002 {
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
			if c.node != 0 && s.publicNodes[c.node] {
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
	for _, g := range s.guards {
		if p[1] < math.Min(g.From[1], g.To[1])-.001 || p[1] > math.Max(g.From[1], g.To[1])+.001 || p[0] < math.Min(g.From[0], g.To[0])-.002 || p[0] > math.Max(g.From[0], g.To[0])+.002 {
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
	x, y := s.segments[a.index], s.segments[b.index]
	if !x.Service || y.Service || !x.Forward || !x.Backward || !y.Forward || !y.Backward || x.Elevated || y.Elevated || s.zones[a.index] != 0 || s.zones[b.index] != 0 {
		return false
	}
	// Source ways often have several short geometry pieces before the junction.
	// Walk only this same unrestricted service way, within 20 m and 32 nodes.
	type visit struct {
		node     int64
		distance float64
	}
	queue := []visit{{x.From, Distance(a.Point, s.nodes[x.From])}, {x.To, Distance(a.Point, s.nodes[x.To])}}
	seen := map[int64]float64{}
	for i := 0; i < len(queue) && i < 32; i++ {
		v := queue[i]
		if v.distance > 20 || s.restrictedNodes[v.node] {
			continue
		}
		if old, ok := seen[v.node]; ok && old <= v.distance {
			continue
		}
		seen[v.node] = v.distance
		if (v.node == y.From || v.node == y.To) && v.distance+Distance(s.nodes[v.node], b.Point) <= 20 {
			return !s.connectorCrossesRoad(a, b)
		}
		for _, id := range s.outgoing[v.node] {
			e := s.edges[id]
			seg := s.segments[e.segment]
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
	for _, v := range s.segments {
		if v.Way == s.segments[a.index].Way || v.Way == s.segments[b.index].Way {
			continue
		}
		if crosses(s.nodes[v.From], s.nodes[v.To]) {
			return true
		}
	}
	for _, g := range s.guards {
		if crosses(g.From, g.To) {
			return true
		}
	}
	return false
}

type Result struct {
	Geometry            []Point
	Distance            float64
	Origin, Destination Snap
	Segments            []string
}
type state struct{ edge, trie, phase int }
type item struct {
	state    state
	distance float64
}
type queue []item

func (q queue) Len() int { return len(q) }
func (q queue) Less(i, j int) bool {
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
func (q *queue) Push(x any)   { *q = append(*q, x.(item)) }
func (q *queue) Pop() any     { x := (*q)[len(*q)-1]; *q = (*q)[:len(*q)-1]; return x }
func (s *Store) Route(ctx context.Context, origin, destination Point) (Result, error) {
	if s == nil {
		return Result{}, &Error{"unavailable", "routing"}
	}
	a, e := s.snap(ctx, origin, "origin")
	if e != nil {
		return Result{}, e
	}
	b, e := s.snap(ctx, destination, "destination")
	if e != nil {
		return Result{}, e
	}
	result := Result{Origin: a, Destination: b}
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
		v := s.segments[edge.segment]
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
		v := s.segments[a.index]
		forward := v.Forward && (!v.DestinationForward || aZone != 0 || bZone != 0)
		backward := v.Backward && (!v.DestinationBackward || aZone != 0 || bZone != 0)
		if a.fraction <= b.fraction && forward || a.fraction >= b.fraction && backward {
			best = Distance(a.Point, b.Point)
			direct = true
		}
	}
	if a.node != 0 && a.node == b.node {
		best = 0
		direct = true
	}
	distances := map[state]float64{}
	previous := map[state]state{}
	q := &queue{}
	heap.Init(q)
	root := state{-1, 0, 0}
	if aZone == 0 {
		root.phase = 1
	}
	add := func(st state, d float64, prev state) {
		if old, ok := distances[st]; !ok || d < old {
			distances[st] = d
			previous[st] = prev
			heap.Push(q, item{st, d})
		}
	}
	if a.node != 0 {
		for _, id := range s.outgoing[a.node] {
			tr, ok := s.advance(0, id)
			if phase, allowed := transition(root.phase, id); ok && allowed {
				add(state{id, tr, phase}, s.edges[id].length, root)
			}
		}
	} else {
		for dir, id := range s.directions[a.index] {
			if id < 0 {
				continue
			}
			fraction := 1 - a.fraction
			if dir == 1 {
				fraction = a.fraction
			}
			tr, ok := s.advance(0, id)
			if phase, allowed := transition(root.phase, id); ok && allowed {
				add(state{id, tr, phase}, fraction*s.edges[id].length, root)
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
		for dir, id := range s.directions[b.index] {
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
			fraction := b.fraction
			if dir == 1 {
				fraction = 1 - b.fraction
			}
			v := d + fraction*s.edges[id].length
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
		cur := heap.Pop(q).(item)
		if cur.distance != distances[cur.state] {
			continue
		}
		if cur.distance >= best {
			break
		}
		iterations++
		if iterations%1024 == 0 {
			if e := ctx.Err(); e != nil {
				return Result{}, e
			}
		}
		in := s.edges[cur.state.edge]
		checkEnd(in.to, cur.state, cur.distance)
		for _, id := range s.outgoing[in.to] {
			out := s.edges[id]
			if out.segment == in.segment {
				continue
			}
			tr, ok := s.advance(cur.state.trie, id)
			if phase, allowed := transition(cur.state.phase, id); ok && allowed {
				add(state{id, tr, phase}, cur.distance+out.length, cur.state)
			}
		}
	}
	if math.IsInf(best, 1) {
		return result, &Error{"unreachable", "destination"}
	}
	result.Distance = best
	result.Geometry = []Point{a.Point}
	if !direct {
		path := []int{}
		for st := endState; st.edge >= 0; st = previous[st] {
			path = append(path, st.edge)
		}
		for i := len(path) - 1; i >= 0; i-- {
			v := s.edges[path[i]]
			result.Geometry = append(result.Geometry, s.nodes[v.to])
			result.Segments = append(result.Segments, s.segments[v.segment].ID)
		}
		if endEdge >= 0 {
			result.Segments = append(result.Segments, s.segments[s.edges[endEdge].segment].ID)
		}
	} else {
		result.Segments = []string{a.Segment}
	}
	if result.Geometry[len(result.Geometry)-1] != b.Point || len(result.Geometry) == 1 {
		result.Geometry = append(result.Geometry, b.Point)
	}
	return result, ctx.Err()
}
