package dimension

import (
	"math"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestParse(t *testing.T) {
	opt := DefaultOptions()

	tests := []struct {
		name  string
		in    string
		check func(*testing.T, Characteristic)
	}{
		{
			name: "symmetric diameter",
			in:   "⌀12.50 ±0.05",
			check: func(t *testing.T, c Characteristic) {
				want(t, c.Kind, KindDiameter)
				wantF(t, c.Nominal, 12.50)
				want(t, c.TolType, TolSymmetric)
				wantF(t, c.LowerLimit, 12.45)
				wantF(t, c.UpperLimit, 12.55)
			},
		},
		{
			name: "quantity prefix",
			in:   "4X ⌀5.00 ±0.05",
			check: func(t *testing.T, c Characteristic) {
				want(t, c.Quantity, 4)
				want(t, c.Kind, KindDiameter)
				wantF(t, c.Nominal, 5.0)
			},
		},
		{
			name: "autocad control codes",
			in:   "%%c12.50 %%p0.05",
			check: func(t *testing.T, c Characteristic) {
				want(t, c.Kind, KindDiameter)
				want(t, c.TolType, TolSymmetric)
				wantF(t, c.UpperLimit, 12.55)
			},
		},
		{
			name: "bilateral unequal",
			in:   "12.50 +0.05/-0.02",
			check: func(t *testing.T, c Characteristic) {
				want(t, c.TolType, TolBilateral)
				wantF(t, c.Upper, 0.05)
				wantF(t, c.Lower, -0.02)
				wantF(t, c.UpperLimit, 12.55)
				wantF(t, c.LowerLimit, 12.48)
			},
		},
		{
			name: "limit dimension",
			in:   "12.45-12.55",
			check: func(t *testing.T, c Characteristic) {
				want(t, c.TolType, TolLimits)
				wantF(t, c.Nominal, 12.50)
				wantF(t, c.LowerLimit, 12.45)
				wantF(t, c.UpperLimit, 12.55)
			},
		},
		{
			name: "radius inherits title block default",
			in:   "R3.2",
			check: func(t *testing.T, c Characteristic) {
				want(t, c.Kind, KindRadius)
				want(t, c.TolType, TolNone)
				wantF(t, c.Upper, 0.2) // one decimal place
				wantF(t, c.LowerLimit, 3.0)
			},
		},
		{
			name: "three place decimal picks tighter default",
			in:   "12.500",
			check: func(t *testing.T, c Characteristic) {
				wantF(t, c.Upper, 0.05)
			},
		},
		{
			name: "max only",
			in:   "12.50 MAX",
			check: func(t *testing.T, c Characteristic) {
				want(t, c.TolType, TolMax)
				wantF(t, c.UpperLimit, 12.50)
			},
		},
		{
			name: "min only",
			in:   "12.50 MIN",
			check: func(t *testing.T, c Characteristic) {
				want(t, c.TolType, TolMin)
				wantF(t, c.LowerLimit, 12.50)
			},
		},
		{
			name: "reference is not inspected",
			in:   "(12.50)",
			check: func(t *testing.T, c Characteristic) {
				want(t, c.TolType, TolReference)
				if c.Inspectable() {
					t.Error("reference dimension must not be inspectable")
				}
			},
		},
		{
			name: "basic is not inspected",
			in:   "[12.50]",
			check: func(t *testing.T, c Characteristic) {
				want(t, c.TolType, TolBasic)
				if c.Inspectable() {
					t.Error("basic dimension must not be inspectable")
				}
			},
		},
		{
			name: "angular default",
			in:   "45°",
			check: func(t *testing.T, c Characteristic) {
				want(t, c.Kind, KindAngular)
				want(t, c.Unit, UnitDegree)
				wantF(t, c.UpperLimit, 45.5)
				wantF(t, c.LowerLimit, 44.5)
			},
		},
		{
			name: "chamfer",
			in:   "2 X 45°",
			check: func(t *testing.T, c Characteristic) {
				want(t, c.Kind, KindChamfer)
				wantF(t, c.Nominal, 2)
				want(t, c.Quantity, 1) // the X is a chamfer angle, not a count
			},
		},
		{
			name: "metric thread",
			in:   "M6x1.0-6H",
			check: func(t *testing.T, c Characteristic) {
				want(t, c.Kind, KindThread)
				if c.Thread == nil {
					t.Fatal("thread not parsed")
				}
				want(t, c.Thread.Series, "M")
				wantF(t, c.Thread.MajorDia, 6)
				wantF(t, c.Thread.Pitch, 1.0)
				want(t, c.Thread.Class, "6H")
			},
		},
		{
			name: "unified fractional thread",
			in:   "1/4-20 UNC-2B",
			check: func(t *testing.T, c Characteristic) {
				want(t, c.Kind, KindThread)
				want(t, c.Unit, UnitInch)
				wantF(t, c.Thread.MajorDia, 0.25)
				wantF(t, c.Thread.TPI, 20)
				want(t, c.Thread.Class, "2B")
			},
		},
		{
			name: "unified numbered thread",
			in:   "#10-32 UNF-2A",
			check: func(t *testing.T, c Characteristic) {
				// ASME B1.1: 0.060 + 0.013 x 10
				wantF(t, c.Thread.MajorDia, 0.190)
				want(t, c.Thread.Series, "UNF")
			},
		},
		{
			name: "surface roughness is an upper bound",
			in:   "Ra 1.6",
			check: func(t *testing.T, c Characteristic) {
				want(t, c.Kind, KindSurfaceFinish)
				want(t, c.TolType, TolMax)
				wantF(t, c.UpperLimit, 1.6)
			},
		},
		{
			name: "modifier is captured not dropped",
			in:   "⌀12.5 THRU",
			check: func(t *testing.T, c Characteristic) {
				want(t, c.Kind, KindDiameter)
				wantF(t, c.Nominal, 12.5)
				if len(c.Modifiers) == 0 || c.Modifiers[0] != "THRU" {
					t.Errorf("THRU not captured, got %v", c.Modifiers)
				}
			},
		},
		{
			name: "unparseable text survives as a note",
			in:   "SEE DETAIL B",
			check: func(t *testing.T, c Characteristic) {
				want(t, c.Kind, KindNote)
				want(t, c.Raw, "SEE DETAIL B")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, Parse(tc.in, opt))
		})
	}
}

// TestISOFits checks fit classes against values from the published ISO 286
// tables. These are the cases the formula reproduces exactly; see the accuracy
// note in isofit.go for why a tolerance band is allowed.
func TestISOFits(t *testing.T) {
	opt := DefaultOptions()

	tests := []struct {
		in               string
		wantUpper        float64
		wantLower        float64
		toleranceMicrons float64
	}{
		{"25 H7", +0.021, 0.000, 1},  // table: +21 / 0
		{"50 H8", +0.039, 0.000, 1},  // table: +39 / 0
		{"25 g6", -0.007, -0.020, 1}, // table: -7 / -20
		{"25 f7", -0.020, -0.041, 1}, // table: -20 / -41
		{"30 h7", 0.000, -0.021, 1},  // table: 0 / -21
	}

	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			c := Parse(tc.in, opt)
			if c.TolType != TolFit {
				t.Fatalf("expected a fit tolerance, got %s", c.TolType)
			}
			tol := tc.toleranceMicrons / 1000
			if math.Abs(c.Upper-tc.wantUpper) > tol {
				t.Errorf("upper = %+.4f, want %+.4f", c.Upper, tc.wantUpper)
			}
			if math.Abs(c.Lower-tc.wantLower) > tol {
				t.Errorf("lower = %+.4f, want %+.4f", c.Lower, tc.wantLower)
			}
			// A computed fit must always warn; silently shipping a derived
			// acceptance limit is the failure mode this guards against.
			if len(c.Warnings) == 0 {
				t.Error("fit-derived tolerance must carry a warning")
			}
		})
	}
}

func TestFeatureControlFrames(t *testing.T) {
	opt := DefaultOptions()

	t.Run("position with material modifier and three datums", func(t *testing.T) {
		c := Parse("⌖|⌀0.2Ⓜ|A|B|C", opt)
		want(t, c.Kind, KindGeometric)
		if c.GDT == nil {
			t.Fatal("frame not parsed")
		}
		want(t, c.GDT.Symbol, "position")
		want(t, c.GDT.Diametral, true)
		wantF(t, c.GDT.Value, 0.2)
		want(t, c.GDT.Material, "MMC")
		if len(c.GDT.Datums) != 3 {
			t.Fatalf("want 3 datums, got %v", c.GDT.Datums)
		}
		want(t, c.GDT.Datums[0], "A")
		want(t, c.GDT.Datums[2], "C")
	})

	t.Run("whitespace separated frame", func(t *testing.T) {
		c := Parse("⊥ 0.05 A", opt)
		want(t, c.Kind, KindGeometric)
		want(t, c.GDT.Symbol, "perpendicularity")
		wantF(t, c.GDT.Value, 0.05)
		want(t, c.GDT.Material, "RFS")
	})

	t.Run("flatness has no datum", func(t *testing.T) {
		c := Parse("⏥ 0.1", opt)
		want(t, c.GDT.Symbol, "flatness")
		if len(c.GDT.Datums) != 0 {
			t.Errorf("flatness takes no datum, got %v", c.GDT.Datums)
		}
	})

	t.Run("bare letter is not a frame", func(t *testing.T) {
		// "c" only means flatness inside a frame; on its own it is a note.
		c := Parse("c", opt)
		if c.Kind == KindGeometric {
			t.Error("bare letter must not parse as a feature control frame")
		}
	})
}

func want[T comparable](t *testing.T, got, expect T) {
	t.Helper()
	if got != expect {
		t.Errorf("got %v, want %v", got, expect)
	}
}

func wantF(t *testing.T, got, expect float64) {
	t.Helper()
	if !near(got, expect) {
		t.Errorf("got %v, want %v", got, expect)
	}
}
