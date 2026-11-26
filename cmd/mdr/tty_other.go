//go:build !linux && !darwin && !windows

package main

import "os"

// isTerminal falls back to a character-device check on other platforms.
// It cannot tell a terminal from /dev/null.
func isTerminal(fd uintptr) bool {
	st, err := os.NewFile(fd, "").Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

// termWidth is unknown on other platforms (COLUMNS still applies).
func termWidth(uintptr) int { return 0 }
