//go:build windows

package hooks

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile takes an exclusive lock on path (created when missing) and returns
// the release function. The CLI and the HTTP server are separate processes
// that edit the same receipts file, so the in-process mutex alone cannot
// serialise a read-modify-write cycle across them; LockFileEx does.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- the lock sits next to the receipts file in the coddy home
	if err != nil {
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	handle := windows.Handle(f.Fd())
	ol := new(windows.Overlapped)
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, ol); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, ol)
		_ = f.Close()
	}, nil
}
