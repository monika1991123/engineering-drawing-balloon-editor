package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// projectRoot is the checkout holding ocr/ and skill/: the working directory
// when the server runs from it, otherwise the directory above bin/balloon.
func projectRoot() string {
	if _, err := os.Stat(filepath.Join("ocr", "recognize.py")); err == nil {
		return "."
	}
	binary, _ := os.Executable()
	return filepath.Join(filepath.Dir(binary), "..")
}

// skillPaths finds the bundled engineering-drawing-balloon skill and the
// Python that runs it: the skill's own .venv if scripts/setup.sh made one,
// otherwise the project's .venv. BALLOON_SKILL_DIR and BALLOON_SKILL_PYTHON
// override either.
func skillPaths() (dir, python string) {
	root := projectRoot()
	dir = os.Getenv("BALLOON_SKILL_DIR")
	if dir == "" {
		dir = filepath.Join(root, "skill", "engineering-drawing-balloon")
	}
	dir, _ = filepath.Abs(dir)
	python = os.Getenv("BALLOON_SKILL_PYTHON")
	if python == "" {
		python = filepath.Join(dir, ".venv", "bin", "python")
		if !fileExists(python) {
			python, _ = filepath.Abs(filepath.Join(root, ".venv", "bin", "python"))
		}
	}
	return dir, python
}

// handleSkill lays out balloons with the engineering-drawing-balloon skill. The browser
// posts the original PDF plus the candidates it already knows, in the skill's
// features.json format; only the skill's placement manifest is returned. The
// skill's own ballooned PDF is discarded so the editor stays the one place
// balloons are drawn and exported.
func (s *Server) handleSkill(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		fail(w, http.StatusForbidden, "Skill 标注只接受同源请求")
		return
	}
	dir, python := skillPaths()
	script := filepath.Join(dir, "scripts", "balloon.py")
	if _, err := os.Stat(script); err != nil {
		fail(w, http.StatusServiceUnavailable, "未找到 Skill：%s", script)
		return
	}
	if _, err := os.Stat(python); err != nil {
		fail(w, http.StatusServiceUnavailable, "Skill Python 环境未安装：%s（运行 make setup）", python)
		return
	}
	select {
	case s.ocrSlot <- struct{}{}:
		defer func() { <-s.ocrSlot }()
	default:
		fail(w, http.StatusTooManyRequests, "正在处理另一个识别或标注任务，请稍后重试")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseMultipartForm(maxBody); err != nil {
		fail(w, http.StatusBadRequest, "请求无效或超过 32 MB")
		return
	}
	file, _, err := r.FormFile("pdf")
	if err != nil {
		fail(w, http.StatusBadRequest, "缺少 PDF")
		return
	}
	defer file.Close()
	features := []byte(r.FormValue("features"))
	if len(features) > 0 && !json.Valid(features) {
		fail(w, http.StatusBadRequest, "features 不是有效 JSON")
		return
	}

	work, err := os.MkdirTemp("", "balloon-skill-")
	if err != nil {
		fail(w, http.StatusInternalServerError, "无法创建临时目录")
		return
	}
	defer os.RemoveAll(work)
	// The skill refuses to write next to its input, so input and output live
	// in sibling directories.
	input := filepath.Join(work, "in", "drawing.pdf")
	output := filepath.Join(work, "out")
	if err := os.Mkdir(filepath.Dir(input), 0o700); err != nil {
		fail(w, http.StatusInternalServerError, "无法创建临时目录")
		return
	}
	if err := writeFrom(input, file); err != nil {
		fail(w, http.StatusInternalServerError, "无法保存 PDF")
		return
	}
	args := []string{filepath.Join(dir, "scripts", "balloon.py"), "--input", input, "--output-dir", output, "--ocr", "off"}
	if style := filepath.Join(dir, "style.json"); fileExists(style) {
		args = append(args, "--style", style)
	}
	if len(features) > 0 {
		path := filepath.Join(work, "features.json")
		if err := os.WriteFile(path, features, 0o600); err != nil {
			fail(w, http.StatusInternalServerError, "无法保存 features")
			return
		}
		args = append(args, "--features", path)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 300*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = io.Discard, &stderr
	err = cmd.Run()
	// Exit 3 means some features need manual placement; the manifest is still complete.
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 3) {
		if ctx.Err() != nil {
			fail(w, http.StatusGatewayTimeout, "Skill 标注超时或已取消")
			return
		}
		fail(w, http.StatusInternalServerError, "Skill 运行失败：%s", skillError(stderr.Bytes()))
		return
	}
	review, err := os.ReadFile(filepath.Join(output, "drawing_review.json"))
	if err != nil || !json.Valid(review) {
		fail(w, http.StatusInternalServerError, "Skill 未生成有效的 review.json")
		return
	}
	var manifest struct {
		Annotations []json.RawMessage `json:"annotations"`
		Pages       []json.RawMessage `json:"pages"`
		Ballooned   int               `json:"ballooned"`
		NeedsReview int               `json:"needs_review"`
	}
	if err := json.Unmarshal(review, &manifest); err != nil {
		fail(w, http.StatusInternalServerError, "Skill review.json 格式无法识别")
		return
	}
	style := map[string]any{}
	if raw, err := os.ReadFile(filepath.Join(dir, "style.json")); err == nil {
		json.Unmarshal(raw, &style)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"annotations":  manifest.Annotations,
		"pages":        manifest.Pages,
		"ballooned":    manifest.Ballooned,
		"needs_review": manifest.NeedsReview,
		"radius":       style["radius"],
	})
}

// skillError extracts the skill's {"error","detail"} line without echoing
// arbitrary output back to the browser.
func skillError(stderr []byte) string {
	lines := strings.Split(strings.TrimSpace(string(stderr)), "\n")
	var e struct{ Error, Detail string }
	if json.Unmarshal([]byte(lines[len(lines)-1]), &e) == nil && e.Error != "" {
		return strings.TrimSpace(e.Error + " " + e.Detail)
	}
	return "请检查服务器日志和 Skill 依赖"
}

func writeFrom(path string, src io.Reader) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, src); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
