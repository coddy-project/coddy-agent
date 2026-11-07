//go:build windows

package llm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

const kimiSecureStoreAvailable = true

// Windows files inherit their DACL from the containing directory, as the
// existing private file writer does. The operator controls that directory.
func openKimiRoot(path string, create bool) (*os.Root, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("kimi credentials: empty directory")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if create {
		if err := os.MkdirAll(abs, 0700); err != nil {
			return nil, err
		}
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("kimi credentials: unsafe store directory %s", abs)
	}
	root, err := os.OpenRoot(abs)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		_ = root.Close()
		return nil, errors.New("kimi credentials: store directory changed")
	}
	return root, nil
}

func kimiCheckPrivateFile(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return errors.New("kimi credentials: unsafe file type")
	}
	return nil
}

func kimiTryFlock(f *os.File) (bool, error) {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return false, nil
	}
	return err == nil, err
}

func kimiUnlock(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, new(windows.Overlapped))
	_ = f.Close()
}
