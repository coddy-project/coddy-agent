//go:build !windows

package session

// On POSIX, rename atomically replaces the destination even while other handles
// hold it open, so there is no transient rename failure to retry.
func isRetryableRenameError(error) bool { return false }
