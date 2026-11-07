package dimension

import (
	"regexp"
	"strconv"
	"strings"
)

// A feature control frame is drawn as a row of boxes:
//
//	┌───┬─────────┬───┬───┬───┐
//	│ ⌖ │ ⌀0.2 Ⓜ │ A │ B │ C │
//	└───┴─────────┴───┴───┴───┘
//
// The box borders are graphics, not text, so by the time the frame reaches us
// the compartments have been flattened into one string. Sometimes the extractor
// preserves a pipe or box-drawing character between compartments; often it just
// leaves whitespace. Both have to parse.

var (
	fcfSplit  = regexp.MustCompile(`\s*[|│┃‖]\s*`)
	datumRe   = regexp.MustCompile(`^[A-Z]{1,2}$`)
	zoneRe    = regexp.MustCompile(`^(` + symDiameter + `|S` + symDiameter + `)?\s*(` + num + `)\s*(.*)$`)
	matModRe  = regexp.MustCompile(`(?i)(Ⓜ|Ⓛ|Ⓢ|\(M\)|\(L\)|\(S\)|@M|@L|@S)`)
	asciiFCF  = regexp.MustCompile(`^([a-z])\s*[|│]`)
	zoneStart = regexp.MustCompile(`^(?:` + symDiameter + `)?\s*` + num)
)

// parseFCF recognises a feature control frame and pulls out its compartments.
func parseFCF(s string) (*FeatureControlFrame, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, false
	}

	name, glyph, ok := hasGDTSymbol(s)
	rest := ""
	switch {
	case ok:
		// Trim the separator that follows the symbol compartment as well as the
		// symbol itself, so the tolerance zone lands in parts[0] rather than
		// behind an empty leading field.
		rest = strings.TrimLeft(strings.TrimPrefix(s, glyph), "|│┃‖ ")
	default:
		// A symbol-font drawing gives us a bare ASCII letter. Only treat it as a
		// geometric symbol when a compartment separator follows, otherwise "c"
		// is just a letter in a note.
		m := asciiFCF.FindStringSubmatch(s)
		if m == nil {
			return nil, false
		}
		n, found := gdtASCII[m[1]]
		if !found {
			return nil, false
		}
		name, glyph = n, m[1]
		rest = strings.TrimSpace(s[len(m[0]):])
	}

	if rest == "" {
		return nil, false
	}

	parts := fcfSplit.Split(rest, -1)
	if len(parts) == 1 {
		// No separators survived; fall back to splitting on whitespace, which
		// works because a tolerance zone never contains a space once the
		// material modifier is attached.
		parts = strings.Fields(rest)
	}
	if len(parts) == 0 {
		return nil, false
	}

	fcf := &FeatureControlFrame{Symbol: name, SymbolRaw: glyph, Material: "RFS"}

	zone := strings.TrimSpace(parts[0])
	if !zoneStart.MatchString(zone) {
		return nil, false
	}
	m := zoneRe.FindStringSubmatch(zone)
	if m == nil {
		return nil, false
	}
	if strings.Contains(m[1], symDiameter) {
		fcf.Diametral = true
	}
	v, err := strconv.ParseFloat(m[2], 64)
	if err != nil {
		return nil, false
	}
	fcf.Value = v

	// The material modifier may be glued to the zone or sit in its own field.
	tail := m[3]
	if mm := matModRe.FindString(zone + " " + tail); mm != "" {
		if name, ok := materialModifiers[normalizeModifier(mm)]; ok {
			fcf.Material = name
		}
	}

	for _, p := range parts[1:] {
		p = strings.TrimSpace(matModRe.ReplaceAllString(p, ""))
		p = strings.TrimSpace(strings.Trim(p, "()"))
		if p == "" {
			continue
		}
		if datumRe.MatchString(strings.ToUpper(p)) {
			fcf.Datums = append(fcf.Datums, strings.ToUpper(p))
		}
	}

	return fcf, true
}

func normalizeModifier(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	switch s {
	case "(M)", "@M", "Ⓜ":
		return "Ⓜ"
	case "(L)", "@L", "Ⓛ":
		return "Ⓛ"
	case "(S)", "@S", "Ⓢ":
		return "Ⓢ"
	}
	return s
}
