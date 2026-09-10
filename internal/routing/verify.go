package routing

// Offline path checking deliberately does not use search labels, turn automata,
// potentials or provider shortcuts. It matches prohibited source sequences
// directly and recalculates access, cost and shape continuity from records.
import (
	"context"
	"errors"
	"fmt"
	"math"
)

type PathVerifier struct {
	r     *Reader
	rules map[ID][][]ID
}

func NewPathVerifier(ctx context.Context, r *Reader) (*PathVerifier, error) {
	v := &PathVerifier{r: r, rules: map[ID][][]ID{}}
	count := 0
	for _, id := range r.TileIDs() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rules, err := r.Restrictions(id)
		if err != nil {
			return nil, err
		}
		for _, rule := range rules {
			count++
			if count > maxTurnRules {
				return nil, errors.New("verification restriction budget exceeded")
			}
			if len(rule.Path) < 2 {
				return nil, errors.New("short prohibited source sequence")
			}
			v.rules[rule.Path[0]] = append(v.rules[rule.Path[0]], rule.Path)
		}
	}
	return v, nil
}

func (v *PathVerifier) Verify(ctx context.Context, result Result) error {
	r := v.r
	if len(result.Geometry) < 2 || len(result.Geometry) > 1000000 {
		return errors.New("invalid route geometry extent")
	}
	if len(result.Steps) == 0 {
		if result.Origin.Edge != result.Destination.Edge || result.Origin.Fraction != result.Destination.Fraction || result.Meters != 0 || result.Seconds != 0 {
			return errors.New("unexplained empty path")
		}
	}
	var previous Edge
	var previousEnd Point
	meters, seconds := 0.0, 0.0
	geometryIndex := 0
	for i, step := range result.Steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if math.IsNaN(step.From) || math.IsNaN(step.To) || step.From < 0 || step.To > 1 || step.From > step.To || (i > 0 && step.From != 0) || (i+1 < len(result.Steps) && step.To != 1) {
			return errors.New("invalid partial endpoint placement")
		}
		e, err := r.Edge(step.Edge)
		if err != nil {
			return err
		}
		if e.Access&1 == 0 || e.Shortcut || e.Destination || e.Speed < 1 || e.Speed > 140 || e.Length <= 0 || e.Use > 11 || e.Use == 3 || e.Use == 7 {
			return fmt.Errorf("forbidden output edge %s", e.ID)
		}
		rules, err := r.AccessRules(e)
		if err != nil {
			return err
		}
		for _, rule := range rules {
			minimum, dimension := map[uint8]uint64{1: 190, 2: 200, 3: 500, 4: 180, 5: 110}[rule.Type]
			if dimension && rule.Value < minimum || !dimension && rule.Modes&1 != 0 {
				return fmt.Errorf("output violates access rule on %s", e.ID)
			}
		}
		start, err := r.Start(e.ID)
		if err != nil {
			return err
		}
		if i > 0 {
			if previous.OppLocalIndex == e.LocalIndex || e.LocalIndex < 8 && previous.Restrictions&(1<<e.LocalIndex) != 0 {
				return errors.New("output violates simple turn or reversal")
			}
			// Traverse only explicit reciprocal hierarchy transitions, requiring
			// public-auto access at every joining node. Never join by coordinates.
			queue := []ID{previous.End}
			seen := map[ID]bool{previous.End: true}
			found := false
			for j := 0; j < len(queue); j++ {
				n, err := r.Node(queue[j])
				if err != nil {
					return err
				}
				if n.Access&1 == 0 {
					continue
				}
				if n.ID == start.ID {
					found = true
					break
				}
				trans, err := r.Transitions(n)
				if err != nil {
					return err
				}
				for _, id := range trans {
					if !seen[id] {
						seen[id] = true
						queue = append(queue, id)
						if len(queue) > 8 {
							return errors.New("verification transition budget exceeded")
						}
					}
				}
			}
			if !found {
				return errors.New("source path is disconnected or crosses blocked node")
			}
		}
		for _, rule := range v.rules[e.ID] {
			if len(rule) > len(result.Steps)-i {
				continue
			}
			same := true
			for j, id := range rule {
				if result.Steps[i+j].Edge != id {
					same = false
					break
				}
			}
			if same {
				return errors.New("output contains prohibited complex source sequence")
			}
		}
		shape, err := r.Shape(e)
		if err != nil {
			return err
		}
		part := clip(shape.Points, step.From, step.To)
		if len(part) == 0 {
			return errors.New("empty partial source shape")
		}
		if i > 0 && Distance(previousEnd, part[0]) > .5 {
			return errors.New("source geometry gap")
		}
		if i == 0 && Distance(part[0], result.Origin.Point) > .1 {
			return errors.New("path starts away from selected snap")
		}
		previousEnd = part[len(part)-1]
		if i > 0 {
			part = part[1:]
		}
		for _, point := range part {
			if geometryIndex >= len(result.Geometry) || Distance(result.Geometry[geometryIndex], point) > .001 {
				return errors.New("returned geometry differs from clipped source vertices")
			}
			geometryIndex++
		}
		meters += e.Length * (step.To - step.From)
		seconds += e.Length * (step.To - step.From) * 3.6 / float64(e.Speed)
		previous = e
	}
	if len(result.Steps) > 0 && (geometryIndex != len(result.Geometry) || Distance(previousEnd, result.Destination.Point) > .1) {
		return errors.New("path ends away from selected snap")
	}
	if math.Abs(meters-result.Meters) > 1e-7 || math.Abs(seconds-result.Seconds) > 1e-7 {
		return errors.New("recomputed path cost differs")
	}
	for _, snap := range []Snap{result.Origin, result.Destination} {
		if !snap.Point.valid() || snap.GapMeters < 0 || snap.GapMeters > 100 || math.Abs(Distance(snap.Requested, snap.Point)-snap.GapMeters) > .01 {
			return errors.New("invalid snap gap metadata")
		}
	}
	if Distance(result.Geometry[0], result.Origin.Point) > .1 || Distance(result.Geometry[len(result.Geometry)-1], result.Destination.Point) > .1 {
		return errors.New("geometry endpoints differ from selected snaps")
	}
	return nil
}
