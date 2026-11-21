package ui

import (
	"testing"
	"time"
)

func opts() Options {
	return Options{
		Title:    "选一个",
		Items:    []string{"甲", "乙", "丙"},
		Footer:   "（提示）",
		Timeout:  5 * time.Second,
		Default:  0,
		AllowAll: true,
	}
}

func keys(evs ...KeyEvent) chan KeyEvent {
	ch := make(chan KeyEvent, len(evs)+1)
	for _, e := range evs {
		ch <- e
	}
	return ch
}

func key(r rune) KeyEvent { return KeyEvent{Kind: KeyRune, R: r} }

// 数字键 → 选中对应项。
func TestChooseDigitKeys(t *testing.T) {
	for i, r := range []rune{'1', '2', '3'} {
		got, err := ChooseWithKeys(opts(), keys(key(r)))
		if err != nil {
			t.Fatalf("按键 %q 出错: %v", r, err)
		}
		if !got.Chosen || got.Index != i {
			t.Errorf("按键 %q 应选中下标 %d，实际 %+v", r, i, got)
		}
	}
}

// 上键：把光标从 0 循环到末尾。
func TestChooseArrowUpWrapsToEnd(t *testing.T) {
	got, err := ChooseWithKeys(opts(), keys(KeyEvent{Kind: KeyUp}, key('\r')))
	if err != nil {
		t.Fatal(err)
	}
	if got.Index != 2 {
		t.Errorf("上键从 0 应循环到 2，实际 %d", got.Index)
	}
}

// 下键：光标下移。
func TestChooseArrowDownMoves(t *testing.T) {
	got, err := ChooseWithKeys(opts(), keys(KeyEvent{Kind: KeyDown}, key('\r')))
	if err != nil {
		t.Fatal(err)
	}
	if got.Index != 1 {
		t.Errorf("下键应到 1，实际 %d", got.Index)
	}
}

// 下键到底后循环回开头。
func TestChooseArrowDownWrapsToStart(t *testing.T) {
	got, err := ChooseWithKeys(opts(),
		keys(KeyEvent{Kind: KeyDown}, KeyEvent{Kind: KeyDown}, KeyEvent{Kind: KeyDown}, key('\r')))
	if err != nil {
		t.Fatal(err)
	}
	if got.Index != 0 {
		t.Errorf("下键越界应循环到 0，实际 %d", got.Index)
	}
}

// 上上下下组合：0 -> 下 1 -> 下 2 -> 上 1 -> 上 0 -> 上 2
func TestChooseArrowSequence(t *testing.T) {
	seq := []KeyEvent{
		{Kind: KeyDown}, {Kind: KeyDown}, // ->2
		{Kind: KeyUp}, {Kind: KeyUp}, // ->0
		{Kind: KeyUp}, // ->2 (wrap)
		key('\r'),
	}
	got, err := ChooseWithKeys(opts(), keys(seq...))
	if err != nil {
		t.Fatal(err)
	}
	if got.Index != 2 {
		t.Errorf("一系列方向键后应停在 2，实际 %d", got.Index)
	}
}

// Home / End 键。
func TestChooseHomeEnd(t *testing.T) {
	got, _ := ChooseWithKeys(opts(), keys(KeyEvent{Kind: KeyEnd}, key('\r')))
	if got.Index != 2 {
		t.Errorf("End 应到 2，实际 %d", got.Index)
	}
	got, _ = ChooseWithKeys(opts(), keys(KeyEvent{Kind: KeyEnd}, KeyEvent{Kind: KeyHome}, key('\r')))
	if got.Index != 0 {
		t.Errorf("Home 应到 0，实际 %d", got.Index)
	}
}

// 左右键等同于上下。
func TestChooseLeftRight(t *testing.T) {
	got, _ := ChooseWithKeys(opts(), keys(KeyEvent{Kind: KeyRight}, key('\r')))
	if got.Index != 1 {
		t.Errorf("右键应到 1，实际 %d", got.Index)
	}
	got, _ = ChooseWithKeys(opts(), keys(KeyEvent{Kind: KeyRight}, KeyEvent{Kind: KeyLeft}, key('\r')))
	if got.Index != 0 {
		t.Errorf("右键后左键应回 0，实际 %d", got.Index)
	}
}

// 方向键后按数字键仍可用。
func TestChooseArrowThenDigit(t *testing.T) {
	got, _ := ChooseWithKeys(opts(), keys(KeyEvent{Kind: KeyDown}, key('3')))
	if got.Index != 2 {
		t.Errorf("数字键 3 应直接选中 2，实际 %d", got.Index)
	}
}

// 超出范围的数字键应被忽略，光标不动。
func TestChooseOutOfRangeDigitIgnored(t *testing.T) {
	o := opts()
	o.Timeout = 300 * time.Millisecond
	got, err := ChooseWithKeys(o, keys(key('9')))
	if err != nil {
		t.Fatal(err)
	}
	if got.Chosen || got.Index != 0 {
		t.Errorf("越界数字应被忽略并采用默认，实际 %+v", got)
	}
}

// A 键 → 全部（Index = -1），仅在 AllowAll 时。
func TestChooseAll(t *testing.T) {
	got, err := ChooseWithKeys(opts(), keys(key('a')))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Chosen || got.Index != -1 {
		t.Errorf("A 键应返回全部（-1），实际 %+v", got)
	}
}

// AllowAll=false 时 A 键不生效（被忽略）。
func TestChooseAllDisabled(t *testing.T) {
	o := opts()
	o.AllowAll = false
	o.Timeout = 300 * time.Millisecond
	got, err := ChooseWithKeys(o, keys(key('a')))
	if err != nil {
		t.Fatal(err)
	}
	if got.Chosen {
		t.Errorf("AllowAll=false 时 A 键应被忽略，实际 %+v", got)
	}
}

// Q 键 → 取消。
func TestChooseQuit(t *testing.T) {
	if _, err := ChooseWithKeys(opts(), keys(key('q'))); err != ErrQuit {
		t.Errorf("Q 键应返回 ErrQuit，实际 %v", err)
	}
}

// 回车 → 当前光标处的项（不是默认项）。
func TestChooseEnterUsesCursor(t *testing.T) {
	o := opts()
	o.Default = 0
	got, err := ChooseWithKeys(o, keys(KeyEvent{Kind: KeyDown}, key('\r')))
	if err != nil {
		t.Fatal(err)
	}
	if got.Index != 1 {
		t.Errorf("回车应确认光标所在项 1，实际 %d", got.Index)
	}
}

// 倒计时归零 → 采用默认项（不是光标项），Chosen=false。
func TestChooseTimeout(t *testing.T) {
	o := opts()
	o.Timeout = 300 * time.Millisecond
	o.Default = 2
	start := time.Now()
	got, err := ChooseWithKeys(o, make(chan KeyEvent))
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if got.Chosen {
		t.Error("超时应标记 Chosen=false")
	}
	if got.Index != 2 {
		t.Errorf("超时应采用默认项 2，实际 %d", got.Index)
	}
	if elapsed < 250*time.Millisecond {
		t.Errorf("没等到超时就返回了（%v）", elapsed)
	}
}

// 无终端 → 采用默认项，不报错。
func TestChooseNoConsole(t *testing.T) {
	o := opts()
	o.Timeout = 100 * time.Millisecond
	o.Default = 2
	got, err := ChooseWithKeys(o, nil)
	if err != nil {
		t.Fatalf("无终端不应报错: %v", err)
	}
	if got.Chosen || got.Index != 2 {
		t.Errorf("无终端应采用默认项 2，实际 %+v", got)
	}
}

// 越界的 Default 应被夹到 0。
func TestChooseDefaultClamped(t *testing.T) {
	o := opts()
	o.Default = 99
	o.Timeout = 100 * time.Millisecond
	got, err := ChooseWithKeys(o, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Index != 0 {
		t.Errorf("越界默认值应夹到 0，实际 %d", got.Index)
	}
}

// 空选项列表应报错。
func TestChooseEmptyItems(t *testing.T) {
	o := opts()
	o.Items = nil
	if _, err := ChooseWithKeys(o, nil); err == nil {
		t.Error("空列表应当报错")
	}
}

// 超时 <= 0 时立即采用默认项。
func TestChooseZeroTimeout(t *testing.T) {
	o := opts()
	o.Timeout = 0
	got, err := ChooseWithKeys(o, make(chan KeyEvent))
	if err != nil {
		t.Fatal(err)
	}
	if got.Chosen || got.Index != 0 {
		t.Errorf("超时 0 应立即采用默认项，实际 %+v", got)
	}
}

// ---------------------------------------------------------------- 两位数选择

// 模型数 > 9 时：单个数字是「可能是两位数的开头」，应等待继续输入。
func TestChooseTwoDigitWaits(t *testing.T) {
	o := opts()
	o.Items = make([]string, 12)
	for i := range o.Items {
		o.Items[i] = "m" + string(rune('a'+i))
	}
	// 输入 "1" 后不回车，应等超时用默认项（说明它没急着把 1 定下来）
	o.Timeout = 300 * time.Millisecond
	got, err := ChooseWithKeys(o, keys(key('1')))
	if err != nil {
		t.Fatal(err)
	}
	if got.Chosen {
		t.Errorf("12 项时输入 1 应等待第二位，实际直接选了 %+v", got)
	}
}

// 模型数 > 9 时：两位数字应正确选中。
func TestChooseTwoDigitSelects(t *testing.T) {
	o := opts()
	o.Items = make([]string, 12)
	for i := range o.Items {
		o.Items[i] = "m" + string(rune('a'+i))
	}
	got, err := ChooseWithKeys(o, keys(key('1'), key('2')))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Chosen || got.Index != 11 {
		t.Errorf("输入 12 应选中下标 11，实际 %+v", got)
	}
}

// 模型数 > 9 时：输入 "1" 后回车，应选中第 1 项。
func TestChooseTwoDigitThenEnter(t *testing.T) {
	o := opts()
	o.Items = make([]string, 12)
	got, err := ChooseWithKeys(o, keys(key('1'), key('\r')))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Chosen || got.Index != 0 {
		t.Errorf("输入 1 后回车应选中 0，实际 %+v", got)
	}
}

// 模型数 > 9 时：输入越界的两位数字（如 15）应被丢弃，走默认。
func TestChooseTwoDigitOutOfRange(t *testing.T) {
	o := opts()
	o.Items = make([]string, 12)
	o.Timeout = 300 * time.Millisecond
	got, err := ChooseWithKeys(o, keys(key('1'), key('5')))
	if err != nil {
		t.Fatal(err)
	}
	if got.Chosen {
		t.Errorf("15 越界应被丢弃并等超时，实际 %+v", got)
	}
}

// 模型数 <= 9 时：单个数字应立即选定，不需要回车（高效路径）。
func TestChooseSingleDigitImmediate(t *testing.T) {
	o := opts() // 3 项
	got, err := ChooseWithKeys(o, keys(key('2')))
	if err != nil {
		t.Fatal(err)
	}
	if !got.Chosen || got.Index != 1 {
		t.Errorf("3 项时按 2 应立即选中 1，实际 %+v", got)
	}
}

// 退格键应能删掉已输入的位数。
func TestChooseBackspace(t *testing.T) {
	o := opts()
	o.Items = make([]string, 12)
	// 输入 "1" 再退格再输入 "2" -> 应选中第 2 项
	got, err := ChooseWithKeys(o, keys(key('1'), key(0x08), key('2'), key('\r')))
	if err != nil {
		t.Fatal(err)
	}
	if got.Index != 1 {
		t.Errorf("退格后输入 2 应选中 1，实际 %+v", got)
	}
}
