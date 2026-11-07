package dimension

import (
	"math"
	"regexp"
	"strconv"
)

// ISO 286-1 limits and fits.
//
// A callout like "25 H7" carries no explicit tolerance — the fit class encodes
// it. Resolving it takes two independent pieces:
//
//  1. The IT grade width, from the standard tolerance factor i, which depends on
//     the nominal size band rather than the nominal size itself.
//  2. The fundamental deviation, which positions that width relative to the
//     nominal and depends on the letter.
//
// Accuracy note: the values here are computed from the ISO 286-1 formulas
// rather than looked up in the published tables. The standard rounds tabulated
// values to preferred increments, so a computed grade can land within about
// 1 µm of the book figure (checked: 25H7 → 21 µm and 50H8 → 39 µm match; 10H7
// computes 14 µm against a tabulated 15 µm). Every fit-derived tolerance is
// therefore flagged with a warning so nobody signs off an acceptance decision
// on a computed value without checking the standard.

// sizeSteps are the ISO 286 nominal size bands in mm. The standard tolerance
// factor is evaluated at the geometric mean of the enclosing band.
var sizeSteps = []struct{ lo, hi float64 }{
	{0, 3}, {3, 6}, {6, 10}, {10, 18}, {18, 30}, {30, 50}, {50, 80},
	{80, 120}, {120, 180}, {180, 250}, {250, 315}, {315, 400}, {400, 500},
}

// itMultipliers are the multiples of the standard tolerance factor i that define
// grades IT5 and coarser.
var itMultipliers = map[int]float64{
	5: 7, 6: 10, 7: 16, 8: 25, 9: 40, 10: 64, 11: 100,
	12: 160, 13: 250, 14: 400, 15: 640, 16: 1000, 17: 1600, 18: 2500,
}

// stepMean returns the geometric mean of the size band containing d (mm).
func stepMean(d float64) (float64, bool) {
	d = math.Abs(d)
	for _, s := range sizeSteps {
		if d > s.lo && d <= s.hi {
			lo := s.lo
			if lo == 0 {
				lo = 1 // the standard treats the first band as 1..3 for this purpose
			}
			return math.Sqrt(lo * s.hi), true
		}
	}
	return 0, false
}

// toleranceFactor is ISO 286's standard tolerance factor i, in micrometres, for
// nominal sizes up to 500 mm.
func toleranceFactor(d float64) (float64, bool) {
	mean, ok := stepMean(d)
	if !ok {
		return 0, false
	}
	return 0.45*math.Cbrt(mean) + 0.001*mean, true
}

// itGrade returns the width of tolerance grade ITn at nominal size d, in
// micrometres.
func itGrade(d float64, grade int) (float64, bool) {
	mult, ok := itMultipliers[grade]
	if !ok {
		return 0, false
	}
	i, ok := toleranceFactor(d)
	if !ok {
		return 0, false
	}
	return math.Round(mult * i), true
}

// fundamentalDeviation returns the deviation of the tolerance zone from the
// nominal, in micrometres, for the deviation letter at nominal size d.
//
// The returned value is the upper deviation (es) for shafts and the lower
// deviation (EI) for holes — that is the side of the zone the standard
// positions. ok is false for letters this implementation does not resolve.
func fundamentalDeviation(letter string, d float64, isHole bool) (float64, bool) {
	mean, ok := stepMean(d)
	if !ok {
		return 0, false
	}

	// H and h place the zone flush against the nominal; they are exact and are
	// the overwhelming majority of real callouts.
	switch letter {
	case "H":
		return 0, true // EI = 0, zone runs positive
	case "h":
		return 0, true // es = 0, zone runs negative
	case "JS", "js":
		return 0, true // symmetric; handled specially by the caller
	}

	// Shaft deviations with closed-form definitions in ISO 286-1. Hole
	// deviations of the same letter are the mirror image (the "general rule").
	var es float64
	switch letter {
	case "g", "G":
		es = -2.5 * math.Pow(mean, 0.34)
	case "f", "F":
		es = -5.5 * math.Pow(mean, 0.41)
	case "e", "E":
		es = -11 * math.Pow(mean, 0.41)
	case "d", "D":
		es = -16 * math.Pow(mean, 0.44)
	default:
		return 0, false
	}
	es = math.Round(es)
	if isHole {
		return -es, true // mirror for internal features
	}
	return es, true
}

var fitRe = regexp.MustCompile(`^(JS|js|[A-Za-z]{1,2})(\d{1,2})$`)

// parseFitClass recognises a token such as "H7", "g6" or "js9".
func parseFitClass(tok string) (Fit, bool) {
	m := fitRe.FindStringSubmatch(tok)
	if m == nil {
		return Fit{}, false
	}
	grade, err := strconv.Atoi(m[2])
	if err != nil || grade < 5 || grade > 18 {
		return Fit{}, false
	}
	letter := m[1]
	// A single uppercase letter denotes a hole, lowercase a shaft. "JS"/"js"
	// follow the same convention.
	isHole := letter[0] >= 'A' && letter[0] <= 'Z'
	return Fit{Deviation: letter, Grade: grade, IsHole: isHole}, true
}

// resolve computes the signed deviations, in millimetres, that the fit class
// implies at the given nominal size.
func (f Fit) resolve(nominal float64) (upper, lower float64, ok bool) {
	width, ok := itGrade(nominal, f.Grade)
	if !ok {
		return 0, 0, false
	}
	dev, ok := fundamentalDeviation(f.Deviation, nominal, f.IsHole)
	if !ok {
		return 0, 0, false
	}

	const µm = 0.001
	switch {
	case f.Deviation == "JS" || f.Deviation == "js":
		half := width / 2
		return half * µm, -half * µm, true
	case f.IsHole:
		// dev is the lower deviation EI; the zone runs upward from it.
		return (dev + width) * µm, dev * µm, true
	default:
		// dev is the upper deviation es; the zone runs downward from it.
		return dev * µm, (dev - width) * µm, true
	}
}
