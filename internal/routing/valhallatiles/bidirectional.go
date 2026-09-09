package valhallatiles

import (
	"container/heap"
	"context"
	"errors"
	"math"
	"time"
)

type searchFront struct {
	labels  []label
	best    map[state]int
	nodes   map[ID][]int
	queue   minQueue
	settled int
}

func (f *searchFront) minimum() float64 {
	for len(f.queue) > 0 {
		top := f.queue[0]
		if f.best[f.labels[top.label].state] == top.label {
			return top.cost
		}
		heap.Pop(&f.queue)
	}
	return math.Inf(1)
}

// RouteBidirectional retains directed incoming/outgoing identities and two
// prohibition automata. At a meeting it checks the connecting simple turn and
// feeds up to 33 suffix edges through the forward history; internal suffix turns
// have already been checked by the reversed automaton. Hierarchy transitions
// preserve history. No hierarchy level is pruned and no shortcut is assumed.
func (s *Router) RouteBidirectional(ctx context.Context, from, to Point, maxLabels int) (Result, error) {
	start := time.Now()
	a, err := s.SnapContext(ctx, from)
	if err != nil {
		return Result{}, err
	}
	b, err := s.SnapContext(ctx, to)
	if err != nil {
		return Result{}, err
	}
	snapMS := float64(time.Since(start).Microseconds()) / 1000
	out, err := s.routeBidirectionalSnaps(ctx, a, b, maxLabels)
	out.Metrics.SnapMilliseconds = snapMS
	return out, err
}

// RouteBidirectionalSnaps preserves the caller's already-selected endpoints.
func (s *Router) RouteBidirectionalSnaps(ctx context.Context, a, b Snap, maxLabels int) (Result, error) {
	return s.routeBidirectionalSnaps(ctx, a, b, maxLabels)
}
func (s *Router) routeBidirectionalSnaps(ctx context.Context, a, b Snap, maxLabels int) (Result, error) {
	out := Result{Profile: CandidateProfile, ProfileNote: ProfileNote, Origin: a, Destination: b}
	start := time.Now()
	if s.turns == nil || s.reverseTurns == nil || s.secondsPerMeter <= 0 {
		return out, errors.New("bidirectional preparation unavailable")
	}
	if maxLabels < 1 || maxLabels > 4000000 {
		return out, errors.New("combined query label budget must be 1..4000000")
	}
	if !a.Point.valid() || !b.Point.valid() || math.IsNaN(a.Fraction) || math.IsNaN(b.Fraction) || a.Fraction < 0 || a.Fraction > 1 || b.Fraction < 0 || b.Fraction > 1 {
		return out, errors.New("invalid snap")
	}
	if err := ctx.Err(); err != nil {
		return out, err
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
	previousReuse := s.Reader.recordPageReuse
	if s.Reader.pages != nil {
		s.Reader.recordPageReuse = true
	}
	defer func() { s.Reader.recordPageReuse = previousReuse }()
	fronts := [2]searchFront{}
	for i := range fronts {
		fronts[i].best = map[state]int{}
		fronts[i].nodes = map[ID][]int{}
	}
	type anchor struct {
		point  Point
		cost   float64
		values landmarkValues
	}
	var anchors [2][]anchor
	if s.landmarks != nil {
		for endpoint, ends := range [][]direction{sources, targets} {
			for _, d := range ends {
				node := d.edge.End
				cost := d.edge.Length * (1 - d.fraction) * 3.6 / float64(d.edge.Speed)
				if endpoint == 1 {
					n, err := s.Reader.Start(d.edge.ID)
					if err != nil {
						return out, err
					}
					node = n.ID
					cost = d.edge.Length * d.fraction * 3.6 / float64(d.edge.Speed)
				}
				if d.fraction == 0 {
					n, err := s.Reader.Start(d.edge.ID)
					if err != nil {
						return out, err
					}
					node = n.ID
					cost = 0
				} else if d.fraction == 1 {
					node = d.edge.End
					cost = 0
				}
				point, err := s.Reader.canonical(node)
				if err != nil {
					return out, err
				}
				values, err := s.landmarks.values(node)
				if err != nil {
					return out, err
				}
				anchors[1-endpoint] = append(anchors[1-endpoint], anchor{point, cost, values})
			}
		}
	}
	potential := func(side int, id ID) (float64, error) {
		p, err := s.Reader.canonical(id)
		if err != nil {
			return 0, err
		}
		if s.landmarks != nil {
			values, err := s.landmarks.values(id)
			if err != nil {
				return 0, err
			}
			best := math.Inf(1)
			for _, a := range anchors[side] {
				bound := s.landmarks.bound(values, a.values)
				if side == 1 {
					bound = s.landmarks.bound(a.values, values)
				}
				best = math.Min(best, math.Max(bound, Distance(p, a.point)*s.secondsPerMeter)+a.cost)
			}
			return best * (1 - 1e-10), nil
		}
		value := (Distance(p, b.Point) - Distance(p, a.Point)) * s.secondsPerMeter * .5
		if side == 1 {
			value = -value
		}
		return value, nil
	}
	push := func(side int, st state, cost float64, prev int, step Step) error {
		f := &fronts[side]
		if i, ok := f.best[st]; ok && f.labels[i].cost <= cost {
			return nil
		}
		if len(fronts[0].labels)+len(fronts[1].labels) >= maxLabels {
			out.Metrics.Labels = len(fronts[0].labels) + len(fronts[1].labels)
			return ErrQueryBudget
		}
		p, err := potential(side, st.node)
		if err != nil {
			return err
		}
		i := len(f.labels)
		f.labels = append(f.labels, label{state: st, cost: cost, prev: prev, step: step})
		f.best[st] = i
		f.nodes[st.node] = append(f.nodes[st.node], i)
		heap.Push(&f.queue, queued{i, cost + p})
		out.Metrics.QueuePeak = max(out.Metrics.QueuePeak, len(fronts[0].queue)+len(fronts[1].queue))
		return nil
	}
	goalCost := math.Inf(1)
	goalF, goalB := -1, -1
	direct := Step{Edge: invalid}
	for _, src := range sources {
		for _, dst := range targets {
			if src.edge.ID == dst.edge.ID && dst.fraction >= src.fraction {
				cost := src.edge.Length * (dst.fraction - src.fraction) * 3.6 / float64(src.edge.Speed)
				if cost < goalCost {
					goalCost = cost
					direct = Step{src.edge.ID, src.fraction, dst.fraction}
				}
			}
		}
	}
	for side, ends := range [][]direction{sources, targets} {
		for _, d := range ends {
			node := d.edge.End
			cost := d.edge.Length * (1 - d.fraction) * 3.6 / float64(d.edge.Speed)
			step := Step{d.edge.ID, d.fraction, 1}
			if side == 1 {
				n, err := s.Reader.Start(d.edge.ID)
				if err != nil {
					return out, err
				}
				node = n.ID
				cost = d.edge.Length * d.fraction * 3.6 / float64(d.edge.Speed)
				step = Step{d.edge.ID, 0, d.fraction}
			}
			last := d.edge.ID
			history := 0
			if d.fraction == 0 || d.fraction == 1 {
				cost = 0
				step = Step{Edge: invalid}
				last = invalid
				if d.fraction == 0 {
					n, err := s.Reader.Start(d.edge.ID)
					if err != nil {
						return out, err
					}
					node = n.ID
				} else {
					node = d.edge.End
				}
			} else {
				var banned bool
				var err error
				if side == 0 {
					history, banned, err = s.turns.advance(0, d.edge.ID)
				} else {
					history, banned, err = s.reverseTurns.advance(0, d.edge.ID)
				}
				if err != nil {
					return out, err
				}
				if banned {
					continue
				}
			}
			if err := push(side, state{node, last, history}, cost, -1, step); err != nil {
				return out, err
			}
		}
	}
	join := func(fi, bi int) (bool, error) {
		f, b := fronts[0].labels[fi], fronts[1].labels[bi]
		if b.state.last != invalid {
			for _, id := range []ID{f.state.node, b.state.node} {
				n, err := s.Reader.Node(id)
				if err != nil {
					return false, err
				}
				if n.Access&1 == 0 {
					return false, nil
				}
			}
		}
		if f.state.last != invalid && b.state.last != invalid {
			previous, err := s.Reader.Edge(f.state.last)
			if err != nil {
				return false, err
			}
			next, err := s.Reader.Edge(b.state.last)
			if err != nil {
				return false, err
			}
			if previous.OppLocalIndex == next.LocalIndex || next.LocalIndex < 8 && previous.Restrictions&(1<<next.LocalIndex) != 0 {
				return false, nil
			}
		}
		history := f.state.restriction
		for i, walk := bi, 0; i >= 0 && walk < 33; i = fronts[1].labels[i].prev {
			step := fronts[1].labels[i].step
			if step.Edge == invalid {
				continue
			}
			walk++
			next, banned, err := s.turns.advance(history, step.Edge)
			if err != nil {
				return false, err
			}
			if banned {
				return false, nil
			}
			history = next
		}
		return true, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		lo, hi := fronts[0].minimum(), fronts[1].minimum()
		bound := lo + hi
		if s.landmarks != nil {
			// Each queue independently lower-bounds a complete source-to-target
			// path. Their maxima are safe; adding these unbalanced bounds is not.
			// Reopened states preserve the frontier argument even with quantized,
			// potentially inconsistent heuristics. All meetings check full turns.
			bound = math.Max(lo, hi)
		}
		if math.IsInf(lo, 1) || math.IsInf(hi, 1) || bound >= goalCost {
			break
		}
		side := 0
		if fronts[1].settled < fronts[0].settled {
			side = 1
		}
		f := &fronts[side]
		other := &fronts[1-side]
		item := heap.Pop(&f.queue).(queued)
		cur := f.labels[item.label]
		f.settled++
		out.Metrics.Settled++
		edges, nodes, err := s.departures(cur.state.node)
		if err != nil {
			return out, err
		}
		for _, node := range nodes {
			for _, oi := range other.nodes[node] {
				if err := ctx.Err(); err != nil {
					return out, err
				}
				ol := other.labels[oi]
				if other.best[ol.state] != oi || cur.cost+ol.cost >= goalCost {
					continue
				}
				fi, bi := item.label, oi
				if side == 1 {
					fi, bi = bi, fi
				}
				allowed, err := join(fi, bi)
				if err != nil {
					return out, err
				}
				if allowed {
					goalCost = cur.cost + ol.cost
					goalF, goalB = fi, bi
					direct = Step{Edge: invalid}
				}
			}
		}
		var last Edge
		if cur.state.last != invalid {
			last, err = s.Reader.Edge(cur.state.last)
			if err != nil {
				return out, err
			}
		}
		type traversal struct {
			edge Edge
			next ID
		}
		traversals := make([]traversal, 0, len(edges))
		if side == 0 {
			for _, edge := range edges {
				traversals = append(traversals, traversal{edge, edge.End})
			}
		} else {
			for _, id := range nodes {
				n, err := s.Reader.Node(id)
				if err != nil {
					return out, err
				}
				if n.Access&1 == 0 {
					continue
				}
				_, err = s.incoming(n, false, func(edge Edge, source ID) error { traversals = append(traversals, traversal{edge, source}); return nil })
				if err != nil {
					return out, err
				}
			}
		}
		for _, traverse := range traversals {
			edge, nextNode := traverse.edge, traverse.next
			if edge.Shortcut {
				continue
			}
			if cur.state.last != invalid {
				incoming, outgoing := last, edge
				if side == 1 {
					incoming, outgoing = edge, last
				}
				if incoming.OppLocalIndex == outgoing.LocalIndex {
					out.Metrics.UTurnRejected++
					continue
				}
				if outgoing.LocalIndex < 8 && incoming.Restrictions&(1<<outgoing.LocalIndex) != 0 {
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
			history, banned, err := s.turns.advance(cur.state.restriction, edge.ID)
			if side == 1 {
				history, banned, err = s.reverseTurns.advance(cur.state.restriction, edge.ID)
			}
			if err != nil {
				return out, err
			}
			if banned {
				out.Metrics.ComplexRejected++
				continue
			}
			cost := cur.cost + edge.Length*3.6/float64(edge.Speed)
			if cost >= goalCost {
				continue
			}
			if err := push(side, state{nextNode, edge.ID, history}, cost, item.label, Step{edge.ID, 0, 1}); err != nil {
				return out, err
			}
		}
	}
	out.Metrics.Labels = len(fronts[0].labels) + len(fronts[1].labels)
	out.Metrics.SearchMilliseconds = float64(time.Since(start).Microseconds()) / 1000
	if math.IsInf(goalCost, 1) {
		return out, ErrUnreachable
	}
	if direct.Edge != invalid {
		out.Steps = []Step{direct}
	} else {
		for i := goalF; i >= 0; i = fronts[0].labels[i].prev {
			if step := fronts[0].labels[i].step; step.Edge != invalid {
				out.Steps = append(out.Steps, step)
			}
		}
		for i, j := 0, len(out.Steps)-1; i < j; i, j = i+1, j-1 {
			out.Steps[i], out.Steps[j] = out.Steps[j], out.Steps[i]
		}
		for i := goalB; i >= 0; i = fronts[1].labels[i].prev {
			if step := fronts[1].labels[i].step; step.Edge != invalid {
				out.Steps = append(out.Steps, step)
			}
		}
	}
	s.Reader.recordPageReuse = previousReuse
	return s.finishGeometry(ctx, out)
}
