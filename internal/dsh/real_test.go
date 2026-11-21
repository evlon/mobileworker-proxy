package dsh

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// 拿本机真实的 cordis.patch.yml 做一次「只读往返」验证：
// 证明我们的定位逻辑找得到条目，且解析不会因为文件里的 !!js 之类而失败。
// 注意：本测试全程只读，绝不写回真实文件。
func TestLocateRealProfiles(t *testing.T) {
	profiles, err := Locate()
	if err != nil {
		t.Skipf("本机没有 DSH profiles 目录: %v", err)
	}
	if len(profiles) == 0 {
		t.Skip("本机没有探测到 profile")
	}
	for _, p := range profiles {
		t.Logf("profile %s  active=%v  patch=%v", p.Name, p.Active, p.HasPatch)
	}
}

// 真实文件必须能被我们安全解析并定位到 llm-pi-ai 条目（存在的话）。
func TestParseRealPatchReadOnly(t *testing.T) {
	profiles, err := Locate()
	if err != nil {
		t.Skipf("跳过: %v", err)
	}
	checked := 0
	for _, p := range profiles {
		if !p.HasPatch {
			continue
		}
		raw, err := os.ReadFile(p.Patch)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", p.Patch, err)
		}
		before := string(raw)

		doc := parseForTest(t, p.Patch)
		l := findEntry(doc)
		if l != nil {
			checked++
			t.Logf("%s: 找到 llm-pi-ai 条目，providers=%v", p.Name, l.Providers != nil)
		} else {
			t.Logf("%s: 没有 llm-pi-ai 条目（安装时会追加）", p.Name)
		}

		// 确认没有被意外写回
		after, _ := os.ReadFile(p.Patch)
		if string(after) != before {
			t.Fatalf("%s 被测试改动了！", p.Patch)
		}
	}
	if checked == 0 {
		t.Log("（本机没有任何 profile 含 llm-pi-ai 条目）")
	}
}

// 真实文件做一次内存内安装，验证产出的 YAML 合法、注释不丢，但不落盘。
func TestInstallOnRealFileInMemory(t *testing.T) {
	profiles, err := Locate()
	if err != nil {
		t.Skipf("跳过: %v", err)
	}
	for _, p := range profiles {
		if !p.HasPatch {
			continue
		}
		raw, err := os.ReadFile(p.Patch)
		if err != nil {
			continue
		}
		orig := string(raw)

		// 复制到临时目录，在副本上真跑一遍
		tmp := t.TempDir() + "/cordis.patch.yml"
		if err := os.WriteFile(tmp, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		res, err := Install(tmp, testRoute())
		if err != nil {
			t.Fatalf("%s 副本上安装失败: %v", p.Name, err)
		}
		out, _ := os.ReadFile(tmp)
		got := string(out)
		t.Logf("%s: changed=%v 原 %d 行 -> 新 %d 行",
			p.Name, res.Changed, strings.Count(orig, "\n"), strings.Count(got, "\n"))

		// 注释不丢
		if a, b := countComments(orig), countComments(got); a != b {
			t.Errorf("%s 注释行数变化: %d -> %d", p.Name, a, b)
		}
		// !!js 保留
		if strings.Contains(orig, "!!js") && !strings.Contains(got, "!!js") {
			t.Errorf("%s 丢了 !!js 表达式", p.Name)
		}
		// 产出合法
		doc := parseForTest(t, tmp)
		if l := findEntry(doc); l == nil || l.Providers == nil {
			t.Errorf("%s 产出结构不对", p.Name)
		}
		// 真实文件本身没被动
		after, _ := os.ReadFile(p.Patch)
		if string(after) != orig {
			t.Fatalf("%s 的真实文件被改动了！", p.Patch)
		}
	}
}

func parseForTest(t *testing.T, path string) *yaml.Node {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("解析 %s 失败: %v", path, err)
	}
	return &doc
}
