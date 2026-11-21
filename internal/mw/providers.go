// Package mw 负责探测本机 MobileWork 桌面端的运行时状态：
// 内部签名代理端口，以及 providers.jsonc 里的模型入口表。
//
// 本包不持有任何凭据，也不重算签名——全部交给 MobileWork 自带的内部代理。
package mw

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// HomeDir 返回 MobileWork 运行时目录（对齐 MobileWork runtime.mjs 的路径算法）。
func HomeDir() string {
	if v := strings.TrimSpace(os.Getenv("MOBILEWORK_DIR")); v != "" {
		return v
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return ".mobilework"
	}
	return filepath.Join(h, ".mobilework")
}

// PortFilePath 返回内部签名代理端口文件路径。
func PortFilePath() string {
	if v := strings.TrimSpace(os.Getenv("MOBILEWORK_MODEL_PROXY_PORT_PATH")); v != "" {
		return v
	}
	return filepath.Join(HomeDir(), "model-proxy-port")
}

// ProvidersFilePath 返回 providers.jsonc 路径。
func ProvidersFilePath() string {
	if v := strings.TrimSpace(os.Getenv("MOBILEWORK_PROVIDERS_PATH")); v != "" {
		return v
	}
	return filepath.Join(HomeDir(), "config", "providers.jsonc")
}

// ReadPort 读取 MobileWork 内部签名代理端口。
func ReadPort() (int, error) {
	b, err := os.ReadFile(PortFilePath())
	if err != nil {
		return 0, err
	}
	txt := strings.TrimSpace(string(b))
	n, err := strconv.Atoi(txt)
	if err != nil {
		return 0, fmt.Errorf("端口文件内容不是整数: %q", txt)
	}
	if n <= 0 || n >= 65536 {
		return 0, fmt.Errorf("端口超出范围: %d", n)
	}
	return n, nil
}

var blockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)

// stripJSONC 去掉 jsonc 里的块注释与行注释。
// 行注释处理会跳过字符串内部，与 Python 版实现对齐。
func stripJSONC(text string) string {
	text = blockCommentRe.ReplaceAllString(text, "")
	var b strings.Builder
	b.Grow(len(text))
	inStr := false
	esc := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		if inStr {
			b.WriteByte(c)
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		if c == '"' {
			inStr = true
			b.WriteByte(c)
			continue
		}
		if c == '/' && i+1 < len(text) && text[i+1] == '/' {
			for i < len(text) && text[i] != '\n' {
				i++
			}
			if i < len(text) {
				b.WriteByte('\n')
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

type modelLimit struct {
	Context int `json:"context"`
	Input   int `json:"input"`
	Output  int `json:"output"`
}

type modelModalities struct {
	Input []string `json:"input"`
}

type modelDef struct {
	Name       string          `json:"name"`
	Limit      modelLimit      `json:"limit"`
	Modalities modelModalities `json:"modalities"`
}

type providerOptions struct {
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey"`
}

type providerDef struct {
	Name    string              `json:"name"`
	Options providerOptions     `json:"options"`
	Models  map[string]modelDef `json:"models"`
}

type providersFile struct {
	Provider map[string]providerDef `json:"provider"`
}

// ModelEntry 是一个「模型名 → providerId」入口。
type ModelEntry struct {
	Model      string   // 客户端请求里用的模型名，如 auto / moma/coding-auto
	ProviderID string   // 内部代理的 providerId，如 mw-auto
	Label      string   // providers.jsonc 里的展示名
	BaseURL    string   // 形如 http://127.0.0.1:60290/p/mw-auto
	Context    int      // 上下文窗口
	Input      int      // 最大输入
	Output     int      // 最大输出
	Modalities []string // 输入模态（如 text, image）
}

// Load 读取并解析 providers.jsonc。
func Load() (*providersFile, error) {
	raw, err := os.ReadFile(ProvidersFilePath())
	if err != nil {
		return nil, err
	}
	cleaned := stripJSONC(string(raw))
	var pf providersFile
	if err := json.Unmarshal([]byte(cleaned), &pf); err != nil {
		return nil, fmt.Errorf("解析 providers.jsonc 失败: %w", err)
	}
	return &pf, nil
}

// Index 返回按模型名排序的入口列表。
func Index() ([]ModelEntry, error) {
	pf, err := Load()
	if err != nil {
		return nil, err
	}
	var out []ModelEntry
	for pid, p := range pf.Provider {
		for mname, m := range p.Models {
			label := m.Name
			if label == "" {
				label = mname
			}
			out = append(out, ModelEntry{
				Model:      mname,
				ProviderID: pid,
				Label:      label,
				BaseURL:    p.Options.BaseURL,
				Context:    m.Limit.Context,
				Input:      m.Limit.Input,
				Output:     m.Limit.Output,
				Modalities: m.Modalities.Input,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out, nil
}

// UpstreamBase 返回 MobileWork 内部签名代理基址。
// 优先用端口文件，其次回退到 providers.jsonc 里的 baseURL 根。
func UpstreamBase() (string, error) {
	if p, err := ReadPort(); err == nil {
		return fmt.Sprintf("http://127.0.0.1:%d", p), nil
	}
	pf, err := Load()
	if err != nil {
		return "", err
	}
	for _, p := range pf.Provider {
		if b := p.Options.BaseURL; b != "" {
			if i := strings.Index(b, "/p/"); i > 0 {
				return b[:i], nil
			}
		}
	}
	return "", fmt.Errorf("找不到 MobileWork 内部签名代理地址（端口文件缺失，providers.jsonc 也没有 baseURL）")
}
