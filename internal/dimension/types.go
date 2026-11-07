// Package dimension parses engineering drawing callouts into structured
// inspection characteristics.
//
// A callout is the short piece of text sitting next to a dimension line on a
// technical drawing, e.g. "4X ⌀12.50 ±0.05" or "M6x1.0 - 6H". Humans read these
// effortlessly; software has to cope with a few decades of accumulated notation,
// three symbol encodings for the same diameter sign, and tolerances that may be
// symmetric, bilateral, expressed as limits, implied by an ISO fit class, or
// omitted entirely and inherited from the title block.
package dimension

import "fmt"

// Kind is the broad category of characteristic a callout describes. It drives
// how the characteristic is grouped on an inspection report.
type Kind string

const (
	KindLinear        Kind = "linear"
	KindDiameter      Kind = "diameter"
	KindRadius        Kind = "radius"
	KindAngular       Kind = "angular"
	KindThread        Kind = "thread"
	KindChamfer       Kind = "chamfer"
	KindSurfaceFinish Kind = "surface_finish"
	KindGeometric     Kind = "geometric" // GD&T feature control frame
	KindNote          Kind = "note"      // anything we could not classify
)

// TolType describes how the tolerance was expressed on the drawing. This is kept
// distinct from the computed limits because inspection reports are expected to
// echo the drawing's own notation back to the customer.
type TolType string

const (
	TolNone      TolType = "none"      // no tolerance on the callout; inherit from title block
	TolSymmetric TolType = "symmetric" // 12.50 ±0.05
	TolBilateral TolType = "bilateral" // 12.50 +0.05/-0.02
	TolLimits    TolType = "limits"    // 12.45-12.55
	TolMax       TolType = "max"       // 12.50 MAX
	TolMin       TolType = "min"       // 12.50 MIN
	TolBasic     TolType = "basic"     // boxed dimension, theoretically exact
	TolReference TolType = "reference" // (12.50), not inspected
	TolFit       TolType = "fit"       // 25 H7
	TolGeometric TolType = "geometric" // tolerance lives in the feature control frame
)

// Unit of the nominal value.
type Unit string

const (
	UnitMM      Unit = "mm"
	UnitInch    Unit = "in"
	UnitDegree  Unit = "deg"
	UnitMicron  Unit = "um"
	UnitUnknown Unit = ""
)

// Thread holds a parsed thread callout. Pitch is in mm for metric threads; for
// unified threads TPI carries threads-per-inch instead and Pitch is 0.
type Thread struct {
	Series      string  // "M", "UNC", "UNF", "UNEF", "NPT"
	MajorDia    float64 // in the callout's own unit
	Pitch       float64 // mm, metric only
	TPI         float64 // threads per inch, unified only
	Class       string  // "6H", "2B", ...
	Designation string  // the callout as written
}

// Fit holds an ISO 286 limits-and-fits class such as H7 or g6.
type Fit struct {
	Deviation string // fundamental deviation letter(s): "H", "g", "js"
	Grade     int    // IT grade number
	IsHole    bool   // uppercase letter means a hole (internal feature)
}

func (f Fit) String() string { return fmt.Sprintf("%s%d", f.Deviation, f.Grade) }

// FeatureControlFrame is a parsed GD&T frame, e.g. ⌖|⌀0.2Ⓜ|A|B|C.
type FeatureControlFrame struct {
	Symbol    string   // the geometric characteristic, e.g. "position", "flatness"
	SymbolRaw string   // the glyph as it appeared
	Diametral bool     // tolerance zone prefixed with ⌀
	Value     float64  // tolerance zone size
	Material  string   // "MMC", "LMC", "RFS"
	Datums    []string // primary, secondary, tertiary
}

// Characteristic is the structured result of parsing one callout.
type Characteristic struct {
	Raw      string `json:"raw"`
	Kind     Kind   `json:"kind"`
	Quantity int    `json:"quantity"` // from a "4X" prefix; 1 when absent

	Nominal    float64 `json:"nominal"`
	HasNominal bool    `json:"has_nominal"`
	Unit       Unit    `json:"unit"`

	TolType TolType `json:"tol_type"`
	// Upper and Lower are signed deviations from Nominal, in Unit.
	// For "12.50 +0.05/-0.02" they are +0.05 and -0.02.
	Upper float64 `json:"upper"`
	Lower float64 `json:"lower"`
	// UpperLimit and LowerLimit are the absolute acceptance bounds an inspector
	// measures against. Always populated when HasNominal is true and the
	// characteristic is not basic/reference.
	UpperLimit float64 `json:"upper_limit"`
	LowerLimit float64 `json:"lower_limit"`
	HasLimits  bool    `json:"has_limits"`

	// ChamferAngle is the angle of a chamfer callout such as "2 X 45", in
	// degrees. The nominal carries the leg length.
	ChamferAngle float64 `json:"chamfer_angle,omitempty"`
	// FinishLabel is the roughness parameter as written: Ra, Rz or Rq.
	FinishLabel string `json:"finish_label,omitempty"`

	Thread *Thread              `json:"thread,omitempty"`
	Fit    *Fit                 `json:"fit,omitempty"`
	GDT    *FeatureControlFrame `json:"gdt,omitempty"`

	// Modifiers captures secondary notation found on the callout that does not
	// change the measurement itself, e.g. "THRU", "DEPTH 5", "TYP".
	Modifiers []string `json:"modifiers,omitempty"`

	// Warnings records anything the parser recognised but could not fully
	// resolve. These surface in the UI rather than being silently dropped —
	// a quality report that quietly guesses is worse than one that flags.
	Warnings []string `json:"warnings,omitempty"`
}

// Inspectable reports whether this characteristic gets a row on the inspection
// report. Reference dimensions are informational and basic dimensions are
// controlled by a feature control frame elsewhere, so neither is measured
// directly.
func (c Characteristic) Inspectable() bool {
	return c.TolType != TolReference && c.TolType != TolBasic
}

func (c *Characteristic) warn(format string, args ...any) {
	c.Warnings = append(c.Warnings, fmt.Sprintf(format, args...))
}
