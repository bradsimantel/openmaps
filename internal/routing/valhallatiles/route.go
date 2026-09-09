package valhallatiles

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

const Profile = "valhalla-3.6.3-go-feasibility-v1"
const ProfileNote = "Experimental public-auto subset; provider edge speeds, no traffic or turn penalties; conditional speed payloads are not evaluated; destination access and current Open Maps snap/address policy are unsupported."

// The automaton is small, explicitly capped preprocessing over complex turn
// records only. Graph topology and spatial indexes stay in tiles. Only-turn
// records in a final Valhalla tile already enumerate prohibited alternatives.
type prefix struct {
	next   map[ID]int
	fail   int
	banned bool
}
type Router struct {
	Reader                                  *Reader
	prefixes                                []prefix
	RestrictionCount, TimedRestrictionCount int
}

func NewRouter(r *Reader) (*Router, error) {
	s := &Router{Reader: r, prefixes: []prefix{{next: map[ID]int{}}}}
	for _, id := range r.TileIDs() {
		rs, err := r.Restrictions(id)
		if err != nil {
			return nil, err
		}
		for _, restriction := range rs {
			s.RestrictionCount++
			if s.RestrictionCount > 4096 {
				return nil, errors.New("complex restriction cap exceeded")
			}
			if restriction.Timed {
				s.TimedRestrictionCount++
			}
			state := 0
			for _, edge := range restriction.Path {
				if _, err := r.Edge(edge); err != nil {
					return nil, fmt.Errorf("restriction reference: %w", err)
				}
				next, ok := s.prefixes[state].next[edge]
				if !ok {
					next = len(s.prefixes)
					if next >= 65536 {
						return nil, errors.New("restriction prefix cap exceeded")
					}
					s.prefixes = append(s.prefixes, prefix{next: map[ID]int{}})
					s.prefixes[state].next[edge] = next
				}
				state = next
			}
			s.prefixes[state].banned = true
		}
	}
	var queue []int
	for _, v := range s.prefixes[0].next {
		queue = append(queue, v)
	}
	for pos := 0; pos < len(queue); pos++ {
		u := queue[pos]
		for edge, v := range s.prefixes[u].next {
			f := s.prefixes[u].fail
			for f != 0 && s.prefixes[f].next[edge] == 0 {
				f = s.prefixes[f].fail
			}
			if target := s.prefixes[f].next[edge]; target != 0 {
				s.prefixes[v].fail = target
			}
			s.prefixes[v].banned = s.prefixes[v].banned || s.prefixes[s.prefixes[v].fail].banned
			queue = append(queue, v)
		}
	}
	return s, nil
}
func (s *Router) advance(state int, edge ID) (int, bool) {
	for state != 0 && s.prefixes[state].next[edge] == 0 {
		state = s.prefixes[state].fail
	}
	if next := s.prefixes[state].next[edge]; next != 0 {
		state = next
	}
	return state, s.prefixes[state].banned
}

// Allowed is an intentionally narrower experimental profile: public auto roads,
// no destination/private roads, tracks, ferries or conditional access. Numeric
// dimensions are checked when encoded, regardless of the provider's mode mask.
// This cannot recover discarded raw OSM tags or reproduce driving-time-v4.
func (s *Router) Allowed(e Edge) (bool, error) {
	if e.Shortcut || e.Access&1 == 0 || e.Destination || e.Speed == 0 || e.Speed > 140 || e.Length <= 0 {
		return false, nil
	}
	if e.Use > 11 || e.Use == 3 || e.Use == 7 {
		return false, nil
	}
	rules, err := s.Reader.AccessRules(e)
	if err != nil {
		return false, err
	}
	for _, r := range rules {
		switch r.Type {
		case 1, 2, 3, 4, 5:
			thresholds := [6]uint64{0, 190, 200, 500, 180, 110}
			if r.Value < thresholds[r.Type] {
				return false, nil
			}
		default:
			if r.Modes&1 != 0 {
				return false, nil
			}
		}
	}
	return true, nil
}
func (s *Router) opposite(e Edge) (Edge, error) {
	n, err := s.Reader.Node(e.End)
	if err != nil {
		return Edge{}, err
	}
	if int(e.OppIndex) >= n.EdgeCount {
		return Edge{}, errors.New("invalid opposing edge index")
	}
	return s.Reader.Edge(e.End.WithIndex(n.EdgeIndex + int(e.OppIndex)))
}

type Snap struct {
	Requested, Point Point
	GapMeters        float64
	Edge             ID
	Fraction         float64
}
type direction struct {
	edge     Edge
	fraction float64
}

func (s *Router) directions(snap Snap) ([]direction, error) {
	e, err := s.Reader.Edge(snap.Edge)
	if err != nil {
		return nil, err
	}
	opp, err := s.opposite(e)
	if err != nil {
		return nil, err
	}
	var out []direction
	for _, d := range []direction{{e, snap.Fraction}, {opp, 1 - snap.Fraction}} {
		ok, err := s.Allowed(d.edge)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, d)
		}
	}
	return out, nil
}
func project(p Point, shape []Point) (Point, float64, float64) {
	total := 0.0
	for i := 1; i < len(shape); i++ {
		total += Distance(shape[i-1], shape[i])
	}
	best, at, walk := math.Inf(1), 0.0, 0.0
	var point Point
	k := math.Cos(p[1] * math.Pi / 180)
	for i := 1; i < len(shape); i++ {
		a, b := shape[i-1], shape[i]
		dx, dy := (b[0]-a[0])*k, b[1]-a[1]
		f := 0.0
		if dx*dx+dy*dy > 0 {
			f = max(0, min(1, ((p[0]-a[0])*k*dx+(p[1]-a[1])*dy)/(dx*dx+dy*dy)))
		}
		q := Point{a[0] + f*(b[0]-a[0]), a[1] + f*(b[1]-a[1])}
		gap := Distance(p, q)
		length := Distance(a, b)
		if gap < best {
			best, at, point = gap, walk+f*length, q
		}
		walk += length
	}
	if total == 0 {
		return shape[0], Distance(p, shape[0]), 0
	}
	return point, best, at / total
}
func (s *Router) Snap(p Point) (Snap, error) {
	ids, err := s.Reader.Candidates(p, 100)
	if err != nil {
		return Snap{}, err
	}
	best := Snap{Requested: p, GapMeters: math.Inf(1)}
	for _, id := range ids {
		e, err := s.Reader.Edge(id)
		if err != nil {
			return Snap{}, err
		}
		if e.Shortcut || e.Class < 2 || e.Bridge || e.Tunnel {
			continue
		}
		ok, err := s.Allowed(e)
		if err != nil {
			return Snap{}, err
		}
		if !ok {
			opp, err := s.opposite(e)
			if err != nil {
				return Snap{}, err
			}
			ok, err = s.Allowed(opp)
			if err != nil {
				return Snap{}, err
			}
		}
		if !ok {
			continue
		}
		shape, err := s.Reader.Shape(e)
		if err != nil {
			return Snap{}, err
		}
		q, gap, f := project(p, shape.Points)
		if gap <= 100 && (gap < best.GapMeters || gap == best.GapMeters && id < best.Edge) {
			best = Snap{p, q, gap, id, f}
		}
	}
	if math.IsInf(best.GapMeters, 1) {
		return Snap{}, errors.New("unsnappable within 100 metres")
	}
	return best, nil
}

// departures traverses hierarchy transitions as zero-cost node equivalences.
// It retains incoming-edge identity and automaton state. No hierarchy pruning,
// shortcut edge, or superseded-edge pruning participates in this prototype.
func (s *Router) departures(id ID) ([]Edge, []ID, error) {
	queue := []ID{id}
	seen := map[ID]bool{id: true}
	var edges []Edge
	for pos := 0; pos < len(queue); pos++ {
		n, err := s.Reader.Node(queue[pos])
		if err != nil {
			return nil, nil, err
		}
		if n.Access&1 == 0 {
			continue
		}
		for i := 0; i < n.EdgeCount; i++ {
			e, err := s.Reader.Edge(n.ID.WithIndex(n.EdgeIndex + i))
			if err != nil {
				return nil, nil, err
			}
			if !e.Shortcut {
				edges = append(edges, e)
			}
		}
		trans, err := s.Reader.Transitions(n)
		if err != nil {
			return nil, nil, err
		}
		for _, to := range trans {
			if !seen[to] {
				seen[to] = true
				queue = append(queue, to)
				if len(queue) > 3 {
					return nil, nil, errors.New("invalid hierarchy transition closure")
				}
			}
		}
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	return edges, queue, nil
}

type state struct {
	node, last  ID
	restriction int
}
type label struct {
	state state
	cost  float64
	prev  int
	step  Step
}
type queued struct {
	label int
	cost  float64
}
type minQueue []queued

func (q minQueue) Len() int { return len(q) }
func (q minQueue) Less(i, j int) bool {
	if q[i].cost != q[j].cost {
		return q[i].cost < q[j].cost
	}
	return q[i].label < q[j].label
}
func (q minQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *minQueue) Push(v any)   { *q = append(*q, v.(queued)) }
func (q *minQueue) Pop() any     { old := *q; v := old[len(old)-1]; *q = old[:len(old)-1]; return v }

type Step struct {
	Edge     ID
	From, To float64
}
type Metrics struct {
	SnapMilliseconds, SearchMilliseconds, GeometryMilliseconds                 float64
	Settled, Labels, QueuePeak, SimpleRejected, ComplexRejected, UTurnRejected int
}
type Result struct {
	Profile             string
	ProfileNote         string
	Origin, Destination Snap
	Meters, Seconds     float64
	Geometry            []Point
	Steps               []Step
	Metrics             Metrics
}

func (s *Router) Route(ctx context.Context, from, to Point, maxLabels int) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	start := time.Now()
	a, err := s.Snap(from)
	if err != nil {
		return Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	b, err := s.Snap(to)
	if err != nil {
		return Result{}, err
	}
	out, err := s.RouteSnaps(ctx, a, b, maxLabels)
	out.Metrics.SnapMilliseconds = float64(time.Since(start).Microseconds())/1000 - out.Metrics.SearchMilliseconds - out.Metrics.GeometryMilliseconds
	return out, err
}
func (s *Router) RouteSnaps(ctx context.Context, a, b Snap, maxLabels int) (Result, error) {
	out := Result{Profile: Profile, ProfileNote: ProfileNote, Origin: a, Destination: b}
	start := time.Now()
	if maxLabels < 1 || maxLabels > 200000 {
		return out, errors.New("label limit must be 1..200000")
	}
	if !a.Point.valid() || !b.Point.valid() || math.IsNaN(a.Fraction) || math.IsNaN(b.Fraction) || a.Fraction < 0 || a.Fraction > 1 || b.Fraction < 0 || b.Fraction > 1 {
		return out, errors.New("invalid snap")
	}
	sources, err := s.directions(a)
	if err != nil {
		return out, err
	}
	targets, err := s.directions(b)
	if err != nil {
		return out, err
	}
	if len(sources) == 0 || len(targets) == 0 {
		return out, errors.New("snap has no permitted directions")
	}
	if a.Edge == b.Edge && a.Fraction == b.Fraction {
		out.Geometry = []Point{a.Point, b.Point}
		return out, nil
	}
	labels := make([]label, 0, 1024)
	best := map[state]int{}
	q := minQueue{}
	push := func(st state, cost float64, prev int, step Step) error {
		if i, ok := best[st]; ok && labels[i].cost <= cost {
			return nil
		}
		if len(labels) >= maxLabels {
			return errors.New("query label budget exhausted")
		}
		i := len(labels)
		labels = append(labels, label{st, cost, prev, step})
		best[st] = i
		heap.Push(&q, queued{i, cost})
		out.Metrics.QueuePeak = max(out.Metrics.QueuePeak, len(q))
		return nil
	}
	goalCost := math.Inf(1)
	goalPrev := -1
	goalStep := Step{Edge: invalid}
	for _, src := range sources {
		for _, dst := range targets {
			if src.edge.ID == dst.edge.ID && dst.fraction >= src.fraction {
				cost := src.edge.Length * (dst.fraction - src.fraction) * 3.6 / float64(src.edge.Speed)
				if cost < goalCost {
					goalCost, goalStep = cost, Step{src.edge.ID, src.fraction, dst.fraction}
				}
			}
		}
		if src.fraction == 0 {
			n, err := s.Reader.Start(src.edge.ID)
			if err != nil {
				return out, err
			}
			if err = push(state{n.ID, invalid, 0}, 0, -1, Step{Edge: invalid}); err != nil {
				return out, err
			}
			continue
		}
		if src.fraction == 1 {
			if err = push(state{src.edge.End, invalid, 0}, 0, -1, Step{Edge: invalid}); err != nil {
				return out, err
			}
			continue
		}
		next, banned := s.advance(0, src.edge.ID)
		if banned {
			continue
		}
		cost := src.edge.Length * (1 - src.fraction) * 3.6 / float64(src.edge.Speed)
		if err = push(state{src.edge.End, src.edge.ID, next}, cost, -1, Step{src.edge.ID, src.fraction, 1}); err != nil {
			return out, err
		}
	}
	goalNodes := map[ID]bool{}
	for _, dst := range targets {
		if dst.fraction == 1 {
			goalNodes[dst.edge.End] = true
		}
		if dst.fraction == 0 {
			n, err := s.Reader.Start(dst.edge.ID)
			if err != nil {
				return out, err
			}
			goalNodes[n.ID] = true
		}
	}
	for len(q) > 0 {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		item := heap.Pop(&q).(queued)
		cur := labels[item.label]
		if best[cur.state] != item.label {
			continue
		}
		if cur.cost >= goalCost {
			break
		}
		out.Metrics.Settled++
		edges, nodes, err := s.departures(cur.state.node)
		if err != nil {
			return out, err
		}
		for _, n := range nodes {
			if goalNodes[n] {
				goalCost, goalPrev, goalStep = cur.cost, item.label, Step{Edge: invalid}
			}
		}
		var previous Edge
		if cur.state.last != invalid {
			previous, err = s.Reader.Edge(cur.state.last)
			if err != nil {
				return out, err
			}
		}
		for _, edge := range edges {
			if cur.state.last != invalid {
				if previous.OppLocalIndex == edge.LocalIndex {
					out.Metrics.UTurnRejected++
					continue
				}
				if edge.LocalIndex < 8 && previous.Restrictions&(1<<edge.LocalIndex) != 0 {
					out.Metrics.SimpleRejected++
					continue
				}
			}
			allowed, err := s.Allowed(edge)
			if err != nil {
				return out, err
			}
			if !allowed {
				continue
			}
			next, banned := s.advance(cur.state.restriction, edge.ID)
			if banned {
				out.Metrics.ComplexRejected++
				continue
			}
			for _, dst := range targets {
				if edge.ID == dst.edge.ID && dst.fraction > 0 {
					cost := cur.cost + edge.Length*dst.fraction*3.6/float64(edge.Speed)
					if cost < goalCost {
						goalCost, goalPrev, goalStep = cost, item.label, Step{edge.ID, 0, dst.fraction}
					}
				}
			}
			cost := cur.cost + edge.Length*3.6/float64(edge.Speed)
			if cost >= goalCost {
				continue
			}
			if err = push(state{edge.End, edge.ID, next}, cost, item.label, Step{edge.ID, 0, 1}); err != nil {
				return out, err
			}
		}
	}
	out.Metrics.Labels = len(labels)
	out.Metrics.SearchMilliseconds = float64(time.Since(start).Microseconds()) / 1000
	if math.IsInf(goalCost, 1) {
		return out, errors.New("unreachable in the supported sample/profile")
	}
	for i := goalPrev; i >= 0; i = labels[i].prev {
		if labels[i].step.Edge != invalid {
			out.Steps = append(out.Steps, labels[i].step)
		}
	}
	for i, j := 0, len(out.Steps)-1; i < j; i, j = i+1, j-1 {
		out.Steps[i], out.Steps[j] = out.Steps[j], out.Steps[i]
	}
	if goalStep.Edge != invalid {
		out.Steps = append(out.Steps, goalStep)
	}
	start = time.Now()
	for _, step := range out.Steps {
		e, err := s.Reader.Edge(step.Edge)
		if err != nil {
			return out, err
		}
		shape, err := s.Reader.Shape(e)
		if err != nil {
			return out, err
		}
		part := clip(shape.Points, step.From, step.To)
		out.Meters += e.Length * (step.To - step.From)
		out.Seconds += e.Length * (step.To - step.From) * 3.6 / float64(e.Speed)
		if len(out.Geometry) > 0 && len(part) > 0 {
			if Distance(out.Geometry[len(out.Geometry)-1], part[0]) > .5 {
				return out, errors.New("route geometry is not contiguous")
			}
			part = part[1:]
		}
		out.Geometry = append(out.Geometry, part...)
	}
	if len(out.Geometry) < 2 {
		out.Geometry = []Point{a.Point, b.Point}
	}
	out.Metrics.GeometryMilliseconds = float64(time.Since(start).Microseconds()) / 1000
	return out, nil
}
func clip(points []Point, from, to float64) []Point {
	length := 0.0
	for i := 1; i < len(points); i++ {
		length += Distance(points[i-1], points[i])
	}
	lo, hi := length*from, length*to
	var out []Point
	walk := 0.0
	for i := 1; i < len(points); i++ {
		a, b := points[i-1], points[i]
		n := Distance(a, b)
		if n > 0 && walk+n >= lo && walk <= hi {
			f, g := max(0, (lo-walk)/n), min(1, (hi-walk)/n)
			if f <= g {
				p := Point{a[0] + f*(b[0]-a[0]), a[1] + f*(b[1]-a[1])}
				q := Point{a[0] + g*(b[0]-a[0]), a[1] + g*(b[1]-a[1])}
				if len(out) == 0 {
					out = append(out, p)
				}
				out = append(out, q)
			}
		}
		walk += n
	}
	return out
}
