package layout

import "math"

const BalloonRadius = 9
const tailClearance = 2.0 // hollow tail half-width 1.5 + half its 0.7 stroke

// SafeSolve rejects conflicting positions instead of emitting least-bad ones.
// Missing placements remain Clean=false and are kept in the editor's pending
// list. Ink obstacles come from the rendered original page, including text the
// OCR did not recognize. All distances use PDF viewport points.
func SafeSolve(anchors []Anchor, ink, texts []Rect, page Rect) []Placement {
	return SafeSolveWithPositions(anchors, ink, texts, page, nil, BalloonRadius)
}

// Keep valid existing placements first, then resolve conflicts near the
// previous centers. Pending items are considered only after placed items.
func SafeSolveWithPositions(anchors []Anchor, ink, texts []Rect, page Rect, current []Placement, radius float64) []Placement {
	out := make([]Placement, len(anchors))
	var placed []Placement
	makePlacement := func(i int, c Point) Placement {
		p := Placement{AnchorID: anchors[i].ID, Number: anchors[i].Number, Balloon: Circle{C: c, R: radius}}
		p.Leader = edgeLeader(anchors[i], p.Balloon)
		return p
	}
	for i, a := range anchors {
		out[i] = Placement{AnchorID: a.ID, Number: a.Number,
			Issues: []string{"未找到无冲突位置，请调整位置、缩小气泡或移除此项"}}
		if i < len(current) && current[i].Balloon.R > 0 {
			out[i].Balloon = Circle{C: current[i].Balloon.C, R: radius}
			out[i].Leader = edgeLeader(a, out[i].Balloon)
		}
	}
	// Honor already checked placements before newly staged or conflicting ones.
	for _, clean := range []bool{true, false} {
		for i := range anchors {
			if i >= len(current) || current[i].Balloon.R <= 0 || current[i].Clean != clean {
				continue
			}
			p := makePlacement(i, current[i].Balloon.C)
			if len(CheckSafe(anchors[i], p, placed, ink, texts, page)) == 0 {
				p.Clean = true
				out[i] = p
				placed = append(placed, p)
			}
		}
	}
	for i, a := range anchors {
		if out[i].Clean {
			continue
		}
		found := false
		centers := []Point{a.At}
		if i < len(current) && current[i].Balloon.R > 0 {
			centers = append([]Point{current[i].Balloon.C}, centers...)
		}
		for originIndex, origin := range centers {
			start := 24.0
			if len(centers) > 1 && originIndex == 0 {
				start = 10
			}
			for distance := start; distance <= 174+radius && !found; distance += 10 {
				for k := 0; k < 64; k++ {
					theta := 2 * math.Pi * float64(k) / 64
					c := Point{origin.X + distance*math.Cos(theta), origin.Y + distance*math.Sin(theta)}
					p := makePlacement(i, c)
					if p.Leader.Len() < 8*radius/BalloonRadius || p.Leader.Len() > 240 {
						continue
					} // keep the pointed tail visibly distinct from its circle
					if len(CheckSafe(a, p, placed, ink, texts, page)) != 0 {
						continue
					}
					p.Clean = true
					out[i] = p
					placed = append(placed, p)
					found = true
					break
				}
			}
			if found {
				break
			}
		}
	}
	return out
}

func edgeLeader(a Anchor, c Circle) Segment {
	s := leaderFor(a.At, c.C, c.R)
	if a.Avoid.IsZero() {
		return s
	}
	// End just outside the text box on the side facing the balloon.
	d := c.C.Sub(a.At)
	box := a.Avoid.Inset(-2)
	tx, ty := math.Inf(1), math.Inf(1)
	if d.X != 0 {
		tx = box.W / 2 / math.Abs(d.X)
	}
	if d.Y != 0 {
		ty = box.H / 2 / math.Abs(d.Y)
	}
	t := math.Min(tx, ty)
	s.A = Point{a.At.X + d.X*t, a.At.Y + d.Y*t}
	return s
}

// CheckSafe is shared by automatic placement and post-drag validation. A human
// move never implicitly clears a collision or an OCR review warning.
func CheckSafe(a Anchor, p Placement, others []Placement, ink, texts []Rect, page Rect) []string {
	clearance := tailClearance * p.Balloon.R / BalloonRadius
	if p.Balloon.R <= 0 || !page.Inset(5).ContainsRect(p.Balloon.Bounds().Inset(-2)) {
		return []string{"气泡超出页面安全边界"}
	}
	if !page.Contains(p.Leader.A) || !page.Contains(p.Leader.B) {
		return []string{"指引线超出页面"}
	}
	marginCircle := p.Balloon
	marginCircle.R += 2
	for _, o := range texts {
		if marginCircle.IntersectsRect(o) {
			return []string{"气泡遮挡文字或文字安全间距不足"}
		}
		if !o.Intersects(a.Avoid) && p.Leader.IntersectsRect(o.Inset(-clearance)) {
			return []string{"指引线穿过其他文字"}
		}
	}
	for _, o := range ink {
		if marginCircle.IntersectsRect(o) {
			return []string{"气泡覆盖原图内容或安全间距不足"}
		}
		// The cells touching this callout may contain its own glyphs. Only this
		// small target region is exempt from the line/ink test.
		if hitsOutside(p.Leader, o.Inset(-clearance), a.Avoid.Inset(-(3 + clearance))) {
			return []string{"指引线穿过原图内容"}
		}
	}
	for _, other := range others {
		if other.AnchorID == p.AnchorID || !other.Clean {
			continue
		}
		if marginCircle.Overlaps(other.Balloon) {
			return []string{"气泡间距不足或重叠"}
		}
		if segmentDistance(p.Leader, other.Leader) < clearance+tailClearance*other.Balloon.R/BalloonRadius {
			return []string{"指引线交叉"}
		}
		if p.Leader.IntersectsRect(other.Balloon.Bounds().Inset(-clearance)) ||
			other.Leader.IntersectsRect(marginCircle.Bounds().Inset(-tailClearance*other.Balloon.R/BalloonRadius)) {
			return []string{"指引线穿过其他气泡"}
		}
	}
	return nil
}

func segmentDistance(a, b Segment) float64 {
	if a.Intersects(b) {
		return 0
	}
	pointDistance := func(p Point, s Segment) float64 {
		d := s.B.Sub(s.A)
		if d.X == 0 && d.Y == 0 {
			return p.Dist(s.A)
		}
		t := clamp(((p.X-s.A.X)*d.X+(p.Y-s.A.Y)*d.Y)/(d.X*d.X+d.Y*d.Y), 0, 1)
		return p.Dist(Point{s.A.X + t*d.X, s.A.Y + t*d.Y})
	}
	return math.Min(math.Min(pointDistance(a.A, b), pointDistance(a.B, b)), math.Min(pointDistance(b.A, a), pointDistance(b.B, a)))
}

// Ignore only the local target area, not an entire compressed ink span that
// happens to touch the target. Otherwise a long dimension line could disappear
// from validation merely because one end overlaps the callout.
func hitsOutside(s Segment, obstacle, exempt Rect) bool {
	if !s.IntersectsRect(obstacle) {
		return false
	}
	if !obstacle.Intersects(exempt) {
		return true
	}
	x1, y1 := math.Max(obstacle.X, exempt.X), math.Max(obstacle.Y, exempt.Y)
	x2, y2 := math.Min(obstacle.X+obstacle.W, exempt.X+exempt.W), math.Min(obstacle.Y+obstacle.H, exempt.Y+exempt.H)
	parts := []Rect{
		{obstacle.X, obstacle.Y, x1 - obstacle.X, obstacle.H},
		{x2, obstacle.Y, obstacle.X + obstacle.W - x2, obstacle.H},
		{x1, obstacle.Y, x2 - x1, y1 - obstacle.Y},
		{x1, y2, x2 - x1, obstacle.Y + obstacle.H - y2},
	}
	for _, p := range parts {
		if p.W > 0 && p.H > 0 && s.IntersectsRect(p) {
			return true
		}
	}
	return false
}
