// Package ui 提供控制台交互：倒计时 + 数字键选择。
package ui

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// ErrQuit 表示用户主动取消。
var ErrQuit = errors.New("用户取消了操作")

var errNoConsole = errors.New("当前终端不支持单键读取")

// Choice 是一次选择的结果。
type Choice struct {
	Index  int  // 选中的下标；-1 表示按了 A（全部）
	Key    rune // 原始按键
	Chosen bool // false 表示倒计时归零，采用默认项
}

// Options 控制一次选择。
type Options struct {
	Title   string
	Items   []string
	Footer  string
	Timeout time.Duration
	Default int
	// AllowAll 为 true 时，A 键表示「全部」（Index = -1）。
	AllowAll bool
}

// Choose 显示菜单并等待用户按键，带倒计时。
// 归零后返回默认项（Chosen=false）；拿不到终端时也走默认项。
func Choose(o Options) (Choice, error) {
	keys, err := ensureRaw()
	if err != nil {
		return chooseFrom(o, nil, true)
	}
	return chooseFrom(o, keys, false)
}

// ChooseWithKeys 是 Choose 的可测内核：从 keys 读按键。
func ChooseWithKeys(o Options, keys <-chan KeyEvent) (Choice, error) {
	return chooseFrom(o, keys, keys == nil)
}

// ---------------------------------------------------------------- 菜单渲染

// tagOf 返回第 i 项的编号标签，如 [1]。
func tagOf(i int) string { return "[" + strconv.Itoa(i+1) + "]" }

// menuLines 渲染菜单每一行。
//
// 行数恒定（标题 / 空行 / 各项 / 空行 / 提示 / 状态），
// 这是原地重绘不刷屏的前提。
func menuLines(o Options, hl int, typed string, remain time.Duration) []string {
	n := len(o.Items)
	tagW := len(strconv.Itoa(n)) + 2 // 含方括号的宽度

	ls := []string{o.Title, ""}
	for i, it := range o.Items {
		mark := "  "
		if i == hl {
			mark = "▸ "
		}
		// 编号右对齐 + 两侧留白，和后面的文字明确隔开
		tag := fmt.Sprintf("%*s", tagW, tagOf(i))
		ls = append(ls, fmt.Sprintf("   %s  %s %s", tag, mark, it))
	}
	ls = append(ls, "")

	hint := "按数字直接安装"
	if o.AllowAll {
		hint += " · A=全部安装"
	}
	hint += " · 回车/超时 采用 ▸ 标记的项 · Q=跳过"
	ls = append(ls, "   "+hint)

	status := fmt.Sprintf("倒计时 %2d 秒", int(remain.Seconds()+0.999))
	if typed != "" {
		status += "      已输入: " + typed
	}
	ls = append(ls, "   "+status)
	return ls
}

func printMenu(o Options, hl int, typed string, remain time.Duration) {
	for _, l := range menuLines(o, hl, typed, remain) {
		fmt.Println(l)
	}
}

// redrawMenu 用 ANSI 光标上移原地重绘，不刷屏。
func redrawMenu(o Options, hl int, typed string, remain time.Duration) {
	lines := menuLines(o, hl, typed, remain)
	fmt.Printf("\x1b[%dA", len(lines))
	for _, l := range lines {
		fmt.Printf("\r\x1b[2K%s\n", l)
	}
}

// clearMenu 把菜单区域擦掉，让结果行从干净处开始。
func clearMenu(o Options, interactive bool) {
	if !interactive {
		return
	}
	n := len(menuLines(o, 0, "", 0))
	fmt.Printf("\x1b[%dA", n)
	for i := 0; i < n; i++ {
		fmt.Print("\r\x1b[2K\n")
	}
	fmt.Printf("\x1b[%dA", n)
}

// ---------------------------------------------------------------- 选择主循环

func chooseFrom(o Options, keys <-chan KeyEvent, noConsole bool) (Choice, error) {
	items := o.Items
	if len(items) == 0 {
		return Choice{}, fmt.Errorf("没有可选项")
	}
	def := o.Default
	if def < 0 || def >= len(items) {
		def = 0
	}

	pick := func(idx int) {
		fmt.Printf("  已选择 [%d] %s\n", idx+1, items[idx])
	}

	// 无终端：等超时后用默认项
	if noConsole {
		printMenu(o, def, "", o.Timeout)
		if o.Timeout > 0 {
			time.Sleep(o.Timeout)
		}
		fmt.Printf("  （无终端输入，采用 [%d] %s）\n", def+1, items[def])
		return Choice{Index: def, Chosen: false}, nil
	}

	// 超时 <= 0：立即采用默认项
	if o.Timeout <= 0 {
		printMenu(o, def, "", 0)
		fmt.Printf("  （不计时等待，采用 [%d] %s）\n", def+1, items[def])
		return Choice{Index: def, Chosen: false}, nil
	}

	interactive := vtOutputOK() && menuFits(menuLines(o, def, "", o.Timeout))
	hl := def
	typed := ""
	deadline := time.Now().Add(o.Timeout)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()

	draw := func() {
		if interactive {
			redrawMenu(o, hl, typed, remaining(deadline))
		} else {
			printMenu(o, hl, typed, remaining(deadline))
		}
	}
	draw()

	for {
		select {
		case ev, ok := <-keys:
			if !ok {
				clearMenu(o, interactive)
				fmt.Printf("  输入结束 -> 采用 [%d] %s\n", def+1, items[def])
				return Choice{Index: def, Chosen: false}, nil
			}

			// 方向键：移动 ▸ 标记。
			// 实测在 Windows 传统控制台里方向键取不到字节，这里属于「能收到就更好」，
			// 收不到也不影响数字选择。
			if ev.Kind != KeyRune {
				switch ev.Kind {
				case KeyUp, KeyLeft:
					if hl > 0 {
						hl--
					} else {
						hl = len(items) - 1
					}
				case KeyDown, KeyRight:
					if hl < len(items)-1 {
						hl++
					} else {
						hl = 0
					}
				case KeyHome:
					hl = 0
				case KeyEnd:
					hl = len(items) - 1
				}
				typed = ""
				draw()
				continue
			}

			switch ev.R {
			case 'q', 'Q':
				clearMenu(o, interactive)
				fmt.Println("  已取消。")
				return Choice{}, ErrQuit

			case 'a', 'A':
				if o.AllowAll {
					clearMenu(o, interactive)
					fmt.Println("  已选择：全部模型")
					return Choice{Index: -1, Key: 'a', Chosen: true}, nil
				}

			case '\r', '\n':
				clearMenu(o, interactive)
				if typed != "" {
					if n, err := strconv.Atoi(typed); err == nil && n >= 1 && n <= len(items) {
						pick(n - 1)
						return Choice{Index: n - 1, Key: ev.R, Chosen: true}, nil
					}
				}
				pick(hl)
				return Choice{Index: hl, Key: ev.R, Chosen: true}, nil

			case 0x08, 0x7F: // 退格
				if typed != "" {
					typed = typed[:len(typed)-1]
					draw()
				}

			default:
				if ev.R >= '0' && ev.R <= '9' {
					cand := typed + string(ev.R)
					n, err := strconv.Atoi(cand)
					if err != nil || n < 1 || n > len(items) {
						// 越界：丢弃已输入内容，等下一次输入
						typed = ""
						draw()
						continue
					}
					// 若再补一位也不可能落在范围内，就无需回车，直接选定。
					// 例如 3 个模型时按 "2"：20 > 3，立即选定。
					if n*10 > len(items) {
						clearMenu(o, interactive)
						pick(n - 1)
						return Choice{Index: n - 1, Key: ev.R, Chosen: true}, nil
					}
					// 还有可能是两位数（模型数 > 9），等用户继续输入或回车
					typed = cand
					hl = n - 1
					draw()
				}
			}

		case <-tick.C:
			remain := remaining(deadline)
			if remain <= 0 {
				clearMenu(o, interactive)
				fmt.Printf("  倒计时结束 -> 采用 [%d] %s\n", def+1, items[def])
				return Choice{Index: def, Chosen: false}, nil
			}
			if interactive {
				redrawMenu(o, hl, typed, remain)
			}
		}
	}
}

func remaining(deadline time.Time) time.Duration {
	d := time.Until(deadline)
	if d < 0 {
		return 0
	}
	return d
}

// isWideRune 判断字符是否占两列（CJK 与全角形式）。
func isWideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F,
		r >= 0x2E80 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3,
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60,
		r >= 0xFFE0 && r <= 0xFFE6:
		return true
	}
	return false
}

// displayWidth 估算一行在终端里占的列数。
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		if isWideRune(r) {
			w += 2
		} else {
			w++
		}
	}
	return w
}

// menuFits 判断菜单所有行是否都不会折行。
// 原地重绘依赖「行数固定」；一旦某行折行，行数就变了，重绘会错位刷屏。
func menuFits(lines []string) bool {
	limit := terminalWidth()
	for _, l := range lines {
		if displayWidth(l) >= limit {
			return false
		}
	}
	return true
}
