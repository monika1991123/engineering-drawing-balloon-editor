package layout

import "testing"

func TestSafeSolveHasNoCertifiedConflicts(t *testing.T) {
	page := Rect{W: 500, H: 400}
	anchors := []Anchor{
		{ID: "a", Number: 1, At: Point{120, 100}, Avoid: Rect{110, 95, 20, 10}},
		{ID: "b", Number: 2, At: Point{150, 100}, Avoid: Rect{140, 95, 20, 10}},
		{ID: "c", Number: 3, At: Point{120, 160}, Avoid: Rect{110, 155, 20, 10}},
	}
	texts := []Rect{anchors[0].Avoid, anchors[1].Avoid, anchors[2].Avoid}
	ink := append(append([]Rect{}, texts...), Rect{160, 130, 130, 2})
	got := SafeSolve(anchors, ink, texts, page)
	for i, p := range got {
		if !p.Clean {
			t.Fatalf("uncrowded page unexpectedly pending: %v", p.Issues)
		}
		if issues := CheckSafe(anchors[i], p, got, ink, texts, page); len(issues) != 0 {
			t.Fatalf("solver certified an unsafe placement: %v", issues)
		}
		if anchors[i].Avoid.Contains(p.Leader.A) {
			t.Error("leader starts inside dimension text")
		}
	}
}

func TestSafeSolveLeavesImpossibleDrawingPending(t *testing.T) {
	page := Rect{W: 200, H: 200}
	a := Anchor{ID: "a", At: Point{100, 100}, Avoid: Rect{90, 95, 20, 10}}
	p := SafeSolve([]Anchor{a}, []Rect{page}, []Rect{a.Avoid}, page)[0]
	if p.Clean || len(p.Issues) == 0 || p.Balloon.R != 0 {
		t.Fatal("must not emit an overlapping balloon as a fallback")
	}
}

func TestValidationRejectsHumanDragOntoTextAndOtherBalloons(t *testing.T) {
	a := Anchor{ID: "a", At: Point{50, 50}, Avoid: Rect{45, 45, 10, 10}}
	p := Placement{AnchorID: "a", Balloon: Circle{Point{100, 100}, 11}, Leader: Segment{Point{57, 57}, Point{92, 92}}}
	page := Rect{W: 300, H: 300}
	if len(CheckSafe(a, p, nil, nil, []Rect{{95, 95, 10, 10}}, page)) == 0 {
		t.Error("text overlap was accepted")
	}
	other := Placement{AnchorID: "b", Clean: true, Balloon: Circle{Point{100, 100}, 11}}
	if len(CheckSafe(a, p, []Placement{other}, nil, nil, page)) == 0 {
		t.Error("balloon overlap was accepted")
	}
}

func TestCompressedInkSpanOnlyExemptsLocalTarget(t *testing.T) {
	ink := Rect{0, 50, 300, 3}
	exempt := Rect{45, 45, 20, 20}
	if !hitsOutside(Segment{Point{150, 30}, Point{150, 80}}, ink, exempt) {
		t.Fatal("long dimension line disappeared outside the local target exemption")
	}
	if hitsOutside(Segment{Point{50, 47}, Point{50, 57}}, ink, exempt) {
		t.Fatal("target glyphs should be exempt")
	}
}

func TestHollowTailWidthParticipatesInAvoidance(t *testing.T) {
	a := Anchor{ID: "a", At: Point{30, 50}, Avoid: Rect{20, 45, 20, 10}}
	p := Placement{AnchorID: "a", Balloon: Circle{Point{100, 50}, 9}, Leader: Segment{Point{42, 50}, Point{91, 50}}}
	ink := Rect{60, 51.2, 10, .5}
	if p.Leader.IntersectsRect(ink) {
		t.Fatal("fixture must miss the center line")
	}
	if len(CheckSafe(a, p, nil, []Rect{ink}, nil, Rect{W: 300, H: 300})) == 0 {
		t.Fatal("ink beside the center line can still overlap the hollow tail")
	}
}

func TestReflowKeepsSafePositionsAndMovesNewConflicts(t *testing.T) {
	anchors := []Anchor{{ID: "a", At: Point{80, 100}, Avoid: Rect{70, 95, 20, 10}}, {ID: "b", At: Point{250, 100}, Avoid: Rect{240, 95, 20, 10}}}
	current := []Placement{{Balloon: Circle{Point{130, 100}, 9}, Clean: true}, {Balloon: Circle{Point{130, 100}, 9}, Clean: false}}
	got := SafeSolveWithPositions(anchors, nil, []Rect{anchors[0].Avoid, anchors[1].Avoid}, Rect{W: 500, H: 400}, current, 9)
	if got[0].Balloon.C != current[0].Balloon.C {
		t.Fatal("safe existing position was reset")
	}
	if !got[1].Clean || got[1].Balloon.C == current[1].Balloon.C {
		t.Fatal("new conflict was not moved")
	}
	resized := SafeSolveWithPositions(anchors, nil, nil, Rect{W: 500, H: 400}, got, 12)
	for _, p := range resized {
		if p.Balloon.R != 12 {
			t.Fatal("requested size was lost on reflow")
		}
	}
}

func TestUnresolvedPlacedItemStaysVisible(t *testing.T) {
	a := Anchor{ID: "a", At: Point{100, 100}, Avoid: Rect{90, 95, 20, 10}}
	current := []Placement{{Balloon: Circle{Point{150, 150}, 9}}}
	p := SafeSolveWithPositions([]Anchor{a}, []Rect{{W: 300, H: 300}}, nil, Rect{W: 300, H: 300}, current, 12)[0]
	if p.Clean || p.Balloon.C != current[0].Balloon.C || p.Balloon.R != 12 {
		t.Fatal("unresolved placed item disappeared or reverted its size")
	}
}
