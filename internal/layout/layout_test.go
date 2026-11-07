package layout

import (
	"math"
	"testing"
)

func TestSegmentIntersects(t *testing.T) {
	tests := []struct {
		name string
		a, b Segment
		want bool
	}{
		{"clean cross", Segment{Point{0, 0}, Point{10, 10}}, Segment{Point{0, 10}, Point{10, 0}}, true},
		{"parallel apart", Segment{Point{0, 0}, Point{10, 0}}, Segment{Point{0, 5}, Point{10, 5}}, false},
		{"disjoint", Segment{Point{0, 0}, Point{1, 1}}, Segment{Point{5, 5}, Point{6, 6}}, false},
		{"touching endpoint", Segment{Point{0, 0}, Point{5, 5}}, Segment{Point{5, 5}, Point{10, 0}}, true},
		{"collinear overlap", Segment{Point{0, 0}, Point{10, 0}}, Segment{Point{5, 0}, Point{15, 0}}, true},
		{"t junction", Segment{Point{0, 0}, Point{10, 0}}, Segment{Point{5, 0}, Point{5, 5}}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Intersects(tc.b); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
			if got := tc.b.Intersects(tc.a); got != tc.want {
				t.Errorf("not symmetric: got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCircleRect(t *testing.T) {
	r := Rect{10, 10, 20, 20}
	tests := []struct {
		name string
		c    Circle
		want bool
	}{
		{"centre inside", Circle{Point{20, 20}, 3}, true},
		{"far away", Circle{Point{100, 100}, 5}, false},
		{"grazes edge", Circle{Point{5, 20}, 6}, true},
		{"just short of edge", Circle{Point{5, 20}, 4}, false},
		{"corner reach", Circle{Point{7, 7}, 5}, true},
		{"corner miss", Circle{Point{5, 5}, 5}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.IntersectsRect(r); got != tc.want {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

// grid builds n anchors spread over the page so a clean solution exists.
func grid(n int, page Rect) []Anchor {
	anchors := make([]Anchor, n)
	cols := int(math.Ceil(math.Sqrt(float64(n))))
	for i := range anchors {
		col, row := i%cols, i/cols
		anchors[i] = Anchor{
			ID:     string(rune('a' + i)),
			Number: i + 1,
			At: Point{
				X: page.X + 80 + float64(col)*120,
				Y: page.Y + 80 + float64(row)*110,
			},
		}
	}
	return anchors
}

func TestSolveNoOverlapWhenSpaceAllows(t *testing.T) {
	page := Rect{0, 0, 842, 595}
	anchors := grid(9, page)
	got := Solve(anchors, nil, DefaultConfig(page))

	if len(got) != len(anchors) {
		t.Fatalf("got %d placements for %d anchors", len(got), len(anchors))
	}

	bounds := page.Inset(DefaultConfig(page).Margin)
	for i, p := range got {
		if !bounds.ContainsRect(p.Balloon.Bounds()) {
			t.Errorf("balloon %d escaped the page: %+v", p.Number, p.Balloon)
		}
		for j := i + 1; j < len(got); j++ {
			if p.Balloon.Overlaps(got[j].Balloon) {
				t.Errorf("balloons %d and %d overlap", p.Number, got[j].Number)
			}
		}
		if !p.Clean {
			t.Errorf("balloon %d not clean on an uncrowded page: %v", p.Number, p.Issues)
		}
	}
}

func TestSolveLeaderGeometry(t *testing.T) {
	page := Rect{0, 0, 842, 595}
	anchors := grid(4, page)
	cfg := DefaultConfig(page)
	got := Solve(anchors, nil, cfg)

	for i, p := range got {
		// The leader must start exactly at its anchor.
		if p.Leader.A != anchors[i].At {
			t.Errorf("balloon %d leader starts at %+v, want %+v", p.Number, p.Leader.A, anchors[i].At)
		}
		// And stop on the balloon's edge, not at its centre.
		d := p.Leader.B.Dist(p.Balloon.C)
		if math.Abs(d-cfg.Radius) > 1e-6 {
			t.Errorf("balloon %d leader ends %v from centre, want radius %v", p.Number, d, cfg.Radius)
		}
		if p.Leader.Len() < cfg.MinLeader-1e-6 {
			t.Errorf("balloon %d leader is %v, shorter than the %v minimum", p.Number, p.Leader.Len(), cfg.MinLeader)
		}
	}
}

func TestSolveAvoidsObstacles(t *testing.T) {
	page := Rect{0, 0, 400, 400}
	// A title block occupying the whole lower half.
	obstacles := []Rect{{0, 200, 400, 200}}
	anchors := []Anchor{{ID: "a", Number: 1, At: Point{200, 190}}}

	got := Solve(anchors, obstacles, DefaultConfig(page))
	if len(got) != 1 {
		t.Fatalf("got %d placements", len(got))
	}
	if got[0].Balloon.IntersectsRect(obstacles[0]) {
		t.Errorf("balloon placed on the title block at %+v", got[0].Balloon)
	}
}

func TestSolveIsDeterministic(t *testing.T) {
	page := Rect{0, 0, 842, 595}
	anchors := grid(12, page)
	obstacles := []Rect{{300, 200, 200, 150}}

	first := Solve(anchors, obstacles, DefaultConfig(page))
	for run := 0; run < 3; run++ {
		again := Solve(anchors, obstacles, DefaultConfig(page))
		for i := range first {
			if first[i].Balloon != again[i].Balloon {
				t.Fatalf("run %d differs at balloon %d: %+v vs %+v",
					run, first[i].Number, first[i].Balloon, again[i].Balloon)
			}
		}
	}
}

// A drawing can be too dense for a clean answer. The solver must still return a
// position for every anchor and say what is wrong, rather than failing.
func TestSolveDegradesGracefullyWhenCrowded(t *testing.T) {
	// Total balloon area exceeds this printable region even with the smaller
	// reference-style circles. The fixture must genuinely be impossible.
	page := Rect{0, 0, 80, 80}
	var anchors []Anchor
	for i := 0; i < 20; i++ {
		anchors = append(anchors, Anchor{
			ID:     string(rune('a' + i)),
			Number: i + 1,
			At:     Point{40 + float64(i%4), 40 + float64(i/4)},
		})
	}

	got := Solve(anchors, nil, DefaultConfig(page))
	if len(got) != len(anchors) {
		t.Fatalf("solver dropped anchors: got %d, want %d", len(got), len(anchors))
	}
	for _, p := range got {
		if p.Balloon.R <= 0 {
			t.Errorf("balloon %d has no radius", p.Number)
		}
	}
	// With 20 balloons crammed around one point some must be flagged; a solver
	// claiming everything is fine here would be lying.
	flagged := 0
	for _, p := range got {
		if !p.Clean {
			flagged++
		}
	}
	if flagged == 0 {
		t.Error("expected crowded layout to flag at least one placement")
	}
}

func TestSolveEmptyInput(t *testing.T) {
	if got := Solve(nil, nil, DefaultConfig(Rect{0, 0, 100, 100})); len(got) != 0 {
		t.Errorf("got %d placements for no anchors", len(got))
	}
}
