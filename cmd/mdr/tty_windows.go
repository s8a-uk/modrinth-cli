package main

import (
	"syscall"
	"unsafe"
)

// isTerminal reports whether fd is a Windows console handle. Pipes, files
// and NUL are not consoles. MSYS/mintty terminals use pipes and are
// therefore reported as non-terminals (use -o table there).
func isTerminal(fd uintptr) bool {
	var mode uint32
	return syscall.GetConsoleMode(syscall.Handle(fd), &mode) == nil
}

var procGetConsoleScreenBufferInfo = syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleScreenBufferInfo")

// consoleScreenBufferInfo is CONSOLE_SCREEN_BUFFER_INFO.
type consoleScreenBufferInfo struct {
	sizeX, sizeY                           int16
	cursorX, cursorY                       int16
	attributes                             uint16
	left, top, right, bottom               int16
	maximumWindowSizeX, maximumWindowSizeY int16
}

// termWidth returns the width of the console window at fd in columns, or 0.
func termWidth(fd uintptr) int {
	var info consoleScreenBufferInfo
	r, _, _ := procGetConsoleScreenBufferInfo.Call(fd, uintptr(unsafe.Pointer(&info)))
	if r == 0 {
		return 0
	}
	return int(info.right-info.left) + 1
}
