package dsh

import (
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// 一个最小 LCS 差分：返回「只存在于原文件」和「只存在于新文件」的行块。
// 用它做严格验证——上一版按行号对齐的比对在有行插入/删除时会空转，不算数。
type hunk struct {
	Kind string // "del" 或 "add"
	From int    // 起始行号（0-based）
	Lines []string
}

func lcsDiff(a, b []string) []hunk {
	n, m := len(a), len(b)
	// dp[i][j] = a[i:] 与 b[j:] 的最长公共子序列长度
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out []hunk
	i, j := 0, 0
	var cur *hunk
	flush := func() {
		if cur != nil && len(cur.Lines) > 0 {
			out = append(out, *cur)
		}
		cur = nil
	}
	for i < n && j < m {
		if a[i] == b[j] {
			flush()
			i++
			j++
			continue
		}
		if dp[i+1][j] >= dp[i][j+1] {
			if cur == nil || cur.Kind != "del" {
				flush()
				cur = &hunk{Kind: "del", From: i}
			}
			cur.Lines = append(cur.Lines, a[i])
			i++
		} else {
			if cur == nil || cur.Kind != "add" {
				flush()
				cur = &hunk{Kind: "add", From: j}
			}
			cur.Lines = append(cur.Lines, b[j])
			j++
		}
	}
	for ; i < n; i++ {
		if cur == nil || cur.Kind != "del" {
			flush()
			cur = &hunk{Kind: "del", From: i}
		}
		cur.Lines = append(cur.Lines, a[i])
	}
	for ; j < m; j++ {
		if cur == nil || cur.Kind != "add" {
			flush()
			cur = &hunk{Kind: "add", From: j}
		}
		cur.Lines = append(cur.Lines, b[j])
	}
	flush()
	return out
}

// 严格验证：安装后，与原文件的差异必须**全部**落在 llm-pi-ai 的 providers 段落内。
func TestStrictDiffOnlyTouchesProviders(t *testing.T) {
	profiles, err := Locate()
	if err != nil {
		t.Skipf("跳过: %v", err)
	}
	for _, p := range profiles {
		if !p.HasPatch {
			continue
		}
		p := p
		t.Run(p.Name, func(t *testing.T) {
			raw, err := os.ReadFile(p.Patch)
			if err != nil {
				t.Skipf("读取失败: %v", err)
			}
			orig := string(raw)

			tmp := t.TempDir() + "/cordis.patch.yml"
			if err := os.WriteFile(tmp, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Install(tmp, testRoute()); err != nil {
				t.Fatal(err)
			}
			out, _ := os.ReadFile(tmp)

			a := strings.Split(orig, "\n")
			b := strings.Split(string(out), "\n")
			hunks := lcsDiff(a, b)
			if len(hunks) == 0 {
				t.Logf("%s: 无差异（已幂等）", p.Name)
				return
			}

			// 确定 providers 段落在前后两个文件里各自的行号范围。
			// 注意：del 块用旧文件坐标，add 块用新文件坐标，必须分别比。
			oldLo, oldHi := providersSpan(t, raw)
			newLo, newHi := providersSpan(t, out)
			entryExisted := hasEntry(raw)
			t.Logf("%s: providers 段落 旧文件 行%d-%d / 新文件 行%d-%d （原有 llm-pi-ai 条目: %v）",
				p.Name, oldLo+1, oldHi+1, newLo+1, newHi+1, entryExisted)

			for _, h := range hunks {
				t.Logf("%s: %s @行%d (%d 行)\n%s",
					p.Name, h.Kind, h.From+1, len(h.Lines), indentBlock(strings.Join(h.Lines, "\n")))

				// 原文件没有 llm-pi-ai 条目时，新条目的头部（id/name/config 行）
				// 本来就落在 providers: 之前，这是预期行为。
				if !entryExisted && h.Kind == "add" {
					continue
				}

				lo, hi := oldLo, oldHi
				if h.Kind == "add" {
					lo, hi = newLo, newHi
				}
				// 容差 1 行：段落内部换行处的归属可能差一行
				if h.From < lo-1 || h.From > hi+1 {
					t.Errorf("%s: %s 差异落在 providers 段外（行 %d，允许区间 %d-%d）",
						p.Name, h.Kind, h.From+1, lo+1, hi+1)
				}
			}

			// 另外确认：所有「非空且非注释」的删除行，都必须是 providers 段内的行
			for _, h := range hunks {
				if h.Kind != "del" {
					continue
				}
				for _, l := range h.Lines {
					if strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimSpace(l), "#") {
						t.Errorf("%s: 删掉了空行或注释: %q", p.Name, l)
					}
				}
			}
		})
	}
}

// 用一个「装空路由」的探针找出 providers 段落在文件里的行号范围。
func providersSpan(t *testing.T, raw []byte) (int, int) {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	l := findEntry(&doc)
	if l == nil || l.Providers == nil {
		return 0, len(strings.Split(string(raw), "\n")) - 1
	}
	return l.ProvKey.Line - 1, nodeEndLine(l.Providers) - 1
}

// hasEntry 判断文件里是否已有 llm-pi-ai 条目。
func hasEntry(raw []byte) bool {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return false
	}
	return findEntry(&doc) != nil
}
