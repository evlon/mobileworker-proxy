package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// setupMW 造一个假的 MobileWork 运行时目录，并起一个假的上游签名代理。
func setupMW(t *testing.T, upstreamURL string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("MOBILEWORK_DIR", dir)

	// 端口文件指向假上游
	parts := strings.Split(upstreamURL, ":")
	port := parts[len(parts)-1]
	if err := os.WriteFile(filepath.Join(dir, "model-proxy-port"), []byte(port), 0o644); err != nil {
		t.Fatal(err)
	}

	cfgDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	jsonc := `{"provider":{"mw-auto":{
	  "options":{"baseURL":"` + upstreamURL + `/p/mw-auto"},
	  "models":{"auto":{"name":"自动","limit":{"context":1000,"output":100}}}
	}}}`
	if err := os.WriteFile(filepath.Join(cfgDir, "providers.jsonc"), []byte(jsonc), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHealthAndModels(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer up.Close()
	setupMW(t, up.URL)

	s := New(Options{Host: "127.0.0.1", Port: 0})
	s.Reload()
	h := s.Handler()

	// /health
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))
	if rec.Code != 200 {
		t.Fatalf("/health = %d", rec.Code)
	}
	var health map[string]any
	json.Unmarshal(rec.Body.Bytes(), &health)
	if health["ok"] != true || health["model_count"].(float64) != 1 {
		t.Errorf("/health 内容不对: %v", health)
	}

	// /v1/models
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/models", nil))
	if rec.Code != 200 {
		t.Fatalf("/v1/models = %d", rec.Code)
	}
	var list struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
	}
	json.Unmarshal(rec.Body.Bytes(), &list)
	if list.Object != "list" || len(list.Data) != 1 {
		t.Fatalf("模型列表不对: %+v", list)
	}
	if list.Data[0]["id"] != "auto" || list.Data[0]["name"] != "自动" {
		t.Errorf("模型字段不对: %v", list.Data[0])
	}
	if list.Data[0]["context_length"].(float64) != 1000 {
		t.Errorf("context_length 不对: %v", list.Data[0])
	}

	// /v1/models/auto
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/models/auto", nil))
	if rec.Code != 200 {
		t.Fatalf("/v1/models/auto = %d", rec.Code)
	}
	// /v1/models/不存在
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/v1/models/nope", nil))
	if rec.Code != 404 {
		t.Errorf("/v1/models/nope = %d，应为 404", rec.Code)
	}
}

// 转发路径必须是 /p/<providerId>/chat/completions。
func TestChatForwardsToProviderPath(t *testing.T) {
	var gotPath, gotBody string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"好"}}]}`)
	}))
	defer up.Close()
	setupMW(t, up.URL)

	s := New(Options{})
	s.Reload()
	rec := httptest.NewRecorder()
	body := `{"model":"auto","messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	h := s.Handler()
	h.ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("状态 %d, 响应 %s", rec.Code, rec.Body.String())
	}
	if gotPath != "/p/mw-auto/chat/completions" {
		t.Errorf("转发路径 = %q，期望 /p/mw-auto/chat/completions", gotPath)
	}
	if !strings.Contains(gotBody, `"model":"auto"`) {
		t.Errorf("请求体未原样转发: %s", gotBody)
	}
	if !strings.Contains(rec.Body.String(), "好") {
		t.Errorf("响应未透传: %s", rec.Body.String())
	}
}

// SSE 流式：上游分多块发，下游必须逐块透传。
func TestChatStreaming(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		f := w.(http.Flusher)
		for _, chunk := range []string{
			"data: {\"choices\":[{\"delta\":{\"content\":\"1\"}}]}\n\n",
			"data: {\"choices\":[{\"delta\":{\"content\":\"2\"}}]}\n\n",
			"data: [DONE]\n\n",
		} {
			io.WriteString(w, chunk)
			f.Flush()
		}
	}))
	defer up.Close()
	setupMW(t, up.URL)

	s := New(Options{})
	s.Reload()
	rec := httptest.NewRecorder()
	body := `{"model":"auto","stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != 200 {
		t.Fatalf("状态 %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/event-stream") {
		t.Errorf("Content-Type = %q", ct)
	}
	out := rec.Body.String()
	for _, want := range []string{`"content":"1"`, `"content":"2"`, "[DONE]"} {
		if !strings.Contains(out, want) {
			t.Errorf("流式输出缺 %q:\n%s", want, out)
		}
	}
}

// 未知模型 → 404，且列出可用模型。
func TestChatUnknownModel(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	setupMW(t, up.URL)

	s := New(Options{})
	s.Reload()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4","messages":[]}`))
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != 404 {
		t.Fatalf("应为 404，实际 %d", rec.Code)
	}
	var e struct {
		Error     string   `json:"error"`
		Available []string `json:"available"`
	}
	json.Unmarshal(rec.Body.Bytes(), &e)
	if !strings.Contains(e.Error, "gpt-4") {
		t.Errorf("错误信息应提到模型名: %s", e.Error)
	}
	if len(e.Available) != 1 || e.Available[0] != "auto" {
		t.Errorf("应列出可用模型: %v", e.Available)
	}
}

// 非法 JSON → 400。
func TestChatBadJSON(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	setupMW(t, up.URL)

	s := New(Options{})
	s.Reload()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{bad"))
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != 400 {
		t.Errorf("应为 400，实际 %d", rec.Code)
	}
}

// api-key 鉴权。
func TestAPIKey(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	setupMW(t, up.URL)

	s := New(Options{APIKey: "sk-test"})
	s.Reload()
	h := s.Handler()

	check := func(name string, hdr map[string]string, want int) {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/v1/models", nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		h.ServeHTTP(rec, req)
		if rec.Code != want {
			t.Errorf("%s: 状态 %d，期望 %d", name, rec.Code, want)
		}
	}
	check("无 key", nil, 401)
	check("错 key", map[string]string{"Authorization": "Bearer wrong"}, 401)
	check("对 key", map[string]string{"Authorization": "Bearer sk-test"}, 200)
	check("x-api-key", map[string]string{"X-Api-Key": "sk-test"}, 200)

	// /health 不需要 key
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))
	if rec.Code != 200 {
		t.Errorf("/health 应免鉴权，实际 %d", rec.Code)
	}
}

// 上游不可达 → 502。
func TestUpstreamDown(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	upURL := up.URL
	up.Close() // 立刻关掉
	setupMW(t, upURL)

	s := New(Options{})
	s.Reload()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"auto","messages":[]}`))
	done := make(chan struct{})
	go func() {
		s.Handler().ServeHTTP(rec, req)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("上游不可达时卡住了")
	}
	if rec.Code != 502 {
		t.Errorf("应为 502，实际 %d", rec.Code)
	}
}

// model 名大小写不敏感查找（在入口层面）。
func TestModelLookupCaseInsensitive(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"ok":true}`)
	}))
	defer up.Close()
	setupMW(t, up.URL)

	s := New(Options{})
	s.Reload()
	if _, ok := s.lookup("AUTO"); !ok {
		t.Error("查找应当大小写不敏感")
	}
	if _, ok := s.lookup("nope"); ok {
		t.Error("不存在的模型不应命中")
	}
}
