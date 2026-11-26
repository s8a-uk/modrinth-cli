//go:build !windows

package download

import "os"

// renameReplace moves from to to, replacing to if it exists (atomic on one file system).
func renameReplace(from, to string) error { return os.Rename(from, to) }

// setMode makes the downloaded file readable like any other (CreateTemp uses 0600).
func setMode(path string) error { return os.Chmod(path, 0o644) }

// inUse reports whether a rename failed because another program holds the file.
func inUse(error) bool { return false }
