//go:build linux

package ui

import "golang.org/x/sys/unix"

// Linux 的 termios 读写请求码
const (
	ioctlGetTermios = unix.TCGETS
	ioctlSetTermios = unix.TCSETS
)
