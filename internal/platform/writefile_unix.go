//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package platform

import (
	"os"
	"syscall"
)

// DirSyncDurable reports that flushing a folder entry is implemented on this
// platform, so a write-then-rename sequence can be made crash durable.
const DirSyncDurable = true

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

// commitReplace renames tmpPath over target, atomically.
func commitReplace(tmpPath, target string) error { return os.Rename(tmpPath, target) }
