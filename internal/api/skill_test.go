package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeSkill installs a stand-in for scripts/balloon.py that records its
// arguments and writes a manifest, then exits with the given status.
func fakeSkill(t *testing.T, exit int) (dir string) {
	t.Helper()
	dir = t.TempDir()
	os.MkdirAll(filepath.Join(dir, "scripts"), 0o755)
	os.WriteFile(filepath.Join(dir, "scripts", "balloon.py"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "style.json"), []byte(`{"radius": 9.4}`), 0o644)
	python := filepath.Join(dir, "python")
	script := `#!/bin/sh
echo "$@" > "` + filepath.Join(dir, "args") + `"
while [ $# -gt 0 ]; do
  case "$1" in
    --output-dir) out="$2";;
    --features) cp "$2" "` + filepath.Join(dir, "features.json") + `";;
  esac
  shift
done
mkdir -p "$out"
echo '{"annotations":[{"number":1,"page":1,"text":"12","source":"item:a","status":"placed_unverified","bbox":[1,2,3,4],"bubble_center":[20,20],"leader":[[15,15],[5,5]]}],"pages":[],"ballooned":1,"needs_review":0}' > "$out/drawing_review.json"
echo '{"error":"ValueError","detail":"bad input"}' >&2
exit ` + string(rune('0'+exit)) + "\n"
	if err := os.WriteFile(python, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BALLOON_SKILL_DIR", dir)
	t.Setenv("BALLOON_SKILL_PYTHON", python)
	return dir
}

func postSkill(t *testing.T, features string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, _ := form.CreateFormFile("pdf", "drawing.pdf")
	part.Write([]byte("%PDF-1.4\n%%EOF\n"))
	form.WriteField("features", features)
	form.Close()
	r := httptest.NewRequest("POST", "/api/skill", &body)
	r.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	srv().ServeHTTP(w, r)
	return w
}

func TestSkillReturnsPlacementManifest(t *testing.T) {
	for _, exit := range []int{0, 3} {
		dir := fakeSkill(t, exit)
		features := `{"features":[{"page":1,"text":"12","bbox":[1,2,3,4],"source":"item:a"}]}`
		w := postSkill(t, features)
		if w.Code != http.StatusOK {
			t.Fatalf("exit %d: got %d %s", exit, w.Code, w.Body)
		}
		var got struct {
			Annotations []map[string]any `json:"annotations"`
			Radius      float64          `json:"radius"`
		}
		json.Unmarshal(w.Body.Bytes(), &got)
		if len(got.Annotations) != 1 || got.Annotations[0]["source"] != "item:a" || got.Radius != 9.4 {
			t.Fatalf("unexpected manifest %s", w.Body)
		}
		args, _ := os.ReadFile(filepath.Join(dir, "args"))
		if !strings.Contains(string(args), "--ocr off") || !strings.Contains(string(args), "--style "+filepath.Join(dir, "style.json")) {
			t.Fatalf("unexpected skill arguments %q", args)
		}
		if sent, _ := os.ReadFile(filepath.Join(dir, "features.json")); string(sent) != features {
			t.Fatalf("features not passed through: %q", sent)
		}
	}
}

func TestSkillReportsFailure(t *testing.T) {
	fakeSkill(t, 2)
	w := postSkill(t, `{"features":[]}`)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "bad input") {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

func TestSkillRejectsInvalidFeatures(t *testing.T) {
	fakeSkill(t, 0)
	if w := postSkill(t, `{`); w.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", w.Code)
	}
}

func TestSkillMissingEnvironment(t *testing.T) {
	t.Setenv("BALLOON_SKILL_DIR", t.TempDir())
	if w := postSkill(t, ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want 503", w.Code)
	}
}
