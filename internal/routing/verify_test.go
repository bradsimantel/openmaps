package routing

import (
	"context"
	"testing"
)

func TestIndependentVerifierRejectsDamagedResults(t *testing.T) {
	s, path := fixture(t, true, true)
	a, err := s.Reader.Edge(path[0])
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Reader.Edge(path[2])
	if err != nil {
		t.Fatal(err)
	}
	sa, _ := s.Reader.Shape(a)
	sb, _ := s.Reader.Shape(b)
	pa, pb := clip(sa.Points, .5, .5)[0], clip(sb.Points, .5, .5)[0]
	got, err := s.RouteSnaps(context.Background(), Snap{pa, pa, 0, a.ID, .5}, Snap{pb, pb, 0, b.ID, .5}, 1000)
	if err != nil {
		t.Fatal(err)
	}
	v, err := NewPathVerifier(context.Background(), s.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Verify(context.Background(), got); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Result){
		func(r *Result) { r.Seconds++ },
		func(r *Result) { r.Meters++ },
		func(r *Result) { r.Geometry[len(r.Geometry)/2][0] += .001 },
		func(r *Result) { r.Steps[1].From = .2 },
		func(r *Result) { r.Steps[1].Edge = path[1] },
		func(r *Result) { r.Origin.GapMeters = 101 },
	} {
		bad := got
		bad.Geometry = append([]Point(nil), got.Geometry...)
		bad.Steps = append([]Step(nil), got.Steps...)
		change(&bad)
		if err := v.Verify(context.Background(), bad); err == nil {
			t.Fatal("accepted corrupted route output")
		}
	}
}
