package layout

import (
	"fmt"
	"math"
	"sort"
)

// Placing balloons is a small constrained-optimisation problem. Every
// characteristic needs a numbered bubble that sits close to the feature it
// annotates, without covering the geometry, without colliding with other
// bubbles, and without its leader line crossing another leader. Those goals
// conflict on a dense drawing, so the solver scores candidate positions and
// takes the least-bad arrangement rather than pretending a perfect one exists.
//
// The whole solver is deterministic: candidates are generated in a fixed order
// and ties break toward the lower index. Two runs over the same drawing produce
// byte-identical output, which matters because a revised drawing should show a
// reviewer only the balloons that genuinely moved.

// Anchor is a point on the drawing that needs a balloon.
type Anchor struct {
	ID     string `json:"id"`
	Number int    `json:"number"`
	// At is where the leader line must terminate — usually the dimension text.
	At Point `json:"at"`
	// Avoid is the callout's own bounding box, which the balloon should not
	// cover even though the leader points at it.
	Avoid Rect `json:"avoid"`
}

// Config tunes the search. Distances are in the same units as the anchors.
type Config struct {
	Radius    float64 // balloon radius
	MinLeader float64 // shortest acceptable leader
	MaxLeader float64 // longest leader before we give up on a clean spot
	Rings     int     // distinct leader lengths to try
	Angles    int     // directions sampled per ring
	Page      Rect
	Margin    float64 // keep balloons this far inside the page edge
	Passes    int     // improvement sweeps after the greedy pass
}

// DefaultConfig returns settings tuned for a drawing measured in PDF points.
func DefaultConfig(page Rect) Config {
	return Config{
		Radius:    BalloonRadius,
		MinLeader: 18,
		MaxLeader: 90,
		Rings:     5,
		Angles:    16,
		Page:      page,
		Margin:    8,
		Passes:    3,
	}
}

// Placement is a solved balloon position.
type Placement struct {
	AnchorID string   `json:"anchor_id"`
	Number   int      `json:"number"`
	Balloon  Circle   `json:"balloon"`
	Leader   Segment  `json:"leader"`
	Clean    bool     `json:"clean"`
	Issues   []string `json:"issues,omitempty"`
}

// Cost weights. Overlapping another balloon is the worst outcome because it
// makes a number unreadable; a long leader is merely ugly.
const (
	wLeader         = 1.0
	wBalloonOverlap = 400.0
	wObstacle       = 120.0
	wLeaderCross    = 60.0
	wLeaderObstacle = 8.0
	wOutOfPage      = 5000.0
)

// Solve assigns a balloon position to every anchor.
//
// obstacles are regions the balloon should not cover: drawing geometry, the
// title block, existing annotation. They are advisory — if a drawing leaves no
// clean space, the solver still returns a position and flags it, because a
// flagged balloon a human can drag is more useful than no balloon at all.
func Solve(anchors []Anchor, obstacles []Rect, cfg Config) []Placement {
	cfg = withDefaults(cfg)
	n := len(anchors)
	placements := make([]Placement, n)
	for i, a := range anchors {
		placements[i] = Placement{AnchorID: a.ID, Number: a.Number}
	}
	if n == 0 {
		return placements
	}

	bounds := cfg.Page.Inset(cfg.Margin)

	// Most constrained first: an anchor hemmed in by geometry has the fewest
	// viable positions, so it should choose before the roomy ones fill the gaps.
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	pressure := make([]float64, n)
	reach := cfg.MaxLeader + cfg.Radius
	for i, a := range anchors {
		box := Rect{a.At.X - reach, a.At.Y - reach, 2 * reach, 2 * reach}
		for _, o := range obstacles {
			if box.Intersects(o) {
				pressure[i]++
			}
		}
		for j, b := range anchors {
			if i != j && a.At.Dist(b.At) < reach {
				pressure[i] += 2 // a crowding neighbour hurts more than geometry
			}
		}
	}
	sort.SliceStable(order, func(x, y int) bool {
		return pressure[order[x]] > pressure[order[y]]
	})

	placed := make([]bool, n)
	for _, i := range order {
		best, _ := bestCandidate(i, anchors, obstacles, placements, placed, bounds, cfg)
		placements[i] = best
		placed[i] = true
	}

	// Improvement sweeps. Each anchor gets to reconsider now that it can see
	// where everyone else ended up. Greedy placement is order-dependent; this is
	// what actually resolves the collisions the first pass creates.
	for pass := 0; pass < cfg.Passes; pass++ {
		improved := false
		for _, i := range order {
			current := scoreAt(i, placements[i].Balloon.C, anchors, obstacles, placements, placed, bounds, cfg, i)
			candidate, cost := bestCandidate(i, anchors, obstacles, placements, placed, bounds, cfg)
			if cost < current-1e-9 {
				placements[i] = candidate
				improved = true
			}
		}
		if !improved {
			break
		}
	}

	for i := range placements {
		annotate(&placements[i], i, obstacles, placements, bounds)
	}
	return placements
}

// bestCandidate evaluates every sampled position around an anchor and returns
// the cheapest.
func bestCandidate(i int, anchors []Anchor, obstacles []Rect, placements []Placement,
	placed []bool, bounds Rect, cfg Config) (Placement, float64) {

	a := anchors[i]
	bestCost := math.Inf(1)
	var bestCenter Point
	found := false

	for ring := 0; ring < cfg.Rings; ring++ {
		t := 0.0
		if cfg.Rings > 1 {
			t = float64(ring) / float64(cfg.Rings-1)
		}
		leader := cfg.MinLeader + t*(cfg.MaxLeader-cfg.MinLeader)
		dist := leader + cfg.Radius

		for k := 0; k < cfg.Angles; k++ {
			theta := 2 * math.Pi * float64(k) / float64(cfg.Angles)
			c := Point{
				X: a.At.X + dist*math.Cos(theta),
				Y: a.At.Y + dist*math.Sin(theta),
			}
			cost := scoreAt(i, c, anchors, obstacles, placements, placed, bounds, cfg, i)
			if cost < bestCost-1e-12 {
				bestCost, bestCenter, found = cost, c, true
			}
		}
	}

	if !found { // cannot happen with Rings>0 and Angles>0, but stay total
		bestCenter = Point{a.At.X, a.At.Y - cfg.MinLeader - cfg.Radius}
	}

	return Placement{
		AnchorID: a.ID,
		Number:   a.Number,
		Balloon:  Circle{C: bestCenter, R: cfg.Radius},
		Leader:   leaderFor(a.At, bestCenter, cfg.Radius),
	}, bestCost
}

// scoreAt costs a candidate centre. skip is the index whose current placement
// should be ignored, so an anchor never collides with its own previous position.
func scoreAt(i int, c Point, anchors []Anchor, obstacles []Rect, placements []Placement,
	placed []bool, bounds Rect, cfg Config, skip int) float64 {

	a := anchors[i]
	balloon := Circle{C: c, R: cfg.Radius}
	leader := leaderFor(a.At, c, cfg.Radius)

	cost := wLeader * leader.Len()

	if !bounds.ContainsRect(balloon.Bounds()) {
		cost += wOutOfPage
	}

	for j := range placements {
		if j == skip || !placed[j] {
			continue
		}
		other := placements[j].Balloon
		if d := balloon.OverlapDepth(other); d > 0 {
			cost += wBalloonOverlap * (1 + d)
		}
		if leader.Intersects(placements[j].Leader) {
			cost += wLeaderCross
		}
		if balloon.IntersectsRect(other.Bounds()) && !balloon.Overlaps(other) {
			cost += wBalloonOverlap * 0.05 // near miss, mildly discouraged
		}
	}

	for _, o := range obstacles {
		if balloon.IntersectsRect(o) {
			cost += wObstacle
		}
		if leader.IntersectsRect(o) {
			cost += wLeaderObstacle
		}
	}

	// The balloon must not sit on the callout it points at.
	if !a.Avoid.IsZero() && balloon.IntersectsRect(a.Avoid) {
		cost += wObstacle
	}

	return cost
}

// leaderFor builds the leader line from the anchor to the balloon edge, so the
// line stops at the circle rather than running to its centre.
func leaderFor(at, center Point, radius float64) Segment {
	d := center.Sub(at)
	l := d.Len()
	if l < 1e-9 {
		return Segment{A: at, B: at}
	}
	return Segment{
		A: at,
		B: Point{X: center.X - d.X/l*radius, Y: center.Y - d.Y/l*radius},
	}
}

// annotate records why a placement is not clean, for the UI to surface.
func annotate(p *Placement, i int, obstacles []Rect,
	placements []Placement, bounds Rect) {

	p.Issues = nil
	if !bounds.ContainsRect(p.Balloon.Bounds()) {
		p.Issues = append(p.Issues, "balloon falls outside the printable area")
	}
	for j := range placements {
		if j == i {
			continue
		}
		if p.Balloon.Overlaps(placements[j].Balloon) {
			p.Issues = append(p.Issues,
				fmt.Sprintf("overlaps balloon %d", placements[j].Number))
		}
		if p.Leader.Intersects(placements[j].Leader) {
			p.Issues = append(p.Issues,
				fmt.Sprintf("leader crosses balloon %d's leader", placements[j].Number))
		}
	}
	for _, o := range obstacles {
		if p.Balloon.IntersectsRect(o) {
			p.Issues = append(p.Issues, "balloon covers drawing geometry")
			break
		}
	}
	p.Clean = len(p.Issues) == 0
}

func withDefaults(c Config) Config {
	if c.Radius <= 0 {
		c.Radius = BalloonRadius
	}
	if c.MinLeader <= 0 {
		c.MinLeader = 18
	}
	if c.MaxLeader < c.MinLeader {
		c.MaxLeader = c.MinLeader * 5
	}
	if c.Rings <= 0 {
		c.Rings = 5
	}
	if c.Angles <= 0 {
		c.Angles = 16
	}
	if c.Passes < 0 {
		c.Passes = 0
	}
	if c.Page.IsZero() {
		c.Page = Rect{0, 0, 842, 595} // A4 landscape in points
	}
	return c
}
