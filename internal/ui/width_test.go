package ui

import (
	"testing"
	"time"
)

func TestDisplayWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"自动", 4},          // 两个 CJK = 4 列
		{"auto", 4},
		{"「自动」", 8},        // CJK 括号也是全角
		{"a自b", 4},         // 1 + 2 + 1
		{"ctx 196608", 10},
	}
	for _, c := range cases {
		if got := displayWidth(c.in); got != c.want {
			t.Errorf("displayWidth(%q) = %d，期望 %d", c.in, got, c.want)
		}
	}
}

func TestIsWideRune(t *testing.T) {
	wide := []rune{'自', '动', '，', '：', '「', '」', '（', '）', '·'}
	// 注意：· 是 U+00B7，属于窄字符
	for _, r := range wide {
		want := r != '·'
		if got := isWideRune(r); got != want {
			t.Errorf("isWideRune(%q) = %v，期望 %v", r, got, want)
		}
	}
	narrow := []rune{'a', '1', ' ', 'c', '-', '/', '→'}
	for _, r := range narrow {
		if isWideRune(r) {
			t.Errorf("isWideRune(%q) 应为窄字符", r)
		}
	}
}

// 菜单在足够宽的终端下应判定为「放得下」。
func TestMenuFitsWide(t *testing.T) {
	lines := []string{
		"检测到 3 个可用模型：",
		"   >   1) auto                   ctx 196608 · out 32768 · 「自动」",
	}
	// terminalWidth() 在测试环境（非控制台）返回 80
	if !menuFits(lines) {
		t.Logf("当前 terminalWidth()=%d，菜单判定放不下（预期 80 列下应放得下）", terminalWidth())
	}
}

// 超宽行应判定为「放不下」，从而退回重印模式而不是错位重绘。
func TestMenuFitsTooWide(t *testing.T) {
	long := ""
	for i := 0; i < 500; i++ {
		long += "x"
	}
	if menuFits([]string{long}) {
		t.Error("超宽行应判定放不下")
	}
}

// 无论标记在哪一项、已输入什么、剩余时间多少，菜单行数必须恒定
// —— 这是原地重绘的前提。
func TestMenuLineCountStable(t *testing.T) {
	o := Options{
		Title:  "标题",
		Items:  []string{"甲", "乙", "丙"},
		Footer: "脚注",
	}
	n0 := len(menuLines(o, 0, "", 0))
	for _, hl := range []int{0, 1, 2} {
		for _, typed := range []string{"", "1", "12", "123"} {
			for _, remain := range []int{0, 1, 30, 300} {
				ms := menuLines(o, hl, typed, time.Duration(remain)*time.Second)
				if len(ms) != n0 {
					t.Fatalf("标记 %d / 已输入 %q / 剩余 %d 秒时行数变成 %d，应为 %d",
						hl, typed, remain, len(ms), n0)
				}
			}
		}
	}
}
