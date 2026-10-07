//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package platform

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// FileLockingAvailable reports whether this platform has an advisory file lock.
const FileLockingAvailable = true

// LockFile takes an exclusive advisory lock on path (created when missing)
// and returns the release function. The console, the CLI and coddy serve are
// separate processes that edit the same state files in the coddy home, so an
// in-process mutex alone cannot serialise a read-modify-write cycle across
// them; flock does. It blocks until the lock is free.
func LockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- the lock sits next to a state file in the coddy home
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, nil
}

// TryLockFile takes an exclusive advisory lock when it is immediately
// available. It reports acquired=false when another process holds the lock.
func TryLockFile(path string) (func(), bool, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- the lock sits next to a state file in the coddy home
	if err != nil {
		return nil, false, fmt.Errorf("lock %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, true, nil
}
