package api

import (
	"math"
	"net/http"

	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/layout"
	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/model"
)

func itemAnchor(it model.Item) layout.Anchor {
	at := it.Source.Box.Center()
	avoid := it.Source.Box
	if it.Anchor != nil {
		at = *it.Anchor
		avoid = layout.Rect{X: at.X, Y: at.Y}
	}
	return layout.Anchor{ID: it.ID, Number: it.Number, At: at, Avoid: avoid}
}

func safeLayout(d *model.Drawing, page model.Page, preserve bool) {
	var anchors []layout.Anchor
	var indices []int
	var current []layout.Placement
	for i, it := range d.Items {
		if it.Page == page.Index {
			anchors = append(anchors, itemAnchor(it))
			indices = append(indices, i)
			p := layout.Placement{}
			if preserve && !it.Pending {
				p = layout.Placement{Balloon: it.Balloon, Clean: it.Clean}
			}
			current = append(current, p)
		}
	}
	radius := d.BalloonRadius
	if radius == 0 {
		radius = layout.BalloonRadius
	}
	positions := layout.SafeSolveWithPositions(anchors, page.Obstacles, page.TextObstacles, layout.Rect{W: page.Width, H: page.Height}, current, radius)
	for n, p := range positions {
		it := &d.Items[indices[n]]
		it.Balloon, it.Leader, it.Clean, it.Issues = p.Balloon, p.Leader, p.Clean, p.Issues
		it.Pending = !p.Clean && p.Balloon.R == 0
	}
}

func validSafeDrawing(w http.ResponseWriter, d *model.Drawing) bool {
	if math.IsNaN(d.BalloonRadius) || math.IsInf(d.BalloonRadius, 0) || (d.BalloonRadius != 0 && (d.BalloonRadius < 4 || d.BalloonRadius > 24)) {
		fail(w, 400, "气泡半径须在 4–24 pt 范围内")
		return false
	}
	if len(d.Pages) > 100 || len(d.Items) > 500 {
		fail(w, 400, "页数或标注数超过限制（100 页、500 个标注）")
		return false
	}
	finite := func(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && math.Abs(v) < 1e7 }
	validRect := func(r layout.Rect) bool {
		return finite(r.X) && finite(r.Y) && finite(r.W) && finite(r.H) && r.W >= 0 && r.H >= 0
	}
	for _, p := range d.Pages {
		if !finite(p.Width) || !finite(p.Height) || p.Width <= 0 || p.Height <= 0 || len(p.Obstacles) > 20000 || len(p.TextObstacles) > 2000 {
			fail(w, 400, "页面尺寸或障碍物数量无效")
			return false
		}
		for _, boxes := range [][]layout.Rect{p.Obstacles, p.TextObstacles} {
			for _, b := range boxes {
				if !validRect(b) {
					fail(w, 400, "障碍物坐标无效")
					return false
				}
			}
		}
	}
	ids := map[string]bool{}
	for _, it := range d.Items {
		if it.ID == "" || ids[it.ID] || !validRect(it.Source.Box) || !finite(it.Balloon.C.X) || !finite(it.Balloon.C.Y) || !finite(it.Balloon.R) {
			fail(w, 400, "标注 ID 或坐标无效")
			return false
		}
		if it.Anchor != nil && (!finite(it.Anchor.X) || !finite(it.Anchor.Y)) {
			fail(w, 400, "指向点无效")
			return false
		}
		ids[it.ID] = true
	}
	return true
}

func validTexts(w http.ResponseWriter, texts []model.TextItem) bool {
	for _, t := range texts {
		if len(t.Text) > 4096 || t.Box.W < 0 || t.Box.H < 0 {
			fail(w, 400, "文字或坐标无效")
			return false
		}
		for _, value := range []float64{t.Box.X, t.Box.Y, t.Box.W, t.Box.H, t.Confidence} {
			if math.IsNaN(value) || math.IsInf(value, 0) || math.Abs(value) > 1e7 {
				fail(w, 400, "文字坐标无效")
				return false
			}
		}
	}
	return true
}

func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	var req layoutRequest
	if !decode(w, r, &req) || !validSafeDrawing(w, &req.Drawing) {
		return
	}
	for _, page := range req.Drawing.Pages {
		var all []layout.Placement
		for _, it := range req.Drawing.Items {
			if it.Page == page.Index && !it.Pending {
				all = append(all, layout.Placement{AnchorID: it.ID, Balloon: it.Balloon, Leader: it.Leader, Clean: true})
			}
		}
		for i := range req.Drawing.Items {
			it := &req.Drawing.Items[i]
			if it.Page != page.Index || it.Pending {
				continue
			}
			it.Issues = layout.CheckSafe(itemAnchor(*it), layout.Placement{AnchorID: it.ID, Balloon: it.Balloon, Leader: it.Leader},
				all, page.Obstacles, page.TextObstacles, layout.Rect{W: page.Width, H: page.Height})
			it.Clean = len(it.Issues) == 0
		}
	}
	writeJSON(w, http.StatusOK, req.Drawing)
}
