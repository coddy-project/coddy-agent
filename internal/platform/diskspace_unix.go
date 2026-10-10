//go:build linux || darwin || freebsd

package platform

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// readDiskSpace asks the kernel for the volume under dir (statfs), which Linux
// - Android included, it takes the linux build tag - macOS and FreeBSD all
// answer. The free figure is f_bavail, the blocks a process without privileges
// can still write, not f_bfree: the superuser's reserve is no room for Coddy.
func readDiskSpace(dir string) (DiskSpace, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return DiskSpace{}, fmt.Errorf("disk space: statfs %s: %w", dir, err)
	}
	unit := statfsBlockSize(&st)
	ds := DiskSpace{
		TotalBytes: nonNegative(st.Blocks) * unit,
		FreeBytes:  nonNegative(st.Bavail) * unit,
	}
	info, err := os.Stat(dir)
	if err != nil {
		return DiskSpace{}, fmt.Errorf("disk space: %w", err)
	}
	// The device number names the volume: the same for every path below one
	// mount, another for a path below a different one.
	if sys, ok := info.Sys().(*syscall.Stat_t); ok {
		ds.Volume = fmt.Sprintf("dev:%v", sys.Dev)
	}
	if ds.Volume == "" {
		ds.Volume = dir
	}
	return ds, nil
}

// nonNegative widens a count the kernel reports in whichever integer type the
// platform declares it (FreeBSD's f_bavail is signed and goes below zero when
// the reserve is eaten into; Linux's f_bsize is 32 bits on 32-bit machines) to
// the unsigned count this package works in, clamping a negative one to zero.
func nonNegative[T ~int32 | ~int64 | ~uint32 | ~uint64](v T) uint64 {
	if v < 0 {
		return 0
	}
	return uint64(v)
}
