//go:build darwin || freebsd

package platform

import "golang.org/x/sys/unix"

// statfsBlockSize is the unit macOS and FreeBSD count f_blocks and f_bavail
// in: f_bsize, which has no separate fragment size next to it.
func statfsBlockSize(st *unix.Statfs_t) uint64 {
	return nonNegative(st.Bsize)
}
