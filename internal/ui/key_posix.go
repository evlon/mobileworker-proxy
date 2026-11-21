//go:build linux || darwin

package ui

import (
	"os"
	"sync"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

var (
	posixOnce  sync.Once
	posixKeyCh <-chan KeyEvent
	posixErr   error

	posixFd    int
	posixOld   *unix.Termios
	posixMu    sync.Mutex
	posixDone  bool
)

// makeRaw 把终端切成「逐键、不回显」，但**保留输出处理**。
//
// 这里刻意不用 term.MakeRaw：它会连 OPOST 一起清掉，
// 于是 "\n" 不再被翻译成回车换行，输出会变成阶梯状。
// 只关 ICANON（行缓冲）和 ECHO（回显）就够了。
func makeRaw(fd int) (*unix.Termios, error) {
	old, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return nil, err
	}
	nw := *old
	nw.Lflag &^= unix.ICANON | unix.ECHO
	nw.Cc[unix.VMIN] = 1
	nw.Cc[unix.VTIME] = 0
	if err := unix.IoctlSetTermios(fd, ioctlSetTermios, &nw); err != nil {
		return nil, err
	}
	return old, nil
}

// ensureRaw 打开单键读取；拿不到终端时返回错误，调用方退化为「等超时用默认项」。
func ensureRaw() (<-chan KeyEvent, error) {
	posixOnce.Do(func() {
		fd := int(os.Stdin.Fd())
		old, err := makeRaw(fd)
		if err != nil {
			posixErr = errNoConsole
			return
		}
		posixFd, posixOld = fd, old

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
		posixKeyCh = translateKeys(rawBytes)
	})
	return posixKeyCh, posixErr
}

// RestoreConsole 还原终端设置（进程退出前调用）。
func RestoreConsole() {
	posixMu.Lock()
	defer posixMu.Unlock()
	if posixDone || posixOld == nil {
		posixDone = true
		return
	}
	posixDone = true
	unix.IoctlSetTermios(posixFd, ioctlSetTermios, posixOld)
}

// vtOutputOK 报告输出端是不是终端；不是就别发 ANSI 转义（否则重定向时会脏）。
func vtOutputOK() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// terminalWidth 返回终端宽度（列），拿不到时 80。
func terminalWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w <= 0 {
		return 80
	}
	return w
}
