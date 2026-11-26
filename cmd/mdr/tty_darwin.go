package main

import (
	"syscall"
	"unsafe"
)

// isTerminal reports whether fd refers to a terminal. Unlike a
// ModeCharDevice check, it returns false for /dev/null.
func isTerminal(fd uintptr) bool {
	var t syscall.Termios
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGETA, uintptr(unsafe.Pointer(&t)))
	return errno == 0
}

// termWidth returns the width of the terminal at fd in columns, or 0.
func termWidth(fd uintptr) int {
	var ws struct{ Row, Col, Xpixel, Ypixel uint16 }
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TIOCGWINSZ, uintptr(unsafe.Pointer(&ws)))
	if errno != 0 {
		return 0
	}
	return int(ws.Col)
}
