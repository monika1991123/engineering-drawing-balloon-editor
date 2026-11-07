package layout

import "math"

// Coordinates use a top-left origin with Y increasing downward, matching the
// canvas the frontend draws on. PDF user space is bottom-left; the conversion
// happens once at the API boundary so nothing in here has to think about it.

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

func (p Point) Sub(q Point) Point    { return Point{p.X - q.X, p.Y - q.Y} }
func (p Point) Add(q Point) Point    { return Point{p.X + q.X, p.Y + q.Y} }
func (p Point) Len() float64         { return math.Hypot(p.X, p.Y) }
func (p Point) Dist(q Point) float64 { return p.Sub(q).Len() }

// Rect is an axis-aligned rectangle.
type Rect struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	W float64 `json:"w"`
	H float64 `json:"h"`
}

func (r Rect) MaxX() float64 { return r.X + r.W }
func (r Rect) MaxY() float64 { return r.Y + r.H }
func (r Rect) Center() Point { return Point{r.X + r.W/2, r.Y + r.H/2} }
func (r Rect) IsZero() bool  { return r.W == 0 && r.H == 0 }
func (r Rect) Area() float64 { return r.W * r.H }

func (r Rect) Contains(p Point) bool {
	return p.X >= r.X && p.X <= r.MaxX() && p.Y >= r.Y && p.Y <= r.MaxY()
}

func (r Rect) ContainsRect(o Rect) bool {
	return o.X >= r.X && o.Y >= r.Y && o.MaxX() <= r.MaxX() && o.MaxY() <= r.MaxY()
}

func (r Rect) Intersects(o Rect) bool {
	return r.X < o.MaxX() && o.X < r.MaxX() && r.Y < o.MaxY() && o.Y < r.MaxY()
}

// Inset shrinks the rectangle by d on every side.
func (r Rect) Inset(d float64) Rect {
	return Rect{r.X + d, r.Y + d, r.W - 2*d, r.H - 2*d}
}

// Circle is a balloon.
type Circle struct {
	C Point   `json:"c"`
	R float64 `json:"r"`
}

func (c Circle) Bounds() Rect {
	return Rect{c.C.X - c.R, c.C.Y - c.R, 2 * c.R, 2 * c.R}
}

func (c Circle) Overlaps(o Circle) bool {
	return c.C.Dist(o.C) < c.R+o.R
}

// OverlapDepth is how far two balloons interpenetrate, zero when disjoint. This
// is preferred over a boolean in scoring so the optimiser can tell a near miss
// from a direct hit.
func (c Circle) OverlapDepth(o Circle) float64 {
	return math.Max(0, c.R+o.R-c.C.Dist(o.C))
}

// IntersectsRect tests the circle against an axis-aligned box by measuring to
// the closest point on the box.
func (c Circle) IntersectsRect(r Rect) bool {
	closest := Point{
		X: clamp(c.C.X, r.X, r.MaxX()),
		Y: clamp(c.C.Y, r.Y, r.MaxY()),
	}
	return c.C.Dist(closest) < c.R
}

// Segment is a leader line.
type Segment struct {
	A Point `json:"a"`
	B Point `json:"b"`
}

func (s Segment) Len() float64 { return s.A.Dist(s.B) }

// Intersects reports whether two segments cross. Collinear touching is treated
// as a crossing because two leaders lying along each other read just as badly on
// a drawing as two that cross.
func (s Segment) Intersects(o Segment) bool {
	d1 := cross(o.A, o.B, s.A)
	d2 := cross(o.A, o.B, s.B)
	d3 := cross(s.A, s.B, o.A)
	d4 := cross(s.A, s.B, o.B)

	if ((d1 > 0 && d2 < 0) || (d1 < 0 && d2 > 0)) &&
		((d3 > 0 && d4 < 0) || (d3 < 0 && d4 > 0)) {
		return true
	}

	// Collinear overlap cases.
	switch {
	case d1 == 0 && onSegment(o.A, o.B, s.A):
		return true
	case d2 == 0 && onSegment(o.A, o.B, s.B):
		return true
	case d3 == 0 && onSegment(s.A, s.B, o.A):
		return true
	case d4 == 0 && onSegment(s.A, s.B, o.B):
		return true
	}
	return false
}

// IntersectsRect reports whether the segment enters the rectangle. Used to keep
// leader lines from being drawn straight through drawing geometry.
func (s Segment) IntersectsRect(r Rect) bool {
	if r.Contains(s.A) || r.Contains(s.B) {
		return true
	}
	tl := Point{r.X, r.Y}
	tr := Point{r.MaxX(), r.Y}
	br := Point{r.MaxX(), r.MaxY()}
	bl := Point{r.X, r.MaxY()}
	for _, edge := range []Segment{{tl, tr}, {tr, br}, {br, bl}, {bl, tl}} {
		if s.Intersects(edge) {
			return true
		}
	}
	return false
}

// cross is the 2D cross product of (b-a) and (p-a); its sign gives the turn
// direction of the three points.
func cross(a, b, p Point) float64 {
	return (b.X-a.X)*(p.Y-a.Y) - (b.Y-a.Y)*(p.X-a.X)
}

// onSegment assumes a, b, p are collinear and tests containment.
func onSegment(a, b, p Point) bool {
	return math.Min(a.X, b.X) <= p.X && p.X <= math.Max(a.X, b.X) &&
		math.Min(a.Y, b.Y) <= p.Y && p.Y <= math.Max(a.Y, b.Y)
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}
