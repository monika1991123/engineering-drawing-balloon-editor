// Package model joins the callout parser and the balloon solver into the
// pipeline the rest of the application drives: extracted text goes in, a
// numbered and positioned inspection sheet comes out.
package model

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/dimension"
	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/layout"
)

// TextItem is one run of text lifted from the drawing's PDF text layer, with
// the box it occupies on the page. This is the shape PDF.js hands back, and it
// is deliberately the only thing the pipeline needs — nothing here knows or
// cares how the text was extracted.
type TextItem struct {
	Text         string      `json:"text"`
	Box          layout.Rect `json:"box"`
	Page         int         `json:"page"`
	OCR          bool        `json:"ocr,omitempty"`
	Confidence   float64     `json:"confidence,omitempty"`
	RawText      string      `json:"raw_text,omitempty"`
	Rotation     int         `json:"rotation,omitempty"`
	Alternatives []string    `json:"alternatives,omitempty"`
}

// Item is one ballooned characteristic on the inspection sheet.
type Item struct {
	ID             string        `json:"id"`
	Number         int           `json:"number"`
	Page           int           `json:"page"`
	Include        bool          `json:"include"`
	Manual         bool          `json:"manual,omitempty"`
	Anchor         *layout.Point `json:"anchor,omitempty"`
	Pending        bool          `json:"pending,omitempty"`
	ReviewRequired bool          `json:"review_required,omitempty"`

	Source TextItem                 `json:"source"`
	Char   dimension.Characteristic `json:"characteristic"`

	// Requirement, LimitsText and Designator are derived from Char. They are
	// carried on the item so the browser can render a table without
	// reimplementing the formatting rules, and so the values it posts back are
	// the ones a human actually saw.
	Requirement string `json:"requirement"`
	LimitsText  string `json:"limits"`
	Designator  string `json:"designator"`

	Balloon layout.Circle  `json:"balloon"`
	Leader  layout.Segment `json:"leader"`
	Clean   bool           `json:"clean"`
	Issues  []string       `json:"issues,omitempty"`
}

// Page is one sheet of the drawing.
type Page struct {
	Index  int     `json:"index"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
	// Obstacles are regions balloons should keep off: drawing geometry, the
	// title block, the revision table.
	Obstacles     []layout.Rect `json:"obstacles,omitempty"`
	TextObstacles []layout.Rect `json:"text_obstacles,omitempty"`
	Strict        bool          `json:"strict,omitempty"`
}

// Drawing is the unit of work: a part, a revision, and the sheets that define it.
type Drawing struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	PartNumber    string  `json:"part_number"`
	Revision      string  `json:"revision"`
	Pages         []Page  `json:"pages"`
	Items         []Item  `json:"items"`
	BalloonRadius float64 `json:"balloon_radius,omitempty"`

	// Options is the title block context callouts are parsed against.
	Options dimension.Options `json:"-"`
}

// Build runs the full pipeline: parse every text item, drop the ones that are
// not characteristics, number what remains in drawing-reading order, then solve
// balloon positions page by page.
func Build(d *Drawing, texts []TextItem) {
	d.Items = nil
	opt := d.Options
	if opt.DefaultTolerances == nil {
		opt = dimension.DefaultOptions()
	}

	byPage := map[int][]TextItem{}
	for _, t := range texts {
		byPage[t.Page] = append(byPage[t.Page], t)
	}

	next := 1
	for _, page := range d.Pages {
		items := byPage[page.Index]

		// Balloon numbers follow the order an inspector reads the sheet:
		// top to bottom, then left to right. Sorting by raw Y alone makes
		// numbering jitter when two callouts sit a hair apart vertically, so
		// rows are banded first.
		sort.SliceStable(items, func(a, b int) bool {
			rowA := int(items[a].Box.Y / rowBand)
			rowB := int(items[b].Box.Y / rowBand)
			if rowA != rowB {
				return rowA < rowB
			}
			return items[a].Box.X < items[b].Box.X
		})

		var anchors []layout.Anchor
		var pageItems []Item

		for _, t := range items {
			parseOpt := opt
			if t.OCR {
				parseOpt.DefaultTolerances = map[int]float64{}
				parseOpt.DefaultAngular = 0
			}
			c := dimension.Parse(t.Text, parseOpt)
			if !isCharacteristic(c) {
				continue
			}
			if t.OCR && !ocrCandidate(c) {
				continue
			}
			if t.OCR {
				c.Warnings = append(c.Warnings, "OCR 识别待人工核对；默认公差未从图纸验证")
			}
			id := fmt.Sprintf("p%d-%d", page.Index, next)
			pageItems = append(pageItems, Item{
				ID:             id,
				Number:         next,
				Page:           page.Index,
				Include:        c.Inspectable() && !t.OCR,
				ReviewRequired: t.OCR,
				Source:         t,
				Char:           c,
				Requirement:    c.Requirement(),
				LimitsText:     c.Limits(),
				Designator:     c.Designator(),
			})
			if t.OCR {
				pageItems[len(pageItems)-1].Requirement = t.Text
			}
			anchors = append(anchors, layout.Anchor{
				ID:     id,
				Number: next,
				At:     t.Box.Center(),
				Avoid:  t.Box,
			})
			next++
		}

		cfg := layout.DefaultConfig(layout.Rect{W: page.Width, H: page.Height})
		if d.BalloonRadius > 0 {
			cfg.Radius = d.BalloonRadius
		}
		placements := layout.Solve(anchors, page.Obstacles, cfg)
		for i := range pageItems {
			p := placements[i]
			pageItems[i].Balloon = p.Balloon
			pageItems[i].Leader = p.Leader
			pageItems[i].Clean = p.Clean
			pageItems[i].Issues = p.Issues
		}
		d.Items = append(d.Items, pageItems...)
	}
}

// Restrict OCR's permissive parser: an isolated "6Q" or "9+" must not become
// an asserted dimension. Unsupported strings remain visible in OCR boxes.
var ocrNumber = regexp.MustCompile(`^\(?(?:(?:\d+)\s*[-xX×]\s*)?(?:[⌀ØΦφϕø∅Rr]|SR)?\s*\d+(?:[.,]\d+)?(?:\s*(?:±|\+|-)\s*\d+(?:[.,]\d+)?(?:\s*/?\s*-\s*\d+(?:[.,]\d+)?)?)?(?:\s*(?:[°º˚]|[Hh]\d+|MAX|MIN|REF|THRU))?\)?$`)

func ocrCandidate(c dimension.Characteristic) bool {
	if len(c.Raw) > 100 || (c.HasNominal && (c.Nominal < 0 || c.Nominal > 100000)) {
		return false
	}
	switch c.Kind {
	case dimension.KindSurfaceFinish, dimension.KindThread, dimension.KindGeometric:
		return true
	}
	return ocrNumber.MatchString(strings.TrimSpace(c.Raw))
}

// rowBand is how far apart two callouts must be vertically before they count as
// different rows for numbering, in PDF points. Roughly one line of drawing text.
const rowBand = 14

// isCharacteristic decides whether a scrap of text earns a balloon.
//
// A drawing's text layer is mostly not dimensions: it is the title block, notes,
// the revision table, the company name. Filtering on "does it contain a number"
// alone is not enough — "SEE NOTE 3" and "SHEET 1 OF 2" both do, and ballooning
// them puts junk rows on the inspection report.
func isCharacteristic(c dimension.Characteristic) bool {
	// Symbol-only arrows, datum letters and section labels are not measured
	// characteristics, even if a glyph resembles a GD&T font substitution.
	if strings.IndexAny(c.Raw, "0123456789") < 0 {
		return false
	}
	// Threads, roughness, chamfers and feature control frames are already
	// positively identified by their own grammar, so prose cannot masquerade
	// as one and they need no further filtering.
	switch c.Kind {
	case dimension.KindGeometric, dimension.KindThread,
		dimension.KindSurfaceFinish, dimension.KindChamfer:
		return true
	}
	if !c.HasNominal || c.Kind == dimension.KindNote {
		return false
	}
	return !looksLikeProse(c.Raw)
}

// drawingVocabulary is the set of words that legitimately appear inside a
// dimension callout. Anything else with letters in it is prose.
var drawingVocabulary = map[string]bool{
	"MAX": true, "MIN": true, "THRU": true, "ALL": true, "TYP": true,
	"REF": true, "BASIC": true, "BSC": true, "DIA": true, "DEEP": true,
	"DP": true, "DEPTH": true, "RA": true, "RZ": true, "RQ": true,
	"UNC": true, "UNF": true, "UNEF": true, "UN": true, "NPT": true,
	"MM": true, "IN": true, "INCH": true, "DEG": true, "PL": true,
	"PLCS": true, "PLACES": true, "EQ": true, "SP": true, "CBORE": true,
	"CSK": true, "SR": true, "TP": true,
}

var wordRe = regexp.MustCompile(`[A-Za-z]{2,}`)

// looksLikeProse reports whether a callout contains a word that has no business
// being in a dimension. Single letters are always allowed because they are
// datum references and fit-class letters.
func looksLikeProse(raw string) bool {
	for _, w := range wordRe.FindAllString(raw, -1) {
		if !drawingVocabulary[strings.ToUpper(w)] {
			return true
		}
	}
	return false
}

// Renumber reassigns sequential numbers after items are added, removed or
// reordered, preserving the current order of d.Items.
func (d *Drawing) Renumber() {
	n := 1
	for i := range d.Items {
		d.Items[i].Number = n
		n++
	}
}

// Inspectable returns the items that get a row on the inspection report.
func (d *Drawing) Inspectable() []Item {
	var out []Item
	for _, it := range d.Items {
		if it.Include && !it.Pending && !it.ReviewRequired {
			out = append(out, it)
		}
	}
	return out
}

// Warnings collects everything the pipeline could not fully resolve, so the UI
// can show one honest list instead of burying problems per-item.
func (d *Drawing) Warnings() []string {
	var out []string
	for _, it := range d.Items {
		for _, w := range it.Char.Warnings {
			out = append(out, fmt.Sprintf("balloon %d: %s", it.Number, w))
		}
		for _, is := range it.Issues {
			out = append(out, fmt.Sprintf("balloon %d: %s", it.Number, is))
		}
	}
	return out
}
