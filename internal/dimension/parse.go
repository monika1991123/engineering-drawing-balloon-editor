package dimension

import (
	"regexp"
	"strconv"
	"strings"
)

// Options carries the drawing-level context a callout is read against. The most
// important piece is the title block's default tolerance table: a dimension
// written as plain "12.50" is not untoleranced, it inherits ±0.01 (or whatever
// the block says for two-place decimals). Parsing without this context produces
// characteristics an inspector cannot actually measure against.
type Options struct {
	// DefaultTolerances maps decimal-place count to a symmetric tolerance, in
	// the drawing's linear unit. Mirrors "UNLESS OTHERWISE SPECIFIED: .X ±0.1".
	DefaultTolerances map[int]float64
	// DefaultAngular is the symmetric tolerance applied to angles with no
	// explicit tolerance, in degrees.
	DefaultAngular float64
	// Unit is assumed when the callout carries no unit of its own.
	Unit Unit
}

// DefaultOptions returns a metric title block with tolerances typical of a
// general-machining drawing.
func DefaultOptions() Options {
	return Options{
		DefaultTolerances: map[int]float64{0: 0.5, 1: 0.2, 2: 0.1, 3: 0.05},
		DefaultAngular:    0.5,
		Unit:              UnitMM,
	}
}

var (
	num = `[-+]?(?:\d+\.?\d*|\.\d+)`

	qtyRe        = regexp.MustCompile(`^(\d+)\s*[xX×]\s*`)
	qtyDashRe    = regexp.MustCompile(`^(\d+)\s*-\s*(SR|R|S?⌀)`)
	numRe        = regexp.MustCompile(num)
	symmetricRe  = regexp.MustCompile(`±\s*(` + num + `)`)
	bilateralRe  = regexp.MustCompile(`\+\s*(` + num + `)\s*/?\s*-\s*(` + num + `)`)
	bilateralAlt = regexp.MustCompile(`-\s*(` + num + `)\s*/?\s*\+\s*(` + num + `)`)
	limitsRe     = regexp.MustCompile(`^(` + num + `)\s*-\s*(` + num + `)$`)
	maxRe        = regexp.MustCompile(`(?i)\bMAX\b`)
	minRe        = regexp.MustCompile(`(?i)\bMIN\b`)
	basicRe      = regexp.MustCompile(`(?i)\b(BASIC|BSC)\b`)
	chamferRe    = regexp.MustCompile(`^(` + num + `)\s*[xX×]\s*(` + num + `)\s*°`)
	metricThread = regexp.MustCompile(`(?i)^M\s*(` + num + `)\s*[x×]\s*(` + num + `)(?:\s*-\s*(\w+))?`)
	unifiedThrd  = regexp.MustCompile(`(?i)^(#\d+|\d+/\d+|` + num + `)\s*-\s*(\d+)\s*(UNC|UNF|UNEF|UN)(?:\s*-\s*(\w+))?`)
	roughnessRe  = regexp.MustCompile(`(?i)\b(Ra|Rz|Rq)\s*(` + num + `)`)
	modifierRe   = regexp.MustCompile(`(?i)\b(THRU(?:\s+ALL)?|TYP|REF|EACH SIDE|BOTH SIDES|NEAR SIDE|FAR SIDE)\b`)
	depthRe      = regexp.MustCompile(`(?i)(?:↧|\bDEPTH\b|\bDP\b)\s*(` + num + `)`)
)

// Parse reads a single drawing callout into a structured characteristic.
//
// Parse never returns an error. A callout it cannot classify comes back as
// KindNote with the raw text preserved, because on a real drawing an
// unrecognised annotation still has to appear on the inspection report for a
// human to deal with — dropping it silently is the one unacceptable outcome.
func Parse(raw string, opt Options) Characteristic {
	c := Characteristic{Raw: raw, Quantity: 1, Unit: opt.Unit}
	s := normalize(raw)
	// The editable requirement formatter includes an explicit REF suffix.
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ") REF") {
		s = strings.TrimSpace(strings.TrimSuffix(s, "REF"))
	}
	if s == "" {
		c.Kind = KindNote
		return c
	}

	// A callout wrapped entirely in parentheses is a reference dimension:
	// shown for convenience, never inspected.
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		c.TolType = TolReference
		s = strings.TrimSpace(s[1 : len(s)-1])
	}

	// Square brackets are how a boxed (basic) dimension usually survives text
	// extraction, since the box itself is drawn as graphics rather than text.
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		c.TolType = TolBasic
		s = strings.TrimSpace(s[1 : len(s)-1])
	} else if basicRe.MatchString(s) {
		c.TolType = TolBasic
		s = basicRe.ReplaceAllString(s, "")
	}

	// A feature control frame is self-contained: its tolerance lives in the
	// frame, not in any surrounding dimension, so it short-circuits everything.
	if fcf, ok := parseFCF(s); ok {
		c.Kind = KindGeometric
		c.TolType = TolGeometric
		c.GDT = fcf
		c.Unit = opt.Unit
		return c
	}

	s = qtyDashRe.ReplaceAllString(s, `${1}X ${2}`)
	if m := qtyRe.FindStringSubmatch(s); m != nil && !chamferRe.MatchString(s) {
		if q, err := strconv.Atoi(m[1]); err == nil && q > 0 {
			c.Quantity = q
		}
		s = strings.TrimSpace(s[len(m[0]):])
	}

	// Pull out trailing modifiers before number parsing so words like "THRU"
	// cannot be mistaken for part of a tolerance expression.
	for _, m := range modifierRe.FindAllString(s, -1) {
		c.Modifiers = append(c.Modifiers, strings.ToUpper(strings.TrimSpace(m)))
	}
	s = strings.TrimSpace(modifierRe.ReplaceAllString(s, " "))

	if m := depthRe.FindStringSubmatch(s); m != nil {
		c.Modifiers = append(c.Modifiers, "DEPTH "+m[1])
		s = strings.TrimSpace(depthRe.ReplaceAllString(s, " "))
	}

	switch {
	case parseThread(&c, s):
		return finish(&c, opt)
	case parseRoughness(&c, s):
		return finish(&c, opt)
	case parseChamfer(&c, s, opt):
		return finish(&c, opt)
	}

	parseDimensional(&c, s, opt)
	return finish(&c, opt)
}

// parseDimensional handles the common case: an optional symbol prefix, a
// nominal value, and some form of tolerance.
func parseDimensional(c *Characteristic, s string, opt Options) {
	// Symbol prefix decides the kind before we touch the number.
	switch {
	case strings.HasPrefix(s, "S"+symDiameter):
		c.Kind = KindDiameter
		c.Modifiers = append(c.Modifiers, "SPHERICAL")
		s = strings.TrimSpace(s[len("S"+symDiameter):])
	case strings.HasPrefix(s, symDiameter):
		c.Kind = KindDiameter
		s = strings.TrimSpace(s[len(symDiameter):])
	case strings.HasPrefix(s, "SR"):
		c.Kind = KindRadius
		c.Modifiers = append(c.Modifiers, "SPHERICAL")
		s = strings.TrimSpace(s[2:])
	case regexp.MustCompile(`^R\s*\d`).MatchString(s):
		c.Kind = KindRadius
		s = strings.TrimSpace(s[1:])
	case strings.HasPrefix(s, symSquare):
		c.Kind = KindLinear
		c.Modifiers = append(c.Modifiers, "SQUARE")
		s = strings.TrimSpace(s[len(symSquare):])
	case strings.Contains(s, symDegree):
		c.Kind = KindAngular
	default:
		c.Kind = KindLinear
	}

	if strings.Contains(s, symDegree) {
		c.Unit = UnitDegree
		if c.Kind == KindLinear {
			c.Kind = KindAngular
		}
	} else if strings.Contains(s, "\"") || regexp.MustCompile(`(?i)\b(IN|INCH)\b`).MatchString(s) {
		c.Unit = UnitInch
	} else if regexp.MustCompile(`(?i)\bmm\b`).MatchString(s) {
		c.Unit = UnitMM
	}

	// Limit dimensions ("12.45-12.55") have to be tested before the plain
	// nominal path, because the hyphen would otherwise read as a minus sign.
	stripped := strings.TrimSpace(strings.NewReplacer(symDegree, "", "\"", "", "mm", "").Replace(s))
	if m := limitsRe.FindStringSubmatch(stripped); m != nil {
		lo, err1 := strconv.ParseFloat(m[1], 64)
		hi, err2 := strconv.ParseFloat(m[2], 64)
		if err1 == nil && err2 == nil && hi > lo {
			c.TolType = TolLimits
			c.LowerLimit, c.UpperLimit, c.HasLimits = lo, hi, true
			c.Nominal, c.HasNominal = (lo+hi)/2, true
			c.Lower, c.Upper = lo-c.Nominal, hi-c.Nominal
			return
		}
	}

	loc := numRe.FindStringIndex(s)
	if loc == nil {
		c.Kind = KindNote
		return
	}
	literal := s[loc[0]:loc[1]]
	v, err := strconv.ParseFloat(literal, 64)
	if err != nil {
		c.Kind = KindNote
		return
	}
	c.Nominal, c.HasNominal = v, true
	rest := strings.TrimSpace(s[loc[1]:])

	if c.TolType == TolBasic || c.TolType == TolReference {
		return // theoretically exact or informational; no tolerance to resolve
	}

	parseTolerance(c, rest, literal, opt)
}

// parseTolerance resolves the tolerance expression trailing a nominal value.
// literal is the nominal exactly as written, which is what decides the default
// tolerance band when no explicit tolerance is present.
func parseTolerance(c *Characteristic, rest, literal string, opt Options) {
	switch {
	case symmetricRe.MatchString(rest):
		m := symmetricRe.FindStringSubmatch(rest)
		t, _ := strconv.ParseFloat(m[1], 64)
		t = absf(t)
		c.TolType, c.Upper, c.Lower = TolSymmetric, t, -t

	case bilateralRe.MatchString(rest):
		m := bilateralRe.FindStringSubmatch(rest)
		up, _ := strconv.ParseFloat(m[1], 64)
		lo, _ := strconv.ParseFloat(m[2], 64)
		c.TolType, c.Upper, c.Lower = TolBilateral, absf(up), -absf(lo)

	case bilateralAlt.MatchString(rest):
		m := bilateralAlt.FindStringSubmatch(rest)
		lo, _ := strconv.ParseFloat(m[1], 64)
		up, _ := strconv.ParseFloat(m[2], 64)
		c.TolType, c.Upper, c.Lower = TolBilateral, absf(up), -absf(lo)

	case maxRe.MatchString(rest):
		c.TolType = TolMax
		c.LowerLimit, c.UpperLimit, c.HasLimits = 0, c.Nominal, true
		return

	case minRe.MatchString(rest):
		c.TolType = TolMin
		c.LowerLimit, c.HasLimits = c.Nominal, true
		return

	default:
		// An ISO fit class is the last structured possibility.
		if fit, ok := parseFitClass(strings.TrimSpace(rest)); ok {
			up, lo, resolved := fit.resolve(c.Nominal)
			c.Fit, c.TolType = &fit, TolFit
			if !resolved {
				c.warn("fit class %s could not be resolved at nominal %g", fit, c.Nominal)
				return
			}
			c.Upper, c.Lower = up, lo
			c.warn("tolerance for %s computed from ISO 286 formulas; verify against the standard before acceptance", fit)
			break
		}
		// Nothing explicit: inherit the title block default for this precision.
		c.TolType = TolNone
		t, ok := defaultTolerance(literal, c, opt)
		if !ok {
			c.warn("no explicit tolerance and no title-block default for this precision")
			return
		}
		c.Upper, c.Lower = t, -t
	}

	c.LowerLimit = c.Nominal + c.Lower
	c.UpperLimit = c.Nominal + c.Upper
	c.HasLimits = true
}

// defaultTolerance looks up the title block band for a nominal written with a
// given number of decimal places.
func defaultTolerance(literal string, c *Characteristic, opt Options) (float64, bool) {
	if c.Kind == KindAngular {
		if opt.DefaultAngular > 0 {
			return opt.DefaultAngular, true
		}
		return 0, false
	}
	t, ok := opt.DefaultTolerances[decimals(literal)]
	return t, ok
}

func parseThread(c *Characteristic, s string) bool {
	if m := metricThread.FindStringSubmatch(s); m != nil {
		d, _ := strconv.ParseFloat(m[1], 64)
		p, _ := strconv.ParseFloat(m[2], 64)
		c.Kind, c.Unit = KindThread, UnitMM
		c.Nominal, c.HasNominal = d, true
		c.Thread = &Thread{Series: "M", MajorDia: d, Pitch: p, Class: m[3], Designation: s}
		return true
	}
	if m := unifiedThrd.FindStringSubmatch(s); m != nil {
		tpi, _ := strconv.ParseFloat(m[2], 64)
		c.Kind, c.Unit = KindThread, UnitInch
		if d, ok := unifiedMajorDia(m[1]); ok {
			c.Nominal, c.HasNominal = d, true
		}
		c.Thread = &Thread{Series: strings.ToUpper(m[3]), MajorDia: c.Nominal, TPI: tpi, Class: m[4], Designation: s}
		return true
	}
	return false
}

// unifiedMajorDia converts a unified thread size designator to inches. Numbered
// sizes (#4, #10) follow the ASME B1.1 rule: diameter = 0.060 + 0.013 × number.
func unifiedMajorDia(size string) (float64, bool) {
	if strings.HasPrefix(size, "#") {
		n, err := strconv.Atoi(size[1:])
		if err != nil {
			return 0, false
		}
		return 0.060 + 0.013*float64(n), true
	}
	if i := strings.IndexByte(size, '/'); i > 0 {
		numer, err1 := strconv.ParseFloat(size[:i], 64)
		denom, err2 := strconv.ParseFloat(size[i+1:], 64)
		if err1 != nil || err2 != nil || denom == 0 {
			return 0, false
		}
		return numer / denom, true
	}
	v, err := strconv.ParseFloat(size, 64)
	return v, err == nil
}

func parseRoughness(c *Characteristic, s string) bool {
	m := roughnessRe.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	v, err := strconv.ParseFloat(m[2], 64)
	if err != nil {
		return false
	}
	c.Kind, c.Nominal, c.HasNominal = KindSurfaceFinish, v, true
	c.Unit = UnitMicron
	c.TolType = TolMax // roughness callouts are upper bounds
	c.UpperLimit, c.HasLimits = v, true
	// Keep the parameter as the standard writes it (Ra, not RA): the report
	// echoes the drawing, and "RA" is not a thing.
	c.FinishLabel = strings.ToUpper(m[1][:1]) + strings.ToLower(m[1][1:])
	return true
}

func parseChamfer(c *Characteristic, s string, opt Options) bool {
	m := chamferRe.FindStringSubmatch(s)
	if m == nil {
		return false
	}
	leg, err1 := strconv.ParseFloat(m[1], 64)
	ang, err2 := strconv.ParseFloat(m[2], 64)
	if err1 != nil || err2 != nil {
		return false
	}
	c.Kind, c.Nominal, c.HasNominal = KindChamfer, leg, true
	c.ChamferAngle = ang

	// The leg is a measured length, so it inherits the title block default the
	// same way any other untoleranced dimension does. Without this the chamfer
	// reaches the report with no acceptance limits for the inspector to use.
	if t, ok := opt.DefaultTolerances[decimals(m[1])]; ok {
		c.TolType = TolNone
		c.Upper, c.Lower = t, -t
		c.LowerLimit, c.UpperLimit, c.HasLimits = leg-t, leg+t, true
	}
	return true
}

// decimals counts the digits after the decimal point in a numeric literal.
func decimals(literal string) int {
	if i := strings.IndexByte(literal, '.'); i >= 0 {
		return len(literal) - i - 1
	}
	return 0
}

// finish applies invariants that hold regardless of which branch parsed the
// callout.
func finish(c *Characteristic, opt Options) Characteristic {
	if c.Quantity < 1 {
		c.Quantity = 1
	}
	if c.Unit == UnitUnknown {
		c.Unit = opt.Unit
	}
	if c.HasLimits && c.UpperLimit < c.LowerLimit && c.TolType != TolMin {
		c.warn("upper limit %g is below lower limit %g", c.UpperLimit, c.LowerLimit)
	}
	return *c
}

func absf(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
