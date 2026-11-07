// Package demo builds a synthetic drawing that exercises most of the parser and
// crowds enough callouts together to make the placement solver work for its
// answers.
//
// It exists so both `balloon demo` and the browser's "load the demo part" run
// the same fixture, and so a fresh clone produces something to look at without
// anyone having to find a drawing first.
package demo

import (
	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/dimension"
	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/layout"
	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/model"
)

// Drawing returns the fixture and the text items that populate it.
func Drawing() (*model.Drawing, []model.TextItem) {
	const w, h = 842, 595

	d := &model.Drawing{
		ID:         "demo",
		Name:       "Bracket, Mounting",
		PartNumber: "DWG-1042",
		Revision:   "C",
		Options:    dimension.DefaultOptions(),
		Pages: []model.Page{{
			Index: 0, Width: w, Height: h,
			Obstacles: []layout.Rect{
				{X: 250, Y: 150, W: 330, H: 240}, // the part view itself
				{X: 560, Y: 470, W: 270, H: 115}, // title block
			},
		}},
	}

	callouts := []struct {
		text string
		x, y float64
	}{
		{"4X ⌀12.50 ±0.05", 120, 120},
		{"⌀25.00 +0.05/-0.02", 120, 180},
		{"R8.0", 120, 240},
		{"60.00 MAX", 120, 300},
		{"25 H7", 120, 360},
		{"M6x1.0-6H", 120, 420},
		{"2 X 45°", 640, 120},
		{"Ra 1.6", 640, 180},
		{"120.45-120.55", 620, 240},
		{"⌖|⌀0.2Ⓜ|A|B|C", 620, 300},
		{"⊥ 0.05 A", 640, 360},
		{"(85.00)", 640, 420},
		{"[42.00]", 300, 430},
		{"15.000", 380, 430},
		{"90°±0.5°", 460, 430},
		{"SEE NOTE 3", 300, 100},
	}

	texts := make([]model.TextItem, 0, len(callouts))
	for _, c := range callouts {
		texts = append(texts, model.TextItem{
			Text: c.text,
			Page: 0,
			Box: layout.Rect{
				X: c.x, Y: c.y,
				W: float64(len([]rune(c.text))) * 5.2,
				H: 10,
			},
		})
	}
	return d, texts
}
