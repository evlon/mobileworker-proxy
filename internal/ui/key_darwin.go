//go:build darwin

package ui

import "golang.org/x/sys/unix"

// macOS / BSD 的 termios 读写请求码与 Linux 不同
const (
	ioctlGetTermios = unix.TIOCGETA
	ioctlSetTermios = unix.TIOCSETA
)
