//go:build unix

package platform

import (
	"fmt"
	"os"
	"syscall"
)

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
