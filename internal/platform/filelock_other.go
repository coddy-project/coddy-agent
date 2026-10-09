//go:build !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !windows

package platform

// FileLockingAvailable reports that this platform has no file-locking primitive.
const FileLockingAvailable = false

// LockFile is a no-op where no file locking primitive is available; the
// in-process mutex of the caller still serialises writers of one process.
func LockFile(string) (func(), error) {
	return func() {}, nil
}

// TryLockFile reports that exclusive ownership cannot be proven where the
// platform has no file-locking primitive.
func TryLockFile(string) (func(), bool, error) {
	return nil, false, nil
}
