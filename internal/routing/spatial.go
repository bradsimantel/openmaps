package routing

import (
	"math"
	"sort"
)

// A packed bounding-volume tree stores each feature exactly once, including
// long ways and polygons. Leaves produce a conservative candidate superset;
// existing projection/containment predicates make the final decision.
type box [4]float64

func segmentBox(a, b Point) box {
	return box{math.Min(a[0], b[0]), math.Min(a[1], b[1]), math.Max(a[0], b[0]), math.Max(a[1], b[1])}
}
func geometryBox(ps []Point) box {
	b := box{180, 90, -180, -90}
	for _, p := range ps {
		b[0] = math.Min(b[0], p[0])
		b[1] = math.Min(b[1], p[1])
		b[2] = math.Max(b[2], p[0])
		b[3] = math.Max(b[3], p[1])
	}
	return b
}
func nearBox(p Point, meters float64) box {
	dy := meters / 110000
	dx := dy / math.Max(.01, math.Cos(p[1]*math.Pi/180))
	return box{p[0] - dx, p[1] - dy, p[0] + dx, p[1] + dy}
}
func coordinateBox(p Point) box {
	b := nearBox(p, SnapLimit)
	b[0] = math.Min(b[0], p[0]-.002)
	b[2] = math.Max(b[2], p[0]+.002)
	b[1] = math.Min(b[1], p[1]-.001)
	b[3] = math.Max(b[3], p[1]+.001)
	return b
}
func (a box) intersects(b box) bool {
	return a[0] <= b[2] && a[2] >= b[0] && a[1] <= b[3] && a[3] >= b[1]
}

type spatialNode struct {
	bounds                  box
	left, right, start, end int32
}
type spatialIndex struct {
	nodes []spatialNode
	ids   []int32
}

func buildSpatial(bounds []box) spatialIndex {
	s := spatialIndex{ids: make([]int32, len(bounds))}
	for i := range s.ids {
		s.ids[i] = int32(i)
	}
	var build func(int, int) int32
	build = func(lo, hi int) int32 {
		b := box{180, 90, -180, -90}
		for _, id := range s.ids[lo:hi] {
			v := bounds[id]
			b[0] = math.Min(b[0], v[0])
			b[1] = math.Min(b[1], v[1])
			b[2] = math.Max(b[2], v[2])
			b[3] = math.Max(b[3], v[3])
		}
		id := int32(len(s.nodes))
		s.nodes = append(s.nodes, spatialNode{bounds: b, left: -1, right: -1, start: int32(lo), end: int32(hi)})
		if hi-lo > 16 {
			axis := 0
			if b[3]-b[1] > b[2]-b[0] {
				axis = 1
			}
			sort.Slice(s.ids[lo:hi], func(i, j int) bool {
				a, c := s.ids[lo+i], s.ids[lo+j]
				x, y := bounds[a][axis]+bounds[a][axis+2], bounds[c][axis]+bounds[c][axis+2]
				if x == y {
					return a < c
				}
				return x < y
			})
			mid := (lo + hi) / 2
			left, right := build(lo, mid), build(mid, hi)
			s.nodes[id].left = left
			s.nodes[id].right = right
		}
		return id
	}
	if len(bounds) > 0 {
		build(0, len(bounds))
	}
	return s
}
func (s spatialIndex) query(b box) []int {
	out := []int{}
	if len(s.nodes) == 0 {
		return out
	}
	stack := []int32{0}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		n := s.nodes[id]
		if !b.intersects(n.bounds) {
			continue
		}
		if n.left < 0 {
			for _, v := range s.ids[n.start:n.end] {
				out = append(out, int(v))
			}
		} else {
			stack = append(stack, n.right, n.left)
		}
	}
	sort.Ints(out)
	return out
}
func (s *Store) buildSpatial() {
	b := make([]box, len(s.segments))
	for i, v := range s.segments {
		b[i] = segmentBox(s.point(v.From), s.point(v.To))
	}
	s.segmentIndex = buildSpatial(b)
	b = make([]box, len(s.guards))
	for i, v := range s.guards {
		b[i] = segmentBox(v.From, v.To)
	}
	s.guardIndex = buildSpatial(b)
	b = make([]box, len(s.access.Areas))
	for i, v := range s.access.Areas {
		b[i] = geometryBox(v.Geometry)
	}
	s.areaIndex = buildSpatial(b)
	b = make([]box, len(s.access.Ways))
	for i, v := range s.access.Ways {
		b[i] = geometryBox(v.Geometry)
	}
	s.accessIndex = buildSpatial(b)
	s.driveways = map[int64][]drivewayLink{}
	for _, w := range s.access.Ways {
		if w.Driveway {
			for i := 1; i < len(w.Nodes); i++ {
				a, b := w.Nodes[i-1], w.Nodes[i]
				d := Distance(w.Geometry[i-1], w.Geometry[i])
				s.driveways[a] = append(s.driveways[a], drivewayLink{b, d})
				s.driveways[b] = append(s.driveways[b], drivewayLink{a, d})
			}
		}
	}
}

type drivewayLink struct {
	to     int64
	length float64
}

func (b box) padMeters(meters float64) box {
	dy := meters / 110000
	latitude := math.Max(math.Abs(b[1]), math.Abs(b[3]))
	dx := dy / math.Max(.01, math.Cos(latitude*math.Pi/180))
	return box{b[0] - dx, b[1] - dy, b[2] + dx, b[3] + dy}
}
