// Package export writes the inspection report the whole pipeline exists to
// produce.
//
// The layout follows AS9102 Form 3 (Characteristic Accountability, Verification
// and Compatibility Evaluation), which is the sheet a customer actually asks for
// with a First Article Inspection. It is deliberately not a generic table dump:
// the column order, the header block and the empty Results column are what make
// it recognisable to the quality engineer receiving it.
package export

import (
	"fmt"
	"io"
	"time"

	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/model"
	"github.com/xuri/excelize/v2"
)

const sheet = "Form 3"

// Meta carries the header block. Anything left empty renders as a blank field
// for someone to fill in by hand, which is how these forms are used in practice.
type Meta struct {
	PartNumber   string
	PartName     string
	SerialNumber string
	FAIRNumber   string
	DrawingNo    string
	Revision     string
	PreparedBy   string
}

// XLSX writes the drawing's inspectable characteristics as an AS9102 Form 3.
func XLSX(w io.Writer, d *model.Drawing, meta Meta) error {
	f := excelize.NewFile()
	defer f.Close()

	idx, err := f.NewSheet(sheet)
	if err != nil {
		return err
	}
	f.SetActiveSheet(idx)
	if err := f.DeleteSheet("Sheet1"); err != nil {
		return err
	}

	styles, err := newStyles(f)
	if err != nil {
		return err
	}

	if meta.PartNumber == "" {
		meta.PartNumber = d.PartNumber
	}
	if meta.PartName == "" {
		meta.PartName = d.Name
	}
	if meta.Revision == "" {
		meta.Revision = d.Revision
	}

	row := 1
	row = writeHeader(f, styles, meta, row)
	row++
	row = writeTable(f, styles, d, row)
	writeFooter(f, styles, d, row+1)

	layoutColumns(f)
	// Freeze below the column headings so the table scrolls under them.
	_ = f.SetPanes(sheet, &excelize.Panes{
		Freeze: true, Split: false, XSplit: 0, YSplit: 8,
		TopLeftCell: "A9", ActivePane: "bottomLeft",
	})

	_, err = f.WriteTo(w)
	return err
}

type styleSet struct {
	title, label, value, th, td, tdCenter, tdMuted, note int
}

func newStyles(f *excelize.File) (styleSet, error) {
	var s styleSet
	var err error

	s.title, err = f.NewStyle(&excelize.Style{
		Font: &excelize.Font{Bold: true, Size: 14},
	})
	if err != nil {
		return s, err
	}
	s.label, err = f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Size: 10},
		Alignment: &excelize.Alignment{Vertical: "center"},
	})
	if err != nil {
		return s, err
	}
	s.value, err = f.NewStyle(&excelize.Style{
		Font:   &excelize.Font{Size: 10},
		Border: []excelize.Border{{Type: "bottom", Color: "999999", Style: 1}},
	})
	if err != nil {
		return s, err
	}

	border := []excelize.Border{
		{Type: "left", Color: "808080", Style: 1},
		{Type: "right", Color: "808080", Style: 1},
		{Type: "top", Color: "808080", Style: 1},
		{Type: "bottom", Color: "808080", Style: 1},
	}

	s.th, err = f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Size: 9, Color: "FFFFFF"},
		Fill:      excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{"44546A"}},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center", WrapText: true},
		Border:    border,
	})
	if err != nil {
		return s, err
	}
	s.td, err = f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Size: 10},
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: true},
		Border:    border,
	})
	if err != nil {
		return s, err
	}
	s.tdCenter, err = f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Size: 10},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border:    border,
	})
	if err != nil {
		return s, err
	}
	s.tdMuted, err = f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Size: 9, Color: "808080", Italic: true},
		Alignment: &excelize.Alignment{Vertical: "center", WrapText: true},
		Border:    border,
	})
	if err != nil {
		return s, err
	}
	s.note, err = f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Size: 9, Color: "9C6500"},
		Alignment: &excelize.Alignment{WrapText: true, Vertical: "top"},
	})
	return s, err
}

func writeHeader(f *excelize.File, s styleSet, m Meta, row int) int {
	set := func(cell string, v any, style int) {
		_ = f.SetCellValue(sheet, cell, v)
		_ = f.SetCellStyle(sheet, cell, cell, style)
	}

	set(cell("A", row), "First Article Inspection Report", s.title)
	_ = f.MergeCell(sheet, cell("A", row), cell("D", row))
	set(cell("F", row), "AS9102 Form 3", s.label)
	row++
	set(cell("A", row), "Characteristic Accountability, Verification and Compatibility Evaluation", s.label)
	_ = f.MergeCell(sheet, cell("A", row), cell("F", row))
	row += 2

	fields := [][2]string{
		{"Part Number", m.PartNumber},
		{"Part Name", m.PartName},
		{"Serial Number", m.SerialNumber},
		{"Drawing Number", m.DrawingNo},
		{"Drawing Revision", m.Revision},
		{"FAIR Number", m.FAIRNumber},
	}
	// Two columns of label/value pairs so the block stays compact.
	for i, fl := range fields {
		r := row + i/2
		labelCol, valueCol := "A", "B"
		if i%2 == 1 {
			labelCol, valueCol = "D", "E"
		}
		set(cell(labelCol, r), fl[0], s.label)
		set(cell(valueCol, r), fl[1], s.value)
		_ = f.MergeCell(sheet, cell(valueCol, r), cell(nextCol(valueCol), r))
	}
	return row + (len(fields)+1)/2
}

var columns = []struct {
	title string
	width float64
}{
	{"Char.\nNo.", 7},
	{"Reference\nLocation", 12},
	{"Characteristic\nDesignator", 13},
	{"Requirement", 26},
	{"Acceptance Limits", 18},
	{"Results", 14},
	{"Non-Conformance\nNumber", 16},
	{"Notes", 30},
}

func writeTable(f *excelize.File, s styleSet, d *model.Drawing, row int) int {
	for i, c := range columns {
		cl := colName(i)
		_ = f.SetCellValue(sheet, cell(cl, row), c.title)
		_ = f.SetCellStyle(sheet, cell(cl, row), cell(cl, row), s.th)
	}
	_ = f.SetRowHeight(sheet, row, 30)
	row++

	for _, it := range d.Inspectable() {
		c := it.Char
		notes := ""
		if len(c.Warnings) > 0 {
			notes = c.Warnings[0]
		}

		values := []any{
			it.Number,
			fmt.Sprintf("Sheet %d", it.Page+1),
			c.Designator(),
			c.Requirement(),
			c.Limits(),
			"", // Results: filled in by the inspector
			"",
			notes,
		}
		for i, v := range values {
			cl := colName(i)
			_ = f.SetCellValue(sheet, cell(cl, row), v)
			style := s.td
			switch i {
			case 0, 1, 2, 4:
				style = s.tdCenter
			case 7:
				style = s.tdMuted
			}
			_ = f.SetCellStyle(sheet, cell(cl, row), cell(cl, row), style)
		}
		row++
	}
	return row
}

func writeFooter(f *excelize.File, s styleSet, d *model.Drawing, row int) {
	stamp := fmt.Sprintf("Generated by balloon on %s — %d characteristics, %d inspectable.",
		time.Now().Format("2006-01-02 15:04"), len(d.Items), len(d.Inspectable()))
	_ = f.SetCellValue(sheet, cell("A", row), stamp)
	_ = f.SetCellStyle(sheet, cell("A", row), cell("A", row), s.note)
	_ = f.MergeCell(sheet, cell("A", row), cell("H", row))
	row += 2

	// Everything the pipeline could not fully resolve goes on the sheet itself.
	// A warning that lives only in the UI is a warning the person signing the
	// report never sees.
	warnings := d.Warnings()
	if len(warnings) == 0 {
		return
	}
	_ = f.SetCellValue(sheet, cell("A", row), "Review before sign-off:")
	_ = f.SetCellStyle(sheet, cell("A", row), cell("A", row), s.note)
	row++
	for _, w := range warnings {
		_ = f.SetCellValue(sheet, cell("A", row), "• "+w)
		_ = f.SetCellStyle(sheet, cell("A", row), cell("A", row), s.note)
		_ = f.MergeCell(sheet, cell("A", row), cell("H", row))
		row++
	}
}

func layoutColumns(f *excelize.File) {
	for i, c := range columns {
		cl := colName(i)
		_ = f.SetColWidth(sheet, cl, cl, c.width)
	}
}

func cell(col string, row int) string { return fmt.Sprintf("%s%d", col, row) }

func colName(i int) string {
	n, _ := excelize.ColumnNumberToName(i + 1)
	return n
}

func nextCol(c string) string {
	n, _ := excelize.ColumnNameToNumber(c)
	name, _ := excelize.ColumnNumberToName(n + 1)
	return name
}
