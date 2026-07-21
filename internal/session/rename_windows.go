//go:build windows

package session

import (
	"errors"
	"syscall"
)

// Windows rejects renaming over a file that another handle holds open, surfacing
// ERROR_ACCESS_DENIED (5) or ERROR_SHARING_VIOLATION (32). Under concurrent
// atomic writes these are transient and worth a brief retry.
const (
	errAccessDenied     = syscall.Errno(5)
	errSharingViolation = syscall.Errno(32)
)

func isRetryableRenameError(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == errAccessDenied || errno == errSharingViolation
	}
	return false
}
