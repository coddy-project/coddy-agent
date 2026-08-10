//go:build !windows

package session

// On POSIX, rename atomically replaces the destination even while other handles
// hold it open, and a reader never collides with a writer, so there is no
// transient failure to retry.
func isRetryableFileLockError(error) bool { return false }
