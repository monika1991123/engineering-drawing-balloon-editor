package export

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/dimension"
	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/layout"
	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/model"
	"github.com/xuri/excelize/v2"
)

func drawing(callouts ...string) *model.Drawing {
	d := &model.Drawing{
		Name:       "Bracket",
		PartNumber: "DWG-1042",
		Revision:   "C",
		Options:    dimension.DefaultOptions(),
		Pages:      []model.Page{{Index: 0, Width: 842, Height: 595}},
	}
	var texts []model.TextItem
	for i, c := range callouts {
		texts = append(texts, model.TextItem{
			Text: c, Page: 0,
			Box: layout.Rect{X: 100, Y: float64(100 + i*40), W: 60, H: 10},
		})
	}
	model.Build(d, texts)
	return d
}

func render(t *testing.T, d *model.Drawing) *excelize.File {
	t.Helper()
	var buf bytes.Buffer
	if err := XLSX(&buf, d, Meta{SerialNumber: "SN-001"}); err != nil {
		t.Fatalf("XLSX: %v", err)
	}
	f, err := excelize.OpenReader(&buf)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	return f
}

func TestXLSXProducesReadableWorkbook(t *testing.T) {
	d := drawing("⌀12.50 ±0.05", "25 H7", "60.00 MAX")
	f := render(t, d)
	defer f.Close()

	names := f.GetSheetList()
	if len(names) != 1 || names[0] != sheet {
		t.Fatalf("sheets = %v, want exactly [%s]", names, sheet)
	}

	rows, err := f.GetRows(sheet)
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(flatten(rows), "\n")

	for _, want := range []string{
		"First Article Inspection Report",
		"DWG-1042", // part number carried from the drawing
		"SN-001",   // serial from Meta
		"Requirement",
		"Acceptance Limits",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("workbook is missing %q", want)
		}
	}
}

// The report must echo the drawing's own notation. An inspector comparing rows
// against the print needs to recognise each one; a normalised rewrite defeats
// the purpose.
func TestRequirementKeepsDrawingNotation(t *testing.T) {
	tests := []struct {
		callout string
		want    string
	}{
		{"⌀12.50 ±0.05", "⌀12.5 ±0.05"},
		{"12.50 +0.05/-0.02", "12.5 +0.05/-0.02"},
		{"60.00 MAX", "60 MAX"},
		{"4X ⌀5.00 ±0.05", "4X ⌀5 ±0.05"},
		{"M6x1.0-6H", "M6x1.0-6H"},
	}
	for _, tc := range tests {
		t.Run(tc.callout, func(t *testing.T) {
			c := dimension.Parse(tc.callout, dimension.DefaultOptions())
			if got := c.Requirement(); got != tc.want {
				t.Errorf("Requirement() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLimitsRendering(t *testing.T) {
	opt := dimension.DefaultOptions()
	tests := []struct{ callout, want string }{
		{"⌀12.50 ±0.05", "12.45 / 12.55"},
		{"60.00 MAX", "≤ 60"},
		{"12.50 MIN", "≥ 12.5"},
		{"25 H7", "25 / 25.021"},
		{"(85.00)", ""}, // reference dimensions are not measured
	}
	for _, tc := range tests {
		t.Run(tc.callout, func(t *testing.T) {
			c := dimension.Parse(tc.callout, opt)
			if got := c.Limits(); got != tc.want {
				t.Errorf("Limits() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Reference and basic dimensions are on the drawing but are not measured, so
// they must not occupy a numbered row on the report.
//
// This asserts on the table rows rather than substring-matching the whole
// sheet: an earlier version searched for "42" and matched the part number
// DWG-1042, failing on correct output.
func TestReferenceAndBasicAreExcludedFromTheReport(t *testing.T) {
	d := drawing("12.50", "(85.00)", "[42.00]")
	f := render(t, d)
	defer f.Close()

	rows := tableRows(t, f)
	if len(rows) != 1 {
		t.Fatalf("got %d inspection rows, want 1:\n%s", len(rows), strings.Join(flatten(rows), "\n"))
	}
	if got := rows[0][colRequirement]; got != "12.5 ±0.1" {
		t.Errorf("requirement = %q, want the only measured dimension", got)
	}
}

const (
	colCharNo      = 0
	colRequirement = 3
	colLimits      = 4
)

// tableRows returns the data rows of the Form 3 table, skipping the header
// block above it and the notes below it.
func tableRows(t *testing.T, f *excelize.File) [][]string {
	t.Helper()
	all, err := f.GetRows(sheet)
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	seenHeader := false
	for _, r := range all {
		if len(r) == 0 {
			continue
		}
		if !seenHeader {
			if strings.HasPrefix(r[colCharNo], "Char.") {
				seenHeader = true
			}
			continue
		}
		// Data rows open with the balloon number; the footer does not.
		if _, err := strconv.Atoi(strings.TrimSpace(r[colCharNo])); err != nil {
			continue
		}
		for len(r) <= colLimits {
			r = append(r, "")
		}
		out = append(out, r)
	}
	return out
}

// A warning that lives only in the UI is one the person signing the report
// never sees, so it has to reach the sheet.
func TestWarningsReachTheSheet(t *testing.T) {
	d := drawing("25 H7") // fit tolerances always warn
	f := render(t, d)
	defer f.Close()

	rows, _ := f.GetRows(sheet)
	flat := strings.Join(flatten(rows), "\n")
	if !strings.Contains(flat, "Review before sign-off") {
		t.Error("warning block missing from the sheet")
	}
	if !strings.Contains(flat, "ISO 286") {
		t.Error("the ISO 286 warning did not reach the sheet")
	}
}

func TestEmptyDrawingStillProducesAValidWorkbook(t *testing.T) {
	d := drawing()
	f := render(t, d)
	defer f.Close()
	if rows, err := f.GetRows(sheet); err != nil || len(rows) == 0 {
		t.Errorf("empty drawing produced an unusable sheet: %v", err)
	}
}

func flatten(rows [][]string) []string {
	var out []string
	for _, r := range rows {
		out = append(out, strings.Join(r, "\t"))
	}
	return out
}
