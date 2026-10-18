//go:build linux || darwin || freebsd

package platform

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// readDiskFree asks the kernel for the disk under path (statfs), which Linux -
// Android included, it takes the linux build tag - macOS and FreeBSD all
// answer. The room is f_bavail, the blocks a process without privileges can
// still write, not f_bfree: the superuser's reserve is no room for Coddy.
func readDiskFree(path string) (available, size uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	unit := statfsBlockSize(&st)
	return nonNegative(st.Bavail) * unit, nonNegative(st.Blocks) * unit, nil
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
