// Package render draws a ballooned drawing page as SVG.
//
// This exists so the pipeline has a visual output that does not depend on the
// browser frontend: the solver's decisions are much easier to judge by looking
// at them than by reading coordinates, and a self-contained SVG is something a
// reviewer can open, diff or attach to an email.
package render

import (
	"fmt"
	"html"
	"io"

	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/layout"
	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/model"
)

// Style controls what gets drawn. The debug layers are off by default because
// the normal output is meant to look like a marked-up drawing, not a diagnostic.
type Style struct {
	ShowTextBoxes bool
	ShowObstacles bool
	// FlagUnclean tints balloons the solver could not place cleanly, so a
	// reviewer's eye lands on the ones that need dragging.
	FlagUnclean bool
}

// DefaultStyle marks up the drawing the way a quality engineer would expect.
func DefaultStyle() Style { return Style{FlagUnclean: true} }

// SVG writes one page of the drawing.
func SVG(w io.Writer, d *model.Drawing, pageIndex int, st Style) error {
	var page *model.Page
	for i := range d.Pages {
		if d.Pages[i].Index == pageIndex {
			page = &d.Pages[i]
			break
		}
	}
	if page == nil {
		return fmt.Errorf("render: no page with index %d", pageIndex)
	}

	bw := &errWriter{w: w}
	bw.printf(`<svg xmlns="http://www.w3.org/2000/svg" width="%g" height="%g" viewBox="0 0 %g %g" font-family="Helvetica, Arial, sans-serif">`,
		page.Width, page.Height, page.Width, page.Height)
	bw.printf(`<rect width="%g" height="%g" fill="#ffffff"/>`, page.Width, page.Height)

	if st.ShowObstacles {
		for _, o := range page.Obstacles {
			bw.printf(`<rect x="%g" y="%g" width="%g" height="%g" fill="none" stroke="#d9534f" stroke-dasharray="4 3" stroke-width="0.6"/>`,
				o.X, o.Y, o.W, o.H)
		}
	}

	// Text first, so leaders and balloons sit on top of it.
	for _, it := range d.Items {
		if it.Page != pageIndex {
			continue
		}
		b := it.Source.Box
		if st.ShowTextBoxes {
			bw.printf(`<rect x="%g" y="%g" width="%g" height="%g" fill="none" stroke="#c7d2e0" stroke-width="0.5"/>`,
				b.X, b.Y, b.W, b.H)
		}
		bw.printf(`<text x="%g" y="%g" font-size="%g" fill="#1a1a1a">%s</text>`,
			b.X, b.Y+b.H*0.78, textSize(b), html.EscapeString(it.Source.Text))
	}

	for _, it := range d.Items {
		if it.Page != pageIndex {
			continue
		}
		drawLeader(bw, it.Leader)
	}

	for _, it := range d.Items {
		if it.Page != pageIndex {
			continue
		}
		drawBalloon(bw, it, st)
	}

	bw.printf(`</svg>`)
	return bw.err
}

func drawLeader(bw *errWriter, s layout.Segment) {
	if s.Len() == 0 {
		return
	}
	bw.printf(`<line x1="%g" y1="%g" x2="%g" y2="%g" stroke="#1a1a1a" stroke-width="0.8"/>`,
		s.A.X, s.A.Y, s.B.X, s.B.Y)
	// A small filled dot marks the feature the leader points at, which is how
	// leaders terminate on a real drawing.
	bw.printf(`<circle cx="%g" cy="%g" r="1.6" fill="#1a1a1a"/>`, s.A.X, s.A.Y)
}

func drawBalloon(bw *errWriter, it model.Item, st Style) {
	stroke, fill := "#1a1a1a", "#ffffff"
	if st.FlagUnclean && !it.Clean {
		stroke, fill = "#b8860b", "#fff8e1"
	}
	c := it.Balloon
	bw.printf(`<circle cx="%g" cy="%g" r="%g" fill="%s" stroke="%s" stroke-width="1.1"/>`,
		c.C.X, c.C.Y, c.R, fill, stroke)
	bw.printf(`<text x="%g" y="%g" font-size="%g" text-anchor="middle" fill="%s">%d</text>`,
		c.C.X, c.C.Y+c.R*0.36, c.R*1.05, stroke, it.Number)
}

// textSize picks a font size that fits the extracted box, so the rendered text
// lands where it did on the original drawing.
func textSize(b layout.Rect) float64 {
	if b.H > 0 {
		return b.H * 0.82
	}
	return 8
}

// errWriter collapses the many small writes into a single error check.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintf(e.w, format, args...)
}
