package ui

import (
	"testing"
	"time"
)

// 把一串字节喂给翻译器，收集所有按键事件。
func decode(t *testing.T, bs []byte) []KeyEvent {
	t.Helper()
	in := make(chan byte, len(bs))
	for _, b := range bs {
		in <- b
	}
	close(in)

	out := translateKeys(in)
	var got []KeyEvent
	timeout := time.After(3 * time.Second)
	for {
		select {
		case ev, ok := <-out:
			if !ok {
				return got
			}
			got = append(got, ev)
		case <-timeout:
			t.Fatal("翻译器卡住了")
		}
	}
}

// VT 形态：ESC [ A/B/C/D。
func TestTranslateVTArrows(t *testing.T) {
	cases := []struct {
		in   []byte
		want KeyKind
	}{
		{[]byte{0x1B, '[', 'A'}, KeyUp},
		{[]byte{0x1B, '[', 'B'}, KeyDown},
		{[]byte{0x1B, '[', 'C'}, KeyRight},
		{[]byte{0x1B, '[', 'D'}, KeyLeft},
		{[]byte{0x1B, '[', 'H'}, KeyHome},
		{[]byte{0x1B, '[', 'F'}, KeyEnd},
		{[]byte{0x1B, 'O', 'A'}, KeyUp},
		{[]byte{0x1B, 'O', 'B'}, KeyDown},
	}
	for _, c := range cases {
		got := decode(t, c.in)
		if len(got) != 1 {
			t.Errorf("%v: 应产生 1 个事件，实际 %d 个: %+v", c.in, len(got), got)
			continue
		}
		if got[0].Kind != c.want {
			t.Errorf("%v: 类型应为 %v，实际 %v", c.in, c.want, got[0].Kind)
		}
	}
}

// **关键回归**：VT 形态的上键是 ESC [ A，那个尾字节 'A'
// 绝不能被翻译成「普通字符 A」—— 否则按上键会误触「选全部」。
func TestTranslateVTArrowDoesNotLeakLetterA(t *testing.T) {
	got := decode(t, []byte{0x1B, '[', 'A'})
	for _, ev := range got {
		if ev.Kind == KeyRune && (ev.R == 'A' || ev.R == 'a') {
			t.Fatalf("上键泄漏出了字符 'A'（会被 UI 误判为「选全部」）: %+v", got)
		}
	}
}

// 传统控制台形态：0xE0 + 扫描码。
func TestTranslateScanCodeArrows(t *testing.T) {
	cases := []struct {
		in   []byte
		want KeyKind
	}{
		{[]byte{0xE0, 0x48}, KeyUp},
		{[]byte{0xE0, 0x50}, KeyDown},
		{[]byte{0xE0, 0x4B}, KeyLeft},
		{[]byte{0xE0, 0x4D}, KeyRight},
		{[]byte{0xE0, 0x47}, KeyHome},
		{[]byte{0xE0, 0x4F}, KeyEnd},
		{[]byte{0x00, 0x48}, KeyUp},
	}
	for _, c := range cases {
		got := decode(t, c.in)
		if len(got) != 1 || got[0].Kind != c.want {
			t.Errorf("%v: 期望 %v，实际 %+v", c.in, c.want, got)
		}
	}
}

// 普通字符原样通过。
func TestTranslatePlainRunes(t *testing.T) {
	got := decode(t, []byte("1aq\r"))
	want := []rune{'1', 'a', 'q', '\r'}
	if len(got) != len(want) {
		t.Fatalf("应有 %d 个事件，实际 %d: %+v", len(want), len(got), got)
	}
	for i, w := range want {
		if got[i].Kind != KeyRune || got[i].R != w {
			t.Errorf("第 %d 个: 期望 %q，实际 %+v", i, w, got[i])
		}
	}
}

// 单独一个 ESC（用户按了 Esc 键）不应卡住，也不应产生事件。
func TestTranslateLoneEscape(t *testing.T) {
	start := time.Now()
	got := decode(t, []byte{0x1B})
	elapsed := time.Since(start)
	if len(got) != 0 {
		t.Errorf("单独的 ESC 不应产生事件，实际 %+v", got)
	}
	if elapsed > 2*time.Second {
		t.Errorf("单独 ESC 处理太慢: %v", elapsed)
	}
}

// 连续多个方向键。
func TestTranslateMultipleArrows(t *testing.T) {
	in := []byte{0x1B, '[', 'B', 0x1B, '[', 'B', 0x1B, '[', 'A', '\r'}
	got := decode(t, in)
	kinds := []KeyKind{KeyDown, KeyDown, KeyUp, KeyRune}
	if len(got) != len(kinds) {
		t.Fatalf("应有 %d 个事件，实际 %d: %+v", len(kinds), len(got), got)
	}
	for i, k := range kinds {
		if got[i].Kind != k {
			t.Errorf("第 %d 个: 期望 %v，实际 %v", i, k, got[i].Kind)
		}
	}
	if got[3].R != '\r' {
		t.Errorf("最后一个应是回车，实际 %q", got[3].R)
	}
}

// ESC 后面不是序列引导符时，按普通字符处理（不丢键）。
func TestTranslateEscFollowedByRune(t *testing.T) {
	got := decode(t, []byte{0x1B, 'x'})
	if len(got) != 1 || got[0].Kind != KeyRune || got[0].R != 'x' {
		t.Errorf("ESC+普通字符 应产出该字符，实际 %+v", got)
	}
}

// 未识别的 VT 序列尾字节应被丢弃，不能变成字符。
func TestTranslateUnknownVTSequenceDropped(t *testing.T) {
	got := decode(t, []byte{0x1B, '[', 'Z'})
	for _, ev := range got {
		if ev.Kind == KeyRune {
			t.Errorf("未识别序列不应产出字符，实际 %+v", got)
			break
		}
	}
}
