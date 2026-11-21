package dsh

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func samplePatch() string {
	return `# Your patch layer for this dsh profile
# 这是头部说明注释

- id: agent-default-model
  name: "@deepseek-ai/dsh-agent-default-model"
  config:
    provider: mobilework   # 行尾注释
    model: dsv4.1-flash

# >>> 别的工具维护的区块：begin
- id: matrix
  name: dsh-matrix-agent
  config:
    homeserverUrl: 'https://im.example'
    accessToken: 'SECRET-TOKEN'
# <<< 别的工具维护的区块：end

- id: llm-pi-ai
  name: "@deepseek-ai/dsh-llm-pi-ai"
  config:
    providers:
      existed:
        apiKeyEnv: EXISTED_KEY
        api: openai-completions
        baseURL: http://localhost:1111/v1
        models:
          - id: keep-me
            name: 保留我
            contextWindow: 1000
            maxTokens: 100

- id: plugin-manager
  name: "@deepseek-ai/dsh-plugin-manager"
  disabled: !!js "!ctx.get('profileContext')"
`
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cordis.patch.yml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func mustParse(t *testing.T, path string) *yaml.Node {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("产出的 YAML 无法解析: %v", err)
	}
	return &doc
}

func testRoute() Route {
	return Route{
		ProviderID: "mobilework",
		APIKeyEnv:  "MOBILEWORK_API_KEY",
		API:        "openai-completions",
		BaseURL:    "http://localhost:8989/v1",
		Models: []RouteModel{
			{ID: "auto", Name: "自动", ContextWindow: 196608, MaxTokens: 32768, Input: []string{"text"}},
			{ID: "dsv4.1-flash", Name: "dsv4.1-flash", ContextWindow: 389120, MaxTokens: 65536, Input: []string{"text", "image"}},
		},
	}
}

// 安装后：目标路由写入正确，其它条目与注释不受影响。
func TestInstallPreservesEverythingElse(t *testing.T) {
	src := samplePatch()
	p := writeTemp(t, src)

	res, err := Install(p, testRoute())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Fatal("首次安装应当改动文件")
	}
	if res.BackupPath == "" {
		t.Fatal("应当产生备份")
	}
	if _, err := os.Stat(res.BackupPath); err != nil {
		t.Fatalf("备份文件不存在: %v", err)
	}
	// 备份内容必须与原文件逐字节一致
	backupRaw, _ := os.ReadFile(res.BackupPath)
	if string(backupRaw) != src {
		t.Fatal("备份内容与原文件不一致")
	}

	out, _ := os.ReadFile(p)
	got := string(out)

	// 1) 其它条目必须原样保留
	for _, want := range []string{
		"- id: matrix",
		"SECRET-TOKEN",
		"- id: plugin-manager",
		`disabled: !!js "!ctx.get('profileContext')"`,
		"- id: agent-default-model",
		"# >>> 别的工具维护的区块：begin",
		"# <<< 别的工具维护的区块：end",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("丢失内容: %q", want)
		}
	}

	// 2) 既有路由必须保留
	if !strings.Contains(got, "existed:") || !strings.Contains(got, "keep-me") {
		t.Error("既有路由 existed / keep-me 被破坏")
	}

	// 3) 新路由写入
	if !strings.Contains(got, "mobilework:") {
		t.Error("新路由未写入")
	}
	if !strings.Contains(got, "MOBILEWORK_API_KEY") {
		t.Error("apiKeyEnv 未写入")
	}
	if !strings.Contains(got, "http://localhost:8989/v1") {
		t.Error("baseURL 未写入")
	}
	for _, id := range []string{"auto", "dsv4.1-flash"} {
		if !strings.Contains(got, "id: "+id) {
			t.Errorf("模型 %s 未写入", id)
		}
	}

	// 4) 注释一条不丢
	before := countComments(src)
	after := countComments(got)
	if before != after {
		t.Errorf("注释行数变化: %d -> %d", before, after)
	}

	// 5) 产出必须是合法 YAML，且 DSH 能读到我们的路由
	doc := mustParse(t, p)
	l := findEntry(doc)
	if l == nil || l.Providers == nil {
		t.Fatal("产出的 YAML 里找不到 llm-pi-ai.providers")
	}
	if _, v := mapGet(l.Providers, "mobilework"); v == nil {
		t.Fatal("产出的 YAML 里没有 mobilework 路由")
	}
	if _, v := mapGet(l.Providers, "existed"); v == nil {
		t.Fatal("产出的 YAML 里丢了 existed 路由")
	}
}

// 回归：空映射 `providers: {}` 合并后必须输出块风格，不能挤成一行。
func TestInstallFromFlowStyleEmptyMap(t *testing.T) {
	src := `# 注释要留着

- id: llm-pi-ai
  name: "@deepseek-ai/dsh-llm-pi-ai"
  config:
    providers: {}

- id: plugin-manager
  name: "@deepseek-ai/dsh-plugin-manager"
`
	p := writeTemp(t, src)
	if _, err := Install(p, testRoute()); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(p)
	got := string(out)

	// 不能出现流式挤行
	if strings.Contains(got, "{mobilework:") || strings.Contains(got, "models: [{") {
		t.Errorf("输出成了流式写法:\n%s", got)
	}
	// 必须是块风格
	for _, want := range []string{"providers:\n", "mobilework:\n", "models:\n", "- id: auto"} {
		if !strings.Contains(got, want) {
			t.Errorf("缺少块风格片段 %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "# 注释要留着") {
		t.Error("注释丢了")
	}
	if !strings.Contains(got, "plugin-manager") {
		t.Error("后续条目丢了")
	}
	// 仍然合法
	mustParse(t, p)
}

// 幂等：装两次，第二次不应再改动。
func TestInstallIdempotent(t *testing.T) {
	p := writeTemp(t, samplePatch())

	first, err := Install(p, testRoute())
	if err != nil {
		t.Fatal(err)
	}
	if !first.Changed {
		t.Fatal("第一次应当改动")
	}
	after1, _ := os.ReadFile(p)

	second, err := Install(p, testRoute())
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed {
		t.Error("第二次不应再改动（幂等）")
	}
	after2, _ := os.ReadFile(p)
	if string(after1) != string(after2) {
		t.Error("第二次运行改变了文件内容")
	}
}

// 已存在的模型条目应被就地更新，而不是重复追加。
func TestInstallUpdatesExistingModel(t *testing.T) {
	p := writeTemp(t, samplePatch())
	if _, err := Install(p, testRoute()); err != nil {
		t.Fatal(err)
	}

	// 同一个模型换个 contextWindow 再装
	r := testRoute()
	r.Models[0].ContextWindow = 999999
	if _, err := Install(p, r); err != nil {
		t.Fatal(err)
	}

	out, _ := os.ReadFile(p)
	if n := strings.Count(string(out), "id: auto"); n != 1 {
		t.Errorf("模型 auto 出现了 %d 次，应当只有 1 次", n)
	}
	if !strings.Contains(string(out), "999999") {
		t.Error("contextWindow 未被更新")
	}
	if strings.Contains(string(out), "196608") {
		t.Error("旧的 contextWindow 残留")
	}
}

// 文件里没有 llm-pi-ai 条目时，应追加一条完整的。
func TestInstallAppendsWhenEntryMissing(t *testing.T) {
	src := `# 只有别的东西
- id: plugin-manager
  name: "@deepseek-ai/dsh-plugin-manager"
  config:
    fallbackRegistries:
      - https://registry.npmmirror.com/
`
	p := writeTemp(t, src)
	res, err := Install(p, testRoute())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Changed {
		t.Fatal("应当改动")
	}
	out, _ := os.ReadFile(p)
	got := string(out)
	if !strings.Contains(got, "id: llm-pi-ai") {
		t.Fatal("未追加 llm-pi-ai 条目")
	}
	if !strings.Contains(got, "https://registry.npmmirror.com/") {
		t.Fatal("原有内容丢失")
	}
	doc := mustParse(t, p)
	if l := findEntry(doc); l == nil || l.Providers == nil {
		t.Fatal("追加的条目结构不对")
	}
}

// 条目在但没有 config.providers 时，应插入 config 段。
func TestInstallAddsConfigWhenMissing(t *testing.T) {
	src := `- id: llm-pi-ai
  name: "@deepseek-ai/dsh-llm-pi-ai"

- id: plugin-manager
  name: "@deepseek-ai/dsh-plugin-manager"
`
	p := writeTemp(t, src)
	if _, err := Install(p, testRoute()); err != nil {
		t.Fatal(err)
	}
	doc := mustParse(t, p)
	l := findEntry(doc)
	if l == nil || l.Providers == nil {
		t.Fatal("未插入 config.providers")
	}
	if _, v := mapGet(l.Providers, "mobilework"); v == nil {
		t.Fatal("未写入 mobilework 路由")
	}
}

// CRLF 文件：保持 CRLF，不混入 LF。
func TestInstallPreservesCRLF(t *testing.T) {
	crlf := strings.ReplaceAll(samplePatch(), "\n", "\r\n")
	p := writeTemp(t, crlf)

	if _, err := Install(p, testRoute()); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(p)
	if !strings.Contains(string(out), "\r\n") {
		t.Fatal("换行符被改成了 LF")
	}
	// 不应出现「单独 LF」（即 \n 前面不是 \r）
	s := string(out)
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' && (i == 0 || s[i-1] != '\r') {
			t.Fatalf("第 %d 字节出现裸 LF", i)
		}
	}
}

// ReadRoute 能读回刚写入的内容。
func TestReadRouteRoundTrip(t *testing.T) {
	p := writeTemp(t, samplePatch())
	want := testRoute()
	if _, err := Install(p, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadRoute(p, want.ProviderID)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("读不回路由")
	}
	if got.APIKeyEnv != want.APIKeyEnv || got.BaseURL != want.BaseURL || got.API != want.API {
		t.Errorf("路由字段不一致: %+v", got)
	}
	if len(got.Models) != len(want.Models) {
		t.Fatalf("模型数不一致: %d != %d", len(got.Models), len(want.Models))
	}
	if !routeModelsEqual(got.Models, want.Models) {
		t.Error("模型内容不一致")
	}

	// 幂等判断应当认为「无需改动」
	if !RoutesEqual(got, &want) {
		t.Error("RoutesEqual 应当判定相等")
	}
}

func routeModelsEqual(a, b []RouteModel) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]RouteModel{}
	for _, x := range a {
		m[x.ID] = x
	}
	for _, y := range b {
		x, ok := m[y.ID]
		if !ok || x.Name != y.Name || x.ContextWindow != y.ContextWindow || x.MaxTokens != y.MaxTokens {
			return false
		}
	}
	return true
}

func countComments(s string) int {
	n := 0
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "#") {
			n++
		}
	}
	return n
}

// routeEqual / findEntry 在非测试代码里也存在，这里复用 main 包的同名语义。
// （findEntry 在 patch.go 中定义）
