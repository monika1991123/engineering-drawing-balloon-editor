package api

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/layout"
	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/model"
)

func TestOCRRejectsUntrustedOriginAndInvalidImageBeforeInference(t *testing.T) {
	for _, tc := range []struct {
		origin, content, data string
		want                  int
	}{
		{"https://other.example", "image/png", "", 403},
		{"", "application/json", "{}", 415},
		{"", "image/png", "invalid PNG", 400},
	} {
		r := httptest.NewRequest("POST", "/api/ocr", bytes.NewBufferString(tc.data))
		r.Header.Set("Content-Type", tc.content)
		r.Header.Set("Origin", tc.origin)
		w := httptest.NewRecorder()
		srv().ServeHTTP(w, r)
		if w.Code != tc.want {
			t.Fatalf("got %d, expected %d: %s", w.Code, tc.want, w.Body)
		}
	}
}

func TestOCRSerializesInferenceOnSmallVPS(t *testing.T) {
	s := srv()
	s.ocrSlot <- struct{}{}
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 64, 64))); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/ocr", &buf)
	r.Header.Set("Content-Type", "image/png")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("got %d, want 429", w.Code)
	}
}

func TestStrictBuildFlagsOCRAndValidationRejectsManualCollision(t *testing.T) {
	source := model.TextItem{Text: "124.5", OCR: true, Confidence: .99, RawText: "124,5", Box: layout.Rect{X: 100, Y: 100, W: 30, H: 10}}
	d := model.Drawing{Pages: []model.Page{{Width: 400, Height: 300, Strict: true}}}
	w := do(t, "POST", "/api/build", map[string]any{"drawing": d, "texts": []model.TextItem{source}})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Items) != 1 {
		t.Fatalf("wrong candidate count %d", len(d.Items))
	}
	it := &d.Items[0]
	if !it.Clean || it.Pending || !it.ReviewRequired || it.Include {
		t.Fatalf("unexpected OCR status %+v", it)
	}
	if it.Requirement != "124.5" || it.LimitsText != "" || it.Char.HasLimits {
		t.Fatal("OCR invented title-block tolerances")
	}
	it.Manual = true
	it.Anchor = &layout.Point{X: 130, Y: 100}
	it.Balloon.C = source.Box.Center()
	w = do(t, "POST", "/api/validate", map[string]any{"drawing": d, "page": 0})
	if w.Code != 200 {
		t.Fatalf("manual fields failed to round trip: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.Items[0].Clean || len(d.Items[0].Issues) == 0 {
		t.Fatal("manual overlap incorrectly passed validation")
	}
}

func TestOCRParseDoesNotInventDefaultTolerance(t *testing.T) {
	w := do(t, "POST", "/api/parse", map[string]any{"text": "7.2", "ocr": true, "tolerances": map[string]float64{"1": .2}})
	var got struct {
		Requirement string `json:"requirement"`
		Limits      string `json:"limits"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Requirement != "7.2" || got.Limits != "" {
		t.Fatalf("invented OCR requirement: %+v", got)
	}
}

func TestAllPagesResizePreservesSafePositionsAndUnresolvedPlacedItems(t *testing.T) {
	d := model.Drawing{BalloonRadius: 12, Pages: []model.Page{{Index: 0, Width: 500, Height: 400}, {Index: 1, Width: 500, Height: 400, Obstacles: []layout.Rect{{W: 500, H: 400}}}}, Items: []model.Item{
		{ID: "safe", Number: 1, Page: 0, Clean: true, Source: model.TextItem{Box: layout.Rect{X: 100, Y: 100, W: 20, H: 10}}, Balloon: layout.Circle{C: layout.Point{X: 170, Y: 105}, R: 9}},
		{ID: "blocked", Number: 2, Page: 1, Manual: true, Source: model.TextItem{Box: layout.Rect{X: 100, Y: 100, W: 20, H: 10}}, Balloon: layout.Circle{C: layout.Point{X: 170, Y: 105}, R: 9}},
	}}
	w := do(t, "POST", "/api/layout", map[string]any{"drawing": d, "all_pages": true})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var got model.Drawing
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for i, it := range got.Items {
		if it.Balloon.R != 12 || it.Pending || it.Balloon.C != d.Items[i].Balloon.C {
			t.Fatalf("lost existing position/size: %+v", it)
		}
	}
	if !got.Items[0].Clean || got.Items[1].Clean || len(got.Items[1].Issues) == 0 || !got.Items[1].Manual {
		t.Fatalf("wrong validation status: %+v", got.Items)
	}
	d.BalloonRadius = 99
	if w = do(t, "POST", "/api/layout", map[string]any{"drawing": d, "all_pages": true}); w.Code != 400 {
		t.Fatalf("invalid radius accepted: %d", w.Code)
	}
}
