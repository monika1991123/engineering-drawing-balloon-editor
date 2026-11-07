package dimension

import "testing"

func TestEngineeringSymbolsQuantityAndReference(t *testing.T) {
	for _, tc := range []struct {
		raw       string
		kind      Kind
		nominal   float64
		quantity  int
		reference bool
	}{
		{"(Ø9,8)", KindDiameter, 9.8, 1, true}, {"(⌀7.8)", KindDiameter, 7.8, 1, true},
		{"4-R3", KindRadius, 3, 4, false}, {"4-Ø4", KindDiameter, 4, 4, false}, {"90°", KindAngular, 90, 1, false},
		{"45º", KindAngular, 45, 1, false},
		{"(⌀9.8) REF", KindDiameter, 9.8, 1, true},
	} {
		c := Parse(tc.raw, DefaultOptions())
		if c.Kind != tc.kind || c.Nominal != tc.nominal || c.Quantity != tc.quantity || (c.TolType == TolReference) != tc.reference {
			t.Errorf("%s parsed incorrectly: %+v", tc.raw, c)
		}
		if tc.reference && c.Kind == KindDiameter && c.Requirement() != "(⌀"+trim(tc.nominal)+") REF" {
			t.Errorf("reference diameter lost its symbol: %s", c.Requirement())
		}
	}
}
