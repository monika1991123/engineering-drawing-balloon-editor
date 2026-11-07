package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/model"
	"github.com/monika1991123/engineering-drawing-balloon-editor/web"
)

func srv() *Server { return New(web.FS) }

func do(t *testing.T, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, path, nil)
	} else {
		buf, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = httptest.NewRequest(method, path, bytes.NewReader(buf))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	srv().ServeHTTP(w, r)
	return w
}

func TestServesEmbeddedFrontend(t *testing.T) {
	for _, path := range []string{"/", "/app.js", "/styles.css", "/vendor/pdf.mjs"} {
		t.Run(path, func(t *testing.T) {
			w := do(t, "GET", path, nil)
			if w.Code != http.StatusOK {
				t.Fatalf("got %d, want 200", w.Code)
			}
			if w.Body.Len() == 0 {
				t.Error("empty body")
			}
		})
	}
}

func TestDemoEndpoint(t *testing.T) {
	w := do(t, "GET", "/api/demo", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	var got struct {
		Drawing model.Drawing    `json:"drawing"`
		Texts   []model.TextItem `json:"texts"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Drawing.Items) == 0 {
		t.Fatal("demo returned no characteristics")
	}
	// The derived fields the browser renders its table from must be populated
	// server-side; the frontend does not reimplement the formatting rules.
	for _, it := range got.Drawing.Items {
		if it.Requirement == "" {
			t.Errorf("item %d has no requirement string", it.Number)
		}
	}
}

// The browser posts the drawing straight back for layout and export. If the
// JSON the server emits does not decode into the structs it expects, every
// action after the first build breaks — and DisallowUnknownFields makes that a
// hard failure rather than a silent one.
func TestDrawingRoundTrips(t *testing.T) {
	var demo struct {
		Drawing model.Drawing `json:"drawing"`
	}
	if err := json.Unmarshal(do(t, "GET", "/api/demo", nil).Body.Bytes(), &demo); err != nil {
		t.Fatal(err)
	}

	w := do(t, "POST", "/api/layout", map[string]any{"drawing": demo.Drawing, "page": 0})
	if w.Code != http.StatusOK {
		t.Fatalf("layout rejected the drawing it produced: %d %s", w.Code, w.Body)
	}
	var back model.Drawing
	if err := json.Unmarshal(w.Body.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Items) != len(demo.Drawing.Items) {
		t.Errorf("layout returned %d items, sent %d", len(back.Items), len(demo.Drawing.Items))
	}
}

func TestBuildFromExtractedText(t *testing.T) {
	w := do(t, "POST", "/api/build", map[string]any{
		"drawing": map[string]any{
			"id": "t", "part_number": "P-1",
			"pages": []map[string]any{{"index": 0, "width": 842, "height": 595}},
		},
		"texts": []map[string]any{
			{"text": "⌀12.50 ±0.05", "page": 0, "box": map[string]float64{"x": 100, "y": 100, "w": 60, "h": 10}},
			{"text": "SEE NOTE 3", "page": 0, "box": map[string]float64{"x": 300, "y": 100, "w": 50, "h": 10}},
		},
		"tolerances": map[string]float64{"2": 0.1},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body)
	}
	var d model.Drawing
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Items) != 1 {
		t.Fatalf("got %d items, want 1 (prose must not be ballooned)", len(d.Items))
	}
	if d.Items[0].Requirement != "⌀12.5 ±0.05" {
		t.Errorf("requirement = %q", d.Items[0].Requirement)
	}
}

func TestParseEndpoint(t *testing.T) {
	w := do(t, "POST", "/api/parse", map[string]any{"text": "25 H7"})
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	var got struct {
		Requirement string `json:"requirement"`
		Limits      string `json:"limits"`
		Inspectable bool   `json:"inspectable"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Requirement != "25 H7" || got.Limits != "25 / 25.021" || !got.Inspectable {
		t.Errorf("got %+v", got)
	}
}

func TestExportXLSXIsAWorkbook(t *testing.T) {
	var demo struct {
		Drawing model.Drawing `json:"drawing"`
	}
	_ = json.Unmarshal(do(t, "GET", "/api/demo", nil).Body.Bytes(), &demo)

	w := do(t, "POST", "/api/export.xlsx", map[string]any{
		"drawing": demo.Drawing, "page": 0,
		"meta": map[string]string{"SerialNumber": "SN-001"},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("got %d: %s", w.Code, w.Body)
	}
	// XLSX is a zip; the magic bytes are the cheapest real check that the
	// response is a workbook and not an error page.
	if got := w.Body.Bytes(); len(got) < 4 || got[0] != 'P' || got[1] != 'K' {
		t.Fatal("response is not a zip archive")
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "DWG-1042") || !strings.HasSuffix(strings.TrimSuffix(cd, `"`), ".xlsx") {
		t.Errorf("Content-Disposition = %q", cd)
	}
}

func TestExportSVG(t *testing.T) {
	var demo struct {
		Drawing model.Drawing `json:"drawing"`
	}
	_ = json.Unmarshal(do(t, "GET", "/api/demo", nil).Body.Bytes(), &demo)

	w := do(t, "POST", "/api/export.svg", map[string]any{"drawing": demo.Drawing, "page": 0})
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	if !strings.HasPrefix(w.Body.String(), "<svg") {
		t.Error("response is not an SVG")
	}
}

func TestMalformedRequestIsRejected(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/build", strings.NewReader(`{"nope":1}`))
	w := httptest.NewRecorder()
	srv().ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400 for an unknown field", w.Code)
	}
}

func TestLayoutRejectsUnknownPage(t *testing.T) {
	w := do(t, "POST", "/api/layout", map[string]any{
		"drawing": model.Drawing{Pages: []model.Page{{Index: 0, Width: 100, Height: 100}}},
		"page":    7,
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", w.Code)
	}
}
