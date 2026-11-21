//go:build !windows && !linux && !darwin

package ui

// 其它 Unix（FreeBSD 等）暂未实现单键读取：
// ensureRaw 返回错误，Choose 会退化为「等超时后采用默认项」。
func ensureRaw() (<-chan KeyEvent, error) {
	return nil, errNoConsole
}

// RestoreConsole 无需还原。
func RestoreConsole() {}

// vtOutputOK 未探测时按不支持处理，避免往重定向的输出里写 ANSI 转义。
func vtOutputOK() bool { return false }

// terminalWidth 拿不到真实宽度时返回 80。
func terminalWidth() int { return 80 }
