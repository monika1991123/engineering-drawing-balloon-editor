package dimension

import (
	"regexp"
	"strings"
)

// The same engineering symbol reaches us in several encodings depending on how
// the drawing was authored and how the PDF text layer was extracted:
//
//   - AutoCAD control codes survive as literal "%%c" / "%%d" / "%%p".
//   - GD&T fonts map glyphs onto ASCII letters, so a position symbol can arrive
//     as a bare "j" and flatness as "c" (the AutoCAD gdt.shx convention).
//   - Unicode has several near-duplicates for diameter alone: U+2300 ⌀,
//     U+00D8 Ø, U+03D5 ϕ, U+0444 ф.
//
// Everything is folded to one canonical glyph before parsing so the grammar
// downstream only has to know a single spelling.

const (
	symDiameter  = "⌀"
	symDegree    = "°"
	symPlusMinus = "±"
	symSquare    = "□"
	symDepth     = "↧"
	symCounterb  = "⌴"
	symCountersk = "⌵"
)

// replacer folds encoding variants onto canonical glyphs. Order matters: the
// multi-character AutoCAD codes must be replaced before single characters.
var replacer = strings.NewReplacer(
	// AutoCAD control codes
	"%%C", symDiameter, "%%c", symDiameter,
	"%%D", symDegree, "%%d", symDegree,
	"%%P", symPlusMinus, "%%p", symPlusMinus,
	// diameter variants
	"Ø", symDiameter, "ø", symDiameter,
	"ϕ", symDiameter, "φ", symDiameter, "Φ", symDiameter,
	"ф", symDiameter, "∅", symDiameter,
	// degree variants
	"º", symDegree, "˚", symDegree, "∘", symDegree,
	// plus/minus variants
	"+/-", symPlusMinus, "+-", symPlusMinus, "∓", symPlusMinus,
	// dashes: en/em dash and minus sign all mean "minus" or "to" here
	"–", "-", "—", "-", "−", "-",
	// primes used for feet/inches
	"″", "\"", "′", "'",
	// non-breaking and thin spaces
	" ", " ", " ", " ", " ", " ",
)

// gdtSymbols maps canonical GD&T glyphs to their ASME Y14.5 characteristic
// names. The ASCII values are the gdt.shx font mappings, which is how these
// symbols arrive when a PDF text layer is extracted from an AutoCAD drawing
// that used the symbol font rather than real Unicode.
var gdtSymbols = map[string]string{
	"⌖": "position",
	"⌭": "cylindricity",
	"⌒": "profile_of_a_line",
	"⌓": "profile_of_a_surface",
	"⏊": "perpendicularity",
	"⊥": "perpendicularity",
	"∥": "parallelism",
	"∠": "angularity",
	"⌰": "total_runout",
	"↗": "runout",
	"⏥": "flatness",
	"○": "circularity",
	"⌱": "concentricity",
	"⌯": "symmetry",
	"—": "straightness",
	"⏤": "straightness",
}

// gdtASCII covers the gdt.shx font substitutions. These are deliberately kept
// separate from gdtSymbols: a bare "j" only means "position" when it sits at the
// head of something that already looks like a feature control frame, so the
// parser consults this map only after that check.
var gdtASCII = map[string]string{
	"j": "position",
	"g": "cylindricity",
	"k": "profile_of_a_line",
	"d": "profile_of_a_surface",
	"b": "perpendicularity",
	"f": "parallelism",
	"a": "angularity",
	"t": "total_runout",
	"h": "runout",
	"c": "flatness",
	"e": "circularity",
	"r": "concentricity",
	"i": "symmetry",
	"u": "straightness",
}

// materialModifiers maps the circled material condition symbols. RFS
// (regardless of feature size) is the default when no modifier is present.
var materialModifiers = map[string]string{
	"Ⓜ": "MMC", "(M)": "MMC", "@M": "MMC",
	"Ⓛ": "LMC", "(L)": "LMC", "@L": "LMC",
	"Ⓢ": "RFS", "(S)": "RFS", "@S": "RFS",
}

// normalize folds encoding variants and collapses whitespace so the grammar
// downstream sees one canonical spelling of every symbol.
var decimalComma = regexp.MustCompile(`(\d),(\d)`)

func normalize(s string) string {
	s = replacer.Replace(s)
	s = decimalComma.ReplaceAllString(s, `${1}.${2}`)
	return strings.Join(strings.Fields(s), " ")
}

// hasGDTSymbol reports whether the string opens with a Unicode geometric
// characteristic symbol, returning its ASME name.
func hasGDTSymbol(s string) (string, string, bool) {
	for glyph, name := range gdtSymbols {
		if strings.HasPrefix(s, glyph) {
			return name, glyph, true
		}
	}
	return "", "", false
}
