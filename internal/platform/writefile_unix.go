//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package platform

import (
	"os"
	"syscall"
)

// linkCount is the number of names the file at path has, 0 when unknown.
func linkCount(path string) uint64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0
	}
	return uint64(st.Nlink)
}

// syncDir flushes the folder entry a rename just wrote.
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()
	return d.Sync()
}

func isRetryableRenameError(error) bool { return false }
