package routing

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
)

// Geographic A* uses an audited Lipschitz embedding of this prepared graph:
// hierarchy-equivalent nodes share one coordinate. The minimum seconds/metre
// over every permitted ordinary edge is a lower bound, without level pruning.
// Provider integer lengths do not automatically dominate spherical distances.
type potentialReceipt struct {
	Schema             string  `json:"schema"`
	GraphReceiptSHA256 string  `json:"graph_receipt_sha256"`
	SecondsPerMeter    float64 `json:"seconds_per_meter"`
	Edges              int64   `json:"edges"`
	MinimumEdge        ID      `json:"minimum_edge"`
	MissingReferences  int64   `json:"missing_references"`
}
type canonicalRecord struct {
	id    ID
	point Point
	valid bool
}
type nodeRecord struct {
	id    ID
	n     Node
	valid bool
}
type edgeRecord struct {
	id    ID
	e     Edge
	valid bool
}
type recordCache struct {
	canonical [65536]canonicalRecord
	nodes     [16384]nodeRecord
	edges     [32768]edgeRecord
}

func recordSlot(id ID, mask uint64) int {
	x := uint64(id)
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return int(x & mask)
}
func (r *Reader) canonical(id ID) (Point, error) {
	if r.records != nil {
		c := r.records.canonical[recordSlot(id, 65535)]
		if c.valid && c.id == id {
			return c.point, nil
		}
	}
	queue := [8]ID{id}
	n := 1
	best := id
	var point Point
	for pos := 0; pos < n; pos++ {
		node, err := r.Node(queue[pos])
		if err != nil {
			return point, err
		}
		if pos == 0 || node.ID < best {
			best, point = node.ID, node.Point
		}
		transitions, err := r.Transitions(node)
		if err != nil {
			return point, err
		}
		for _, to := range transitions {
			// This defines the potential on retained vertices only. Ordinary traversal
			// still reports every required absent dependency; no missing road is added.
			if _, ok := r.index[to.Base()]; !ok {
				continue
			}
			other, err := r.Node(to)
			if err != nil {
				return point, err
			}
			reverse, err := r.Transitions(other)
			if err != nil {
				return point, err
			}
			reciprocal := false
			for _, back := range reverse {
				reciprocal = reciprocal || back == node.ID
			}
			if !reciprocal {
				return point, errors.New("nonreciprocal hierarchy transition")
			}
			seen := false
			for i := 0; i < n; i++ {
				seen = seen || queue[i] == to
			}
			if seen {
				continue
			}
			if n == len(queue) {
				return point, errors.New("hierarchy node equivalence exceeds eight records")
			}
			queue[n] = to
			n++
		}
	}
	if r.records != nil {
		for i := 0; i < n; i++ {
			r.records.canonical[recordSlot(queue[i], 65535)] = canonicalRecord{queue[i], point, true}
		}
	}
	return point, nil
}
func PrepareScoutPotential(ctx context.Context, dir string) error {
	r, err := OpenPreparedScout(ctx, dir, 64<<20)
	if err != nil {
		return err
	}
	defer r.Close()
	r.records = &recordCache{}
	s := &Router{Reader: r}
	pin := potentialReceipt{Schema: "openmaps-scout-potential-v1", SecondsPerMeter: math.Inf(1)}
	graph, err := readBoundedFile(filepath.Join(dir, "receipt.json"), maxPreparedIndex)
	if err != nil {
		return err
	}
	if hexSum(graph) != r.preparedSHA {
		return errors.New("graph receipt changed during preparation")
	}
	pin.GraphReceiptSHA256 = hexSum(graph)
	for _, id := range r.TileIDs() {
		t, err := r.get(id)
		if err != nil {
			return err
		}
		for i := 0; i < t.nodes; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, err := r.Node(id.WithIndex(i))
			if err != nil {
				return err
			}
			from, err := r.canonical(n.ID)
			if err != nil {
				return err
			}
			for j := 0; j < n.EdgeCount; j++ {
				e, err := r.Edge(id.WithIndex(n.EdgeIndex + j))
				if err != nil {
					return err
				}
				allowed, err := s.Allowed(e)
				if err != nil {
					return err
				}
				if !allowed {
					continue
				}
				if _, ok := r.index[e.End.Base()]; !ok {
					pin.MissingReferences++
					continue
				}
				to, err := r.canonical(e.End)
				if err != nil {
					return err
				}
				d := Distance(from, to)
				pin.Edges++
				if d > 0 {
					ratio := e.Length * 3.6 / float64(e.Speed) / d
					if ratio < pin.SecondsPerMeter {
						pin.SecondsPerMeter = ratio
						pin.MinimumEdge = e.ID
					}
				}
			}
		}
	}
	if math.IsInf(pin.SecondsPerMeter, 1) {
		pin.SecondsPerMeter = 0
	}
	// A downward margin protects the lower bound from floating point rounding.
	pin.SecondsPerMeter *= 1 - 1e-10
	return publishJSON(dir, "potential.json", pin)
}
func (s *Router) loadPotential(dir string) error {
	b, err := readBoundedFile(filepath.Join(dir, "potential.json"), 1<<20)
	if err != nil {
		return err
	}
	if len(b) > 65536 {
		return errors.New("oversized potential receipt")
	}
	var pin potentialReceipt
	if err := json.Unmarshal(b, &pin); err != nil {
		return err
	}
	graph, err := readBoundedFile(filepath.Join(dir, "receipt.json"), maxPreparedIndex)
	if err != nil {
		return err
	}
	if pin.Schema != "openmaps-scout-potential-v1" || pin.GraphReceiptSHA256 != hexSum(graph) || pin.GraphReceiptSHA256 != s.Reader.preparedSHA || math.IsNaN(pin.SecondsPerMeter) || math.IsInf(pin.SecondsPerMeter, 0) || pin.SecondsPerMeter < 0 {
		return errors.New("invalid/foreign potential")
	}
	s.secondsPerMeter = pin.SecondsPerMeter
	s.potentialSHA = hexSum(b)
	return nil
}

// EnablePotential opts into the graph-bound, offline validated A* bound.
func (s *Router) EnablePotential(dir string) error { return s.loadPotential(dir) }
