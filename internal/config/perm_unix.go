//go:build !windows

package config

import "os"

// permWarning returns a warning when the file at path can be read or written by the
// group or others ("" otherwise, or when it cannot be stat'ed).
func permWarning(path string) string {
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&0o077 == 0 {
		return ""
	}
	return path + " is readable by other users; run: chmod 600 " + path
}
