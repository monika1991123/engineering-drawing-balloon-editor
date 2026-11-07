package model

import (
	"testing"

	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/dimension"
	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/layout"
)

func page() Page { return Page{Index: 0, Width: 842, Height: 595} }

func TestNoNumericValueNeverGeneratesAutomaticBalloon(t *testing.T) {
	for _, ocr := range []bool{false, true} {
		for _, text := range []string{"A-A", "A", "j", "↗", "⏥", "→", "Ra", "Rz", "⌖ A"} {
			source := at(text, 100, 100)
			source.OCR = ocr
			if d := build(source); len(d.Items) != 0 {
				t.Errorf("symbol-only %q OCR=%v generated %+v", text, ocr, d.Items)
			}
		}
	}
}

func TestOCREngineeringCandidatesKeepSymbols(t *testing.T) {
	for _, text := range []string{"(Ø9,8)", "(Ø7.8)", "4-R3", "4-Ø4", "45°"} {
		source := at(text, 100, 100)
		source.OCR = true
		source.Confidence = .98
		d := build(source)
		if len(d.Items) != 1 {
			t.Errorf("engineering candidate %q dropped", text)
			continue
		}
		if !d.Items[0].ReviewRequired || d.Items[0].Include {
			t.Errorf("OCR %q was silently approved", text)
		}
	}
}

func at(text string, x, y float64) TextItem {
	return TextItem{
		Text: text,
		Page: 0,
		Box:  layout.Rect{X: x, Y: y, W: float64(len([]rune(text))) * 5.2, H: 10},
	}
}

func build(texts ...TextItem) *Drawing {
	d := &Drawing{Options: dimension.DefaultOptions(), Pages: []Page{page()}}
	Build(d, texts)
	return d
}

// The drawing's text layer is mostly not dimensions. Anything containing a
// number will pass a naive filter, which is how "SEE NOTE 3" ends up on an
// inspection report as a 3 mm dimension.
func TestProseIsNotBallooned(t *testing.T) {
	prose := []string{
		"SEE NOTE 3",
		"SHEET 1 OF 2",
		"SCALE 1:1",
		"DO NOT SCALE DRAWING",
		"REVISION C",
		"MATERIAL: 6061-T6",
	}
	for _, s := range prose {
		t.Run(s, func(t *testing.T) {
			if d := build(at(s, 100, 100)); len(d.Items) != 0 {
				t.Errorf("prose was ballooned as %+v", d.Items[0].Char)
			}
		})
	}
}

// The vocabulary exception has to keep real callouts that contain words.
func TestCalloutsWithWordsSurvive(t *testing.T) {
	callouts := []string{
		"60.00 MAX",
		"12.50 MIN",
		"⌀12.5 THRU",
		"25.0 TYP",
		"M6x1.0-6H",
		"1/4-20 UNC-2B",
		"Ra 1.6",
		"25 H7",
	}
	for _, s := range callouts {
		t.Run(s, func(t *testing.T) {
			if d := build(at(s, 100, 100)); len(d.Items) != 1 {
				t.Errorf("callout was dropped, got %d items", len(d.Items))
			}
		})
	}
}

// Balloon numbers follow the order an inspector reads the sheet: top to bottom,
// then left to right within a row.
func TestNumberingFollowsReadingOrder(t *testing.T) {
	d := build(
		at("30.0", 400, 300), // row 3
		at("10.0", 300, 100), // row 1, right
		at("20.0", 100, 100), // row 1, left
		at("40.0", 200, 200), // row 2
	)
	if len(d.Items) != 4 {
		t.Fatalf("got %d items", len(d.Items))
	}
	want := []string{"20.0", "10.0", "40.0", "30.0"}
	for i, w := range want {
		if d.Items[i].Source.Text != w {
			t.Errorf("position %d is %q, want %q", i+1, d.Items[i].Source.Text, w)
		}
		if d.Items[i].Number != i+1 {
			t.Errorf("item %d numbered %d", i, d.Items[i].Number)
		}
	}
}

// Callouts a hair apart vertically must not scramble the numbering, which is why
// rows are banded rather than sorted on raw Y.
func TestNumberingIsStableAcrossMinorYJitter(t *testing.T) {
	d := build(
		at("10.0", 100, 100),
		at("20.0", 200, 102), // 2pt lower, still the same row
		at("30.0", 300, 99),  // 1pt higher, still the same row
	)
	want := []string{"10.0", "20.0", "30.0"}
	for i, w := range want {
		if d.Items[i].Source.Text != w {
			t.Errorf("position %d is %q, want %q", i+1, d.Items[i].Source.Text, w)
		}
	}
}

// Reference and basic dimensions get a balloon but no inspection row.
func TestReferenceAndBasicAreBallooedButNotInspected(t *testing.T) {
	d := build(
		at("(85.00)", 100, 100),
		at("[42.00]", 200, 100),
		at("12.50", 300, 100),
	)
	if len(d.Items) != 3 {
		t.Fatalf("got %d items, want 3", len(d.Items))
	}
	if got := len(d.Inspectable()); got != 1 {
		t.Errorf("got %d inspectable items, want 1", got)
	}
}

func TestBuildIsIdempotent(t *testing.T) {
	texts := []TextItem{at("12.50", 100, 100), at("⌀8.0", 200, 100)}
	d := &Drawing{Options: dimension.DefaultOptions(), Pages: []Page{page()}}
	Build(d, texts)
	first := d.Items
	Build(d, texts)
	if len(d.Items) != len(first) {
		t.Fatalf("rebuild changed item count: %d then %d", len(first), len(d.Items))
	}
	for i := range first {
		if d.Items[i].Balloon != first[i].Balloon || d.Items[i].Number != first[i].Number {
			t.Errorf("rebuild moved item %d", i)
		}
	}
}
