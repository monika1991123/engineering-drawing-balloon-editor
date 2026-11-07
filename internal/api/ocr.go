package api

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Only the local editor can initiate CPU-intensive image processing. The
// service remains bound to loopback and is accessed through the SSH tunnel.
func (s *Server) handleOCR(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		fail(w, http.StatusForbidden, "OCR 只接受同源请求")
		return
	}
	if r.Header.Get("Content-Type") != "image/png" {
		fail(w, http.StatusUnsupportedMediaType, "请提交 PNG 页面图像")
		return
	}
	select {
	case s.ocrSlot <- struct{}{}:
		defer func() { <-s.ocrSlot }()
	default:
		fail(w, http.StatusTooManyRequests, "OCR 正在处理另一页，请稍后重试")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		fail(w, http.StatusRequestEntityTooLarge, "图像超过 16 MB 限制")
		return
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width < 32 || cfg.Height < 32 || int64(cfg.Width)*int64(cfg.Height) > 16_000_000 {
		fail(w, http.StatusBadRequest, "PNG 无效，或超过 1600 万像素限制")
		return
	}
	root := projectRoot()
	python, _ := filepath.Abs(filepath.Join(root, ".venv", "bin", "python"))
	script, _ := filepath.Abs(filepath.Join(root, "ocr", "recognize.py"))
	if _, err := os.Stat(python); err != nil {
		fail(w, http.StatusServiceUnavailable, "本地 OCR 环境未安装，请运行 make setup")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 180*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, script)
	cmd.Stdin = bytes.NewReader(data)
	cmd.Env = append(os.Environ(), "OMP_NUM_THREADS=1", "OPENBLAS_NUM_THREADS=1", "HF_HUB_OFFLINE=1")
	var output, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			fail(w, http.StatusGatewayTimeout, "本页 OCR 超时或已取消，请减少页面分辨率后重试")
			return
		}
		// No image or recognized drawing text is written to application logs.
		fail(w, http.StatusInternalServerError, "本地 OCR 运行失败，请检查模型依赖和服务器内存")
		return
	}
	if output.Len() > 4<<20 || !json.Valid(output.Bytes()) {
		fail(w, http.StatusInternalServerError, "OCR 返回结果无效")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(output.Bytes())
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}
