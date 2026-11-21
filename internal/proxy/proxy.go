// Package proxy 实现 OpenAI 兼容的本地代理。
//
// 它只做两件事：把请求里的模型名映射成 MobileWork 的 providerId，
// 再把 /v1/* 透传到内部签名代理的 /p/<providerId>/*。
// 不持有凭据，也不重算签名。
package proxy

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"mobilework-proxy/internal/mw"
)

// Options 是代理的运行参数。
type Options struct {
	Host           string
	Port           int
	APIKey         string
	ReloadInterval time.Duration
}

// Server 是代理服务。
type Server struct {
	opt Options

	mu      sync.RWMutex
	index   map[string]mw.ModelEntry // 模型名 -> 入口
	loaded  time.Time
	loadErr error
}

// New 创建一个代理服务。
func New(opt Options) *Server {
	return &Server{opt: opt, index: map[string]mw.ModelEntry{}}
}

// Reload 重新读取 providers.jsonc，重建「模型名 → providerId」映射。
func (s *Server) Reload() {
	entries, err := mw.Index()
	if err != nil {
		s.mu.Lock()
		s.loadErr = err
		s.mu.Unlock()
		log.Printf("[warn] 读取 providers.jsonc 失败: %v", err)
		return
	}
	idx := make(map[string]mw.ModelEntry, len(entries))
	for _, e := range entries {
		idx[e.Model] = e
	}
	s.mu.Lock()
	s.index = idx
	s.loaded = time.Now()
	s.loadErr = nil
	s.mu.Unlock()

	names := make([]string, 0, len(idx))
	for k := range idx {
		names = append(names, k)
	}
	sort.Strings(names)
	log.Printf("[info] 已加载 %d 个模型入口: %s", len(idx), strings.Join(names, ", "))
}

// Count 返回已加载的模型数。
func (s *Server) Count() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.index)
}

func (s *Server) lookup(model string) (mw.ModelEntry, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if e, ok := s.index[model]; ok {
		return e, true
	}
	for k, e := range s.index {
		if strings.EqualFold(k, model) {
			return e, true
		}
	}
	return mw.ModelEntry{}, false
}

func (s *Server) allModels() []mw.ModelEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]mw.ModelEntry, 0, len(s.index))
	for _, e := range s.index {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}

// ---------------------------------------------------------------- 鉴权

func (s *Server) authorized(r *http.Request) bool {
	if s.opt.APIKey == "" {
		return true
	}
	auth := r.Header.Get("Authorization")
	if len(auth) >= 7 && strings.EqualFold(auth[:7], "bearer ") {
		if strings.TrimSpace(auth[7:]) == s.opt.APIKey {
			return true
		}
	}
	return r.Header.Get("X-Api-Key") == s.opt.APIKey
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

// ---------------------------------------------------------------- 路由

// Handler 返回代理的 http.Handler。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/health", s.handleHealth)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/v1/models/", s.handleModelByID)
	mux.HandleFunc("/v1/chat/completions", s.handleChat)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	upstream, err := mw.UpstreamBase()
	providers := map[string]bool{}
	for _, e := range s.allModels() {
		providers[e.ProviderID] = true
	}
	pl := make([]string, 0, len(providers))
	for k := range providers {
		pl = append(pl, k)
	}
	sort.Strings(pl)

	resp := map[string]any{
		"ok":          true,
		"providers":   pl,
		"model_count": len(s.allModels()),
	}
	if err != nil {
		resp["ok"] = false
		resp["upstream_error"] = err.Error()
	} else {
		resp["upstream"] = upstream
	}
	writeJSON(w, http.StatusOK, resp)
}

func modelJSON(e mw.ModelEntry) map[string]any {
	ctx := e.Context
	if ctx == 0 {
		ctx = 0
	}
	return map[string]any{
		"id":                 e.Model,
		"object":             "model",
		"created":            time.Now().Unix(),
		"owned_by":           "mobilework",
		"name":               e.Label,
		"context_length":     e.Context,
		"max_input_tokens":   e.Input,
		"max_output_tokens":  e.Output,
		"max_model_len":      e.Context,
		"max_context_length": e.Context,
		"vendor":             "mobilework",
		"supportsToolCall":   true,
		"supportsImages":     true,
	}
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid api key")
		return
	}
	list := s.allModels()
	data := make([]map[string]any, 0, len(list))
	for _, e := range list {
		data = append(data, modelJSON(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) handleModelByID(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid api key")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/models/")
	if id == "" {
		writeErr(w, http.StatusNotFound, "model not found")
		return
	}
	e, ok := s.lookup(id)
	if !ok {
		writeErr(w, http.StatusNotFound, fmt.Sprintf("model '%s' not found", id))
		return
	}
	writeJSON(w, http.StatusOK, modelJSON(e))
}

// ---------------------------------------------------------------- 对话转发

var forwardedHeaders = []string{
	"x-session-id", "x-session-affinity", "x-parent-session-id", "x-msg-id",
	"x-mw-context-agent", "x-mw-context-mcp",
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorized(r) {
		writeErr(w, http.StatusUnauthorized, "invalid api key")
		return
	}

	upstream, err := mw.UpstreamBase()
	if err != nil {
		writeErr(w, http.StatusBadGateway,
			"找不到 MobileWork 内部签名代理端口，请确认 MobileWork 桌面端已启动登录")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "读取请求体失败")
		return
	}
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	modelName, _ := parsed["model"].(string)
	entry, ok := s.lookup(modelName)
	if !ok {
		names := []string{}
		for _, e := range s.allModels() {
			names = append(names, e.Model)
		}
		writeJSON(w, http.StatusNotFound, map[string]any{
			"error":     fmt.Sprintf("未知模型 '%s'，可用模型见 /v1/models", modelName),
			"available": names,
		})
		return
	}

	target := fmt.Sprintf("%s/p/%s/chat/completions", upstream, entry.ProviderID)
	stream, _ := parsed["stream"].(bool)

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target, strings.NewReader(string(body)))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "构造上游请求失败")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	accept := r.Header.Get("Accept")
	if accept == "" {
		if stream {
			accept = "text/event-stream"
		} else {
			accept = "application/json"
		}
	}
	req.Header.Set("Accept", accept)
	for _, h := range forwardedHeaders {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}

	client := &http.Client{Timeout: 0} // 流式不能设总超时
	if !stream {
		client.Timeout = 600 * time.Second
	}

	resp, err := client.Do(req)
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("转发失败: %v", err))
		return
	}
	defer resp.Body.Close()

	if stream {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(resp.StatusCode)
		flusher, _ := w.(http.Flusher)
		buf := make([]byte, 16*1024)
		for {
			n, rerr := resp.Body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if rerr != nil {
				return
			}
		}
	}

	// 非流式
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("读取上游响应失败: %v", err))
		return
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/json; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(raw)
}

// ---------------------------------------------------------------- 运行

// Run 启动代理；reloadInterval > 0 时后台定时重读 providers.jsonc。
func (s *Server) Run() error {
	s.Reload()
	if s.opt.ReloadInterval > 0 {
		go func() {
			t := time.NewTicker(s.opt.ReloadInterval)
			defer t.Stop()
			for range t.C {
				s.Reload()
			}
		}()
	}
	addr := fmt.Sprintf("%s:%d", s.opt.Host, s.opt.Port)
	srv := &http.Server{Addr: addr, Handler: s.Handler()}
	log.Printf("[info] MobileWork OpenAI 代理监听 http://%s", addr)
	log.Printf("[info] 使用方式: base_url=http://%s/v1 , 模型名见 /v1/models", addr)
	if s.opt.APIKey == "" {
		log.Printf("[info] 未启用 api-key（客户端留空即可）")
	}
	return srv.ListenAndServe()
}
