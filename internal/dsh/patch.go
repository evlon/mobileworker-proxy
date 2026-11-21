package dsh

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	// LLMPiAiEntryID 是 cordis.patch.yml 里模型适配器那一行的 id。
	LLMPiAiEntryID = "llm-pi-ai"
	// LLMPiAiEntryName 是它的包名，id 缺失时按包名兜底匹配。
	LLMPiAiEntryName = "@deepseek-ai/dsh-llm-pi-ai"
)

// RouteModel 是写入 cordis.patch.yml 的一个模型条目。
type RouteModel struct {
	ID            string
	Name          string
	ContextWindow int
	MaxTokens     int
	Input         []string
}

// Route 是写入 cordis.patch.yml 的一个 provider 路由。
type Route struct {
	ProviderID string
	APIKeyEnv  string
	API        string
	BaseURL    string
	Models     []RouteModel
}

// Result 描述一次安装的结果。
type Result struct {
	Changed    bool
	BackupPath string
	PatchPath  string
	Summary    string
}

// ---------------------------------------------------------------- YAML 工具

func scalarStr(s string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
}

func scalarInt(i int) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.Itoa(i)}
}

// mapGet 在映射节点里按键取值；返回键节点与值节点。
func mapGet(m *yaml.Node, key string) (*yaml.Node, *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil, nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i], m.Content[i+1]
		}
	}
	return nil, nil
}

// mapSet 在映射节点里就地写一个键；已存在则原地替换值（保留位置与注释）。
func mapSet(m *yaml.Node, key string, val *yaml.Node) {
	if k, v := mapGet(m, key); k != nil {
		*v = *val
		return
	}
	m.Content = append(m.Content, scalarStr(key), val)
}

func seqFindByID(s *yaml.Node, id string) *yaml.Node {
	if s == nil || s.Kind != yaml.SequenceNode {
		return nil
	}
	for _, it := range s.Content {
		if _, v := mapGet(it, "id"); v != nil && v.Value == id {
			return it
		}
	}
	return nil
}

func mappingIndent(m *yaml.Node) int {
	if m == nil || len(m.Content) == 0 {
		return 0
	}
	return m.Content[0].Column - 1
}

func firstLine(n *yaml.Node) int {
	if n == nil {
		return 0
	}
	return n.Line
}

// nodeEndLine 返回一个节点子树所占的最后一行（1-based）。
// 用于精确切出「providers 这一段到底到哪结束」，避免把后面
// 别的工具维护的注释一起吞掉。
func nodeEndLine(n *yaml.Node) int {
	if n == nil {
		return 0
	}
	end := n.Line
	// 多行标量（块标量）自身占多行
	if n.Kind == yaml.ScalarNode {
		end += strings.Count(n.Value, "\n")
	}
	for _, c := range n.Content {
		if e := nodeEndLine(c); e > end {
			end = e
		}
	}
	return end
}

func marshalNode(n *yaml.Node) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	// 必须显式设 2：yaml.v3 默认缩进是 4，会把整个文件格式改掉。
	enc.SetIndent(2)
	if err := enc.Encode(n); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

// ---------------------------------------------------------------- 节点定位

type entryLoc struct {
	Seq        *yaml.Node // 根序列
	Entry      *yaml.Node // llm-pi-ai 那条映射
	EntryIndex int
	ConfigKey  *yaml.Node
	Config     *yaml.Node
	ProvKey    *yaml.Node
	Providers  *yaml.Node
}

func findEntry(doc *yaml.Node) *entryLoc {
	if doc == nil || len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root.Kind != yaml.SequenceNode {
		return nil
	}
	for i, item := range root.Content {
		if item.Kind != yaml.MappingNode {
			continue
		}
		_, idv := mapGet(item, "id")
		_, namev := mapGet(item, "name")
		if (idv != nil && idv.Value == LLMPiAiEntryID) ||
			(namev != nil && namev.Value == LLMPiAiEntryName) {
			l := &entryLoc{Seq: root, Entry: item, EntryIndex: i}
			l.ConfigKey, l.Config = mapGet(item, "config")
			if l.Config != nil {
				l.ProvKey, l.Providers = mapGet(l.Config, "providers")
			}
			return l
		}
	}
	return nil
}

// ---------------------------------------------------------------- 路由合并

func modelNodeOf(m RouteModel) *yaml.Node {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	updateModelNode(n, m)
	return n
}

func updateModelNode(n *yaml.Node, m RouteModel) {
	mapSet(n, "id", scalarStr(m.ID))
	if m.Name != "" {
		mapSet(n, "name", scalarStr(m.Name))
	}
	if m.ContextWindow > 0 {
		mapSet(n, "contextWindow", scalarInt(m.ContextWindow))
	}
	if m.MaxTokens > 0 {
		mapSet(n, "maxTokens", scalarInt(m.MaxTokens))
	}
	if len(m.Input) > 0 {
		in := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, s := range m.Input {
			in.Content = append(in.Content, scalarStr(s))
		}
		mapSet(n, "input", in)
	}
}

// mergeRoute 把一条路由合并进 providers 映射（就地修改，保留其它路由）。
func mergeRoute(providers *yaml.Node, r Route) *yaml.Node {
	if providers == nil || providers.Kind != yaml.MappingNode {
		providers = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	_, rv := mapGet(providers, r.ProviderID)
	if rv == nil || rv.Kind != yaml.MappingNode {
		rv = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		providers.Content = append(providers.Content, scalarStr(r.ProviderID), rv)
	}
	if r.APIKeyEnv != "" {
		mapSet(rv, "apiKeyEnv", scalarStr(r.APIKeyEnv))
	}
	if r.API != "" {
		mapSet(rv, "api", scalarStr(r.API))
	}
	if r.BaseURL != "" {
		mapSet(rv, "baseURL", scalarStr(r.BaseURL))
	}

	_, mv := mapGet(rv, "models")
	if mv == nil || mv.Kind != yaml.SequenceNode {
		mv = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		mapSet(rv, "models", mv)
	}
	for _, m := range r.Models {
		if ex := seqFindByID(mv, m.ID); ex != nil {
			updateModelNode(ex, m)
		} else {
			mv.Content = append(mv.Content, modelNodeOf(m))
		}
	}
	return providers
}

// ---------------------------------------------------------------- 文本渲染

// forceBlockStyle 递归把节点树里的流式风格（{} / []）清掉。
//
// 原文件里若写的是 `providers: {}` 或 `providers: []`，那个节点会带
// FlowStyle；合并数据后直接序列化就会输出成挤在一行的流式写法，
// 与 DSH 其它配置的块风格不一致、也不便人读。
func forceBlockStyle(n *yaml.Node) {
	if n == nil {
		return
	}
	switch n.Kind {
	case yaml.MappingNode, yaml.SequenceNode:
		n.Style &^= yaml.FlowStyle
	}
	for _, c := range n.Content {
		forceBlockStyle(c)
	}
}

// renderProvidersBlock 生成 "providers:" 那一段的文本。
// indent 是 providers 子键（即路由名）应有的列缩进。
func renderProvidersBlock(providers *yaml.Node, indent int) (string, error) {
	if indent < 2 {
		indent = 2
	}
	forceBlockStyle(providers)
	if providers == nil || len(providers.Content) == 0 {
		return strings.Repeat(" ", indent-2) + "providers: {}", nil
	}
	body, err := marshalNode(providers)
	if err != nil {
		return "", err
	}
	pad := strings.Repeat(" ", indent)
	var out strings.Builder
	out.WriteString(strings.Repeat(" ", indent-2) + "providers:\n")
	for _, ln := range strings.Split(body, "\n") {
		if strings.TrimSpace(ln) == "" {
			out.WriteString("\n")
			continue
		}
		out.WriteString(pad + ln + "\n")
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// renderConfigBlock 生成 "config:" + "providers:" 两层的文本。
func renderConfigBlock(providers *yaml.Node, entryIndent int) (string, error) {
	if entryIndent < 0 {
		entryIndent = 0
	}
	forceBlockStyle(providers)
	cfg := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapSet(cfg, "providers", providers)
	body, err := marshalNode(cfg)
	if err != nil {
		return "", err
	}
	pad := strings.Repeat(" ", entryIndent+2)
	var out strings.Builder
	out.WriteString(strings.Repeat(" ", entryIndent) + "config:\n")
	for _, ln := range strings.Split(body, "\n") {
		if strings.TrimSpace(ln) == "" {
			out.WriteString("\n")
			continue
		}
		out.WriteString(pad + ln + "\n")
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// renderEntry 生成整条 "- id: llm-pi-ai …" 条目文本。
func renderEntry(providers *yaml.Node) (string, error) {
	forceBlockStyle(providers)
	entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapSet(entry, "id", scalarStr(LLMPiAiEntryID))
	mapSet(entry, "name", scalarStr(LLMPiAiEntryName))
	cfg := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapSet(cfg, "providers", providers)
	mapSet(entry, "config", cfg)

	body, err := marshalNode(entry)
	if err != nil {
		return "", err
	}
	lines := strings.Split(body, "\n")
	var out strings.Builder
	out.WriteString("- id: " + LLMPiAiEntryID + "\n")
	for _, ln := range lines[1:] {
		if strings.TrimSpace(ln) == "" {
			out.WriteString("\n")
			continue
		}
		out.WriteString("  " + ln + "\n")
	}
	return strings.TrimRight(out.String(), "\n"), nil
}

// ---------------------------------------------------------------- 安装

// RoutesEqual 判断两条路由是否等价（用于幂等预检）。
func RoutesEqual(a, b *Route) bool {
	if a == nil || b == nil {
		return false
	}
	if a.ProviderID != b.ProviderID || a.APIKeyEnv != b.APIKeyEnv ||
		a.API != b.API || a.BaseURL != b.BaseURL {
		return false
	}
	if len(a.Models) != len(b.Models) {
		return false
	}
	byID := make(map[string]RouteModel, len(a.Models))
	for _, m := range a.Models {
		byID[m.ID] = m
	}
	for _, m := range b.Models {
		got, ok := byID[m.ID]
		if !ok {
			return false
		}
		if got.Name != m.Name || got.ContextWindow != m.ContextWindow || got.MaxTokens != m.MaxTokens {
			return false
		}
	}
	return true
}

// flowEmptySeqLine 判断根节点是否为「流式空序列」（文件内容就是个 `[]`）。
// 是则返回那一行的下标（0-based），否则 -1。
//
// 这种文件不能直接追加块序列项 —— `[]` 后面再跟 `- id: …` 是非法 YAML，
// 必须把那一行替换掉。
func flowEmptySeqLine(doc *yaml.Node, lines []string) int {
	if doc == nil || len(doc.Content) == 0 {
		return -1
	}
	root := doc.Content[0]
	if root.Kind != yaml.SequenceNode || len(root.Content) != 0 {
		return -1
	}
	if root.Line >= 1 && root.Line <= len(lines) &&
		strings.TrimSpace(lines[root.Line-1]) == "[]" {
		return root.Line - 1
	}
	for i, l := range lines {
		if strings.TrimSpace(l) == "[]" {
			return i
		}
	}
	return -1
}

// ReadRoute 只读地取出当前已安装的路由（供展示/幂等判断）。
func ReadRoute(patchPath, providerID string) (*Route, error) {
	raw, err := os.ReadFile(patchPath)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("解析 %s 失败: %w", patchPath, err)
	}
	l := findEntry(&doc)
	if l == nil || l.Providers == nil {
		return nil, nil
	}
	_, rv := mapGet(l.Providers, providerID)
	if rv == nil {
		return nil, nil
	}
	r := &Route{ProviderID: providerID}
	if _, v := mapGet(rv, "apiKeyEnv"); v != nil {
		r.APIKeyEnv = v.Value
	}
	if _, v := mapGet(rv, "api"); v != nil {
		r.API = v.Value
	}
	if _, v := mapGet(rv, "baseURL"); v != nil {
		r.BaseURL = v.Value
	}
	if _, mv := mapGet(rv, "models"); mv != nil && mv.Kind == yaml.SequenceNode {
		for _, it := range mv.Content {
			m := RouteModel{}
			if _, v := mapGet(it, "id"); v != nil {
				m.ID = v.Value
			}
			if _, v := mapGet(it, "name"); v != nil {
				m.Name = v.Value
			}
			if _, v := mapGet(it, "contextWindow"); v != nil {
				m.ContextWindow, _ = strconv.Atoi(v.Value)
			}
			if _, v := mapGet(it, "maxTokens"); v != nil {
				m.MaxTokens, _ = strconv.Atoi(v.Value)
			}
			r.Models = append(r.Models, m)
		}
	}
	return r, nil
}

// Install 把一条路由合并进 cordis.patch.yml。
//
// 策略是「文本外科手术」：只重写 llm-pi-ai 条目里的 providers 段落，
// 文件其余字节（包括别的工具维护的区块）原样不动。
func Install(patchPath string, r Route) (Result, error) {
	res := Result{PatchPath: patchPath}

	raw, err := os.ReadFile(patchPath)
	if err != nil {
		return res, err
	}

	eol := "\n"
	if bytes.Contains(raw, []byte("\r\n")) {
		eol = "\r\n"
	}
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	hadFinalNewline := strings.HasSuffix(text, "\n")
	lines := strings.Split(text, "\n")
	if hadFinalNewline {
		lines = lines[:len(lines)-1]
	}

	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return res, fmt.Errorf("解析 %s 失败: %w", patchPath, err)
	}

	l := findEntry(&doc)
	var newLines []string

	switch {
	// --- 情况 A：文件里根本没有 llm-pi-ai 条目 → 追加整条 ---
	case l == nil:
		providers := mergeRoute(nil, r)
		block, err := renderEntry(providers)
		if err != nil {
			return res, err
		}
		blockLines := strings.Split(block, "\n")

		// 空序列 `[]` 必须被替换掉，否则追加后是非法 YAML
		if emptyIdx := flowEmptySeqLine(&doc, lines); emptyIdx >= 0 {
			newLines = splice(lines, emptyIdx, emptyIdx+1, blockLines, nil)
			break
		}

		newLines = append([]string{}, lines...)
		if len(newLines) > 0 && strings.TrimSpace(newLines[len(newLines)-1]) != "" {
			newLines = append(newLines, "")
		}
		newLines = append(newLines, blockLines...)

	// --- 情况 B：条目在，但没有 config（或 config 下没有 providers）→ 插入 ---
	case l.Config == nil:
		providers := mergeRoute(nil, r)
		entryIndent := mappingIndent(l.Entry)
		block, err := renderConfigBlock(providers, entryIndent)
		if err != nil {
			return res, err
		}
		at, trimmed := entryEnd(lines, l)
		newLines = splice(lines, at, at, strings.Split(block, "\n"), trimmed)

	case l.Providers == nil:
		providers := mergeRoute(nil, r)
		indent := l.ConfigKey.Column - 1 + 2
		block, err := renderProvidersBlock(providers, indent)
		if err != nil {
			return res, err
		}
		// 插在 "config:" 那一行之后
		at := l.ConfigKey.Line
		if at > len(lines) {
			at = len(lines)
		}
		newLines = splice(lines, at, at, strings.Split(block, "\n"), nil)

	// --- 情况 C：providers 段落已存在 → 原地替换该段 ---
	default:
		startIdx := l.ProvKey.Line - 1
		// 用子树实际占用的最后一行来定界，而不是「下一个条目的上一行」——
		// 后者会把 providers 之后、下一条目之前属于别人的注释一起吞掉。
		endIdx := nodeEndLine(l.Providers)
		if endIdx <= 0 {
			endIdx = startIdx + 1
		}
		if startIdx < 0 {
			startIdx = 0
		}
		if endIdx > len(lines) {
			endIdx = len(lines)
		}
		if endIdx < startIdx {
			endIdx = startIdx
		}

		// 保留段尾的空行，别把条目之间的分隔吃掉
		var tail []string
		for endIdx > startIdx && strings.TrimSpace(lines[endIdx-1]) == "" {
			tail = append([]string{lines[endIdx-1]}, tail...)
			endIdx--
		}

		indent := l.ProvKey.Column - 1 + 2
		block, err := renderProvidersBlock(mergeRoute(l.Providers, r), indent)
		if err != nil {
			return res, err
		}
		newLines = splice(lines, startIdx, endIdx, strings.Split(block, "\n"), tail)
	}

	out := strings.Join(newLines, "\n")
	if hadFinalNewline {
		out += "\n"
	}
	if eol != "\n" {
		out = strings.ReplaceAll(out, "\n", eol)
	}

	if out == string(raw) {
		res.Changed = false
		res.Summary = "配置已是目标状态，未改动文件"
		return res, nil
	}

	// 备份
	backup := fmt.Sprintf("%s.bak-%s-mwproxy", patchPath, time.Now().Format("20060102-150405"))
	if err := os.WriteFile(backup, raw, 0o644); err != nil {
		return res, fmt.Errorf("写备份失败: %w", err)
	}
	res.BackupPath = backup

	// 原子替换
	tmp := patchPath + ".mwproxy.tmp"
	if err := os.WriteFile(tmp, []byte(out), 0o644); err != nil {
		return res, fmt.Errorf("写临时文件失败: %w", err)
	}
	if err := os.Rename(tmp, patchPath); err != nil {
		os.Remove(tmp)
		return res, fmt.Errorf("替换文件失败: %w", err)
	}

	res.Changed = true
	var ids []string
	for _, m := range r.Models {
		ids = append(ids, m.ID)
	}
	res.Summary = fmt.Sprintf("已写入路由 %s（%d 个模型: %s）", r.ProviderID, len(r.Models), strings.Join(ids, ", "))
	return res, nil
}

// entryEnd 返回某条目应当插入新键的位置（0-based 下标）与段尾空行。
func entryEnd(lines []string, l *entryLoc) (int, []string) {
	endIdx := len(lines)
	if l.EntryIndex+1 < len(l.Seq.Content) {
		endIdx = firstLine(l.Seq.Content[l.EntryIndex+1]) - 1
	}
	if endIdx > len(lines) {
		endIdx = len(lines)
	}
	var tail []string
	for endIdx > 0 && strings.TrimSpace(lines[endIdx-1]) == "" {
		tail = append([]string{lines[endIdx-1]}, tail...)
		endIdx--
	}
	return endIdx, tail
}

// splice 用 block 替换 lines[start:end]，再拼回 tail。
func splice(lines []string, start, end int, block []string, tail []string) []string {
	out := make([]string, 0, len(lines)+len(block)+len(tail))
	out = append(out, lines[:start]...)
	out = append(out, block...)
	out = append(out, tail...)
	out = append(out, lines[end:]...)
	return out
}
