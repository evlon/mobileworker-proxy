//go:build windows

package ui

import (
	"os"
	"sync"
	"syscall"
	"unsafe"
)

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procGetStdHandle               = kernel32.NewProc("GetStdHandle")
	procGetConsoleMode             = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode             = kernel32.NewProc("SetConsoleMode")
	procGetConsoleScreenBufferInfo = kernel32.NewProc("GetConsoleScreenBufferInfo")
)

const (
	enableLineInput = 0x0002
	enableEchoInput = 0x0004
	enableVTProc    = 0x0004 // 输出模式：ENABLE_VIRTUAL_TERMINAL_PROCESSING

	// GetStdHandle 的伪句柄（-10 = stdin，-11 = stdout）
	stdInputHandle  = ^uintptr(9)
	stdOutputHandle = ^uintptr(10)
)

type coord struct{ X, Y int16 }

type smallRect struct{ Left, Top, Right, Bottom int16 }

type consoleScreenBufferInfo struct {
	Size              coord
	CursorPosition    coord
	Attributes        uint16
	Window            smallRect
	MaximumWindowSize coord
}

func getConsoleMode(h uintptr, mode *uint32) bool {
	r, _, _ := procGetConsoleMode.Call(h, uintptr(unsafe.Pointer(mode)))
	return r != 0
}

func setConsoleMode(h uintptr, mode uint32) bool {
	r, _, _ := procSetConsoleMode.Call(h, uintptr(mode))
	return r != 0
}

var (
	rawOnce   sync.Once
	keyCh     <-chan KeyEvent
	rawErr    error
	restoreFn func()
	restoreMu sync.Mutex
	restored  bool

	vtOutOK  bool
	widthVal int
)

// vtOutputOK 报告输出端是否支持 ANSI 转义（支持才能原地重绘菜单）。
func vtOutputOK() bool { return vtOutOK }

// terminalWidth 返回控制台窗口宽度（列）。拿不到时返回 80。
func terminalWidth() int {
	h, _, _ := procGetStdHandle.Call(stdOutputHandle)
	var info consoleScreenBufferInfo
	if r, _, _ := procGetConsoleScreenBufferInfo.Call(h, uintptr(unsafe.Pointer(&info))); r != 0 {
		w := int(info.Window.Right) - int(info.Window.Left) + 1
		if w > 0 {
			widthVal = w
		}
	}
	if widthVal <= 0 {
		return 80
	}
	return widthVal
}

// ensureRaw 准备按键读取：
//
//   - stdin 是控制台时，切成「逐键、不回显」模式，并打开 VT 输出；
//   - stdin 是管道/文件时（被脚本驱动），照样读字节并翻译转义序列。
//
// 只初始化一次很关键：重复创建读取协程会互相抢键。
func ensureRaw() (<-chan KeyEvent, error) {
	rawOnce.Do(func() {
		hIn, _, _ := procGetStdHandle.Call(stdInputHandle)
		var mode uint32
		if getConsoleMode(hIn, &mode) {
			if setConsoleMode(hIn, mode&^(enableLineInput|enableEchoInput)) {
				m := mode
				restoreFn = func() { procSetConsoleMode.Call(hIn, uintptr(m)) }
			}
		}
		// 打开 VT 输出处理，才能原地重绘菜单
		if hOut, _, _ := procGetStdHandle.Call(stdOutputHandle); hOut != 0 {
			var omode uint32
			if getConsoleMode(hOut, &omode) {
				if setConsoleMode(hOut, omode|enableVTProc) {
					vtOutOK = true
				}
			}
		}

		// 无论控制台还是管道，都从 stdin 读字节。
		// 管道被关闭时读取会返回 EOF，通道随之关闭 —— 调用方按「输入结束」处理。
		rawBytes := make(chan byte, 64)
		go func() {
			defer close(rawBytes)
			buf := make([]byte, 1)
			for {
				n, err := os.Stdin.Read(buf)
				if n > 0 {
					rawBytes <- buf[0]
				}
				if err != nil {
					return
				}
			}
		}()
		keyCh = translateKeys(rawBytes)
	})
	return keyCh, nil
}

// RestoreConsole 还原终端模式（进程退出前调用）。
func RestoreConsole() {
	restoreMu.Lock()
	defer restoreMu.Unlock()
	if restored || restoreFn == nil {
		restored = true
		return
	}
	restored = true
	restoreFn()
}
