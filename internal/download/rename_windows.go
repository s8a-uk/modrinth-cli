//go:build windows

package download

import (
	"errors"
	"os"
	"syscall"
	"time"
)

// Windows error codes a rename hits while another program (a running game, a virus
// scanner looking at the new file) has the file open.
const (
	errorAccessDenied     syscall.Errno = 5
	errorSharingViolation syscall.Errno = 32
	errorLockViolation    syscall.Errno = 33
)

// renameAttempts and the growing pause (100ms, 200ms, ...) give scanners about 1.5s.
const renameAttempts = 5

// renameReplace moves from to to, replacing to if it exists (os.Rename uses
// MoveFileEx with MOVEFILE_REPLACE_EXISTING). It retries while the file is in use.
func renameReplace(from, to string) error {
	var err error
	for i := 0; i < renameAttempts; i++ {
		if err = os.Rename(from, to); err == nil || !inUse(err) {
			return err
		}
		if i < renameAttempts-1 {
			time.Sleep(time.Duration(100*(i+1)) * time.Millisecond)
		}
	}
	return err
}

// setMode does nothing: Windows has no Unix permission bits to fix.
func setMode(string) error { return nil }

// inUse reports whether err is a sharing, lock or access violation.
func inUse(err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	return errno == errorAccessDenied || errno == errorSharingViolation || errno == errorLockViolation
}
