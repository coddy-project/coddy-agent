//go:build windows

package session

import (
	"errors"
	"syscall"
)

// Windows rejects both renaming over a file another handle holds open and opening
// a file an atomic write currently holds, surfacing ERROR_ACCESS_DENIED (5) or
// ERROR_SHARING_VIOLATION (32). Under concurrent access these are transient and
// worth a brief retry.
const (
	errAccessDenied     = syscall.Errno(5)
	errSharingViolation = syscall.Errno(32)
)

func isRetryableFileLockError(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == errAccessDenied || errno == errSharingViolation
	}
	return false
}
