package dimension

import (
	"fmt"
	"strconv"
	"strings"
)

// Requirement renders the characteristic the way it should read on an
// inspection report: the drawing's own notation, not a normalised rewrite.
// A customer comparing the report against the drawing needs to recognise
// each row at a glance, so "12.50 +0.05/-0.02" stays in that form rather than
// being flattened to its limits.
func (c Characteristic) Requirement() string {
	var b strings.Builder

	if c.Quantity > 1 {
		fmt.Fprintf(&b, "%dX ", c.Quantity)
	}

	switch c.Kind {
	case KindGeometric:
		return c.geometricRequirement()
	case KindThread:
		if c.Thread != nil {
			b.WriteString(c.Thread.Designation)
			return b.String()
		}
	case KindSurfaceFinish:
		label := c.FinishLabel
		if label == "" {
			label = "Ra"
		}
		fmt.Fprintf(&b, "%s %s max", label, trim(c.Nominal))
		return b.String()
	case KindChamfer:
		fmt.Fprintf(&b, "%s X %s%s", trim(c.Nominal), trim(c.ChamferAngle), symDegree)
		return b.String()
	case KindDiameter:
		b.WriteString(symDiameter)
	case KindRadius:
		b.WriteString("R")
	}

	if !c.HasNominal {
		return c.Raw
	}
	b.WriteString(trim(c.Nominal))
	if c.Unit == UnitDegree {
		b.WriteString(symDegree)
	}

	switch c.TolType {
	case TolSymmetric:
		fmt.Fprintf(&b, " %s%s", symPlusMinus, trim(c.Upper))
	case TolBilateral:
		fmt.Fprintf(&b, " +%s/-%s", trim(c.Upper), trim(-c.Lower))
	case TolLimits:
		return fmt.Sprintf("%s - %s", trim(c.LowerLimit), trim(c.UpperLimit))
	case TolMax:
		b.WriteString(" MAX")
	case TolMin:
		b.WriteString(" MIN")
	case TolBasic:
		b.WriteString(" BASIC")
	case TolReference:
		return fmt.Sprintf("(%s) REF", b.String())
	case TolFit:
		if c.Fit != nil {
			fmt.Fprintf(&b, " %s", c.Fit)
		}
	case TolNone:
		// The tolerance came from the title block. Showing it explicitly saves
		// the inspector a trip back to the drawing.
		if c.Upper != 0 {
			fmt.Fprintf(&b, " %s%s", symPlusMinus, trim(c.Upper))
		}
	}

	for _, m := range c.Modifiers {
		if m == "SPHERICAL" || m == "SQUARE" {
			continue
		}
		fmt.Fprintf(&b, " %s", m)
	}
	return b.String()
}

func (c Characteristic) geometricRequirement() string {
	g := c.GDT
	if g == nil {
		return c.Raw
	}
	var b strings.Builder
	b.WriteString(strings.ReplaceAll(g.Symbol, "_", " "))
	b.WriteString(" ")
	if g.Diametral {
		b.WriteString(symDiameter)
	}
	b.WriteString(trim(g.Value))
	if g.Material != "" && g.Material != "RFS" {
		fmt.Fprintf(&b, " (%s)", g.Material)
	}
	if len(g.Datums) > 0 {
		fmt.Fprintf(&b, " | %s", strings.Join(g.Datums, " ")) //nolint:staticcheck
	}
	return b.String()
}

// Limits renders the acceptance bounds an inspector measures against, or an
// empty string when the characteristic is not measured that way.
func (c Characteristic) Limits() string {
	if !c.HasLimits {
		return ""
	}
	switch c.TolType {
	case TolMax:
		return "≤ " + trim(c.UpperLimit)
	case TolMin:
		return "≥ " + trim(c.LowerLimit)
	case TolBasic, TolReference:
		return ""
	}
	return fmt.Sprintf("%s / %s", trim(c.LowerLimit), trim(c.UpperLimit))
}

// Designator marks characteristics that carry extra inspection weight. AS9102
// leaves the vocabulary to the customer; these are the common ones.
func (c Characteristic) Designator() string {
	switch c.TolType {
	case TolBasic:
		return "Basic"
	case TolReference:
		return "Reference"
	}
	if c.Kind == KindGeometric {
		return "GD&T"
	}
	return ""
}

// trim formats a float without trailing zero noise, keeping engineering
// precision: 12.50 stays 12.5, 0.021 stays 0.021.
func trim(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if s == "-0" {
		return "0"
	}
	return s
}
