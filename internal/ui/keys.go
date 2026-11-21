package ui

import "time"

// KeyKind 是一次按键事件的类型。
type KeyKind int

const (
	KeyRune KeyKind = iota // 普通字符（含回车、数字、字母）
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
)

// KeyEvent 是从终端读到的一次按键。
type KeyEvent struct {
	Kind KeyKind
	R    rune
}

// rk 是测试用的小helper。
func rk(r rune) KeyEvent { return KeyEvent{Kind: KeyRune, R: r} }

// 方向键在不同终端形态下的两种编码：
//
//   - 传统控制台（未开 VT 输入）：前缀字节 0xE0 或 0x00 + 扫描码
//   - 开了 VT 输入（Windows Terminal / ConPTY）：ESC [ A 这样的三字节序列
//
// 两种都要认。只认一种的话，另一种的尾字节会被当成普通字符 ——
// 例如 VT 形态下按「上」是 ESC [ A，那个 'A' 会被误判成「选全部」。
const (
	byteEsc      = 0x1B
	byteExtend   = 0xE0
	byteFunction = 0x00
)

// 传统控制台的扫描码
const (
	scanUp    = 0x48
	scanDown  = 0x50
	scanLeft  = 0x4B
	scanRight = 0x4D
	scanHome  = 0x47
	scanEnd   = 0x4F
)

// seqWait 是等待「序列后续字节」的上限。
// 按一下单独的 ESC 不该把读取协程永久卡住。
const seqWait = 60 * time.Millisecond

// translateKeys 把原始字节流翻译成按键事件。
// 单独拆出来是为了能在没有真实终端的情况下测到每条分支。
func translateKeys(bytes <-chan byte) <-chan KeyEvent {
	out := make(chan KeyEvent, 16)
	go func() {
		defer close(out)
		next := func() (byte, bool) {
			select {
			case b, ok := <-bytes:
				return b, ok
			case <-time.After(seqWait):
				return 0, false
			}
		}
		for b := range bytes {
			switch b {
			case byteExtend, byteFunction:
				// 传统形态：前缀 + 扫描码
				sc, ok := next()
				if !ok {
					continue
				}
				if ev, ok := scanToEvent(sc); ok {
					out <- ev
				}

			case byteEsc:
				// VT 形态：ESC [ X 或 ESC O X
				b2, ok := next()
				if !ok {
					continue // 单独一个 ESC，忽略
				}
				if b2 == '[' || b2 == 'O' {
					b3, ok := next()
					if !ok {
						continue
					}
					if ev, ok := finalToEvent(b3); ok {
						out <- ev
					}
					continue
				}
				// ESC 后面跟的不是序列引导符：当作普通字符处理
				out <- rk(rune(b2))

			default:
				out <- rk(rune(b))
			}
		}
	}()
	return out
}

func scanToEvent(sc byte) (KeyEvent, bool) {
	switch sc {
	case scanUp:
		return KeyEvent{Kind: KeyUp}, true
	case scanDown:
		return KeyEvent{Kind: KeyDown}, true
	case scanLeft:
		return KeyEvent{Kind: KeyLeft}, true
	case scanRight:
		return KeyEvent{Kind: KeyRight}, true
	case scanHome:
		return KeyEvent{Kind: KeyHome}, true
	case scanEnd:
		return KeyEvent{Kind: KeyEnd}, true
	}
	return KeyEvent{}, false
}

func finalToEvent(c byte) (KeyEvent, bool) {
	switch c {
	case 'A':
		return KeyEvent{Kind: KeyUp}, true
	case 'B':
		return KeyEvent{Kind: KeyDown}, true
	case 'C':
		return KeyEvent{Kind: KeyRight}, true
	case 'D':
		return KeyEvent{Kind: KeyLeft}, true
	case 'H':
		return KeyEvent{Kind: KeyHome}, true
	case 'F':
		return KeyEvent{Kind: KeyEnd}, true
	}
	return KeyEvent{}, false
}
