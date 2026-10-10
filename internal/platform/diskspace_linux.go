//go:build linux

package platform

import "golang.org/x/sys/unix"

// statfsBlockSize is the unit Linux counts f_blocks and f_bavail in: the
// fundamental block size (f_frsize) when the filesystem reports one, else
// f_bsize, which is the same number on the filesystems that matter.
func statfsBlockSize(st *unix.Statfs_t) uint64 {
	if frsize := nonNegative(st.Frsize); frsize != 0 {
		return frsize
	}
	return nonNegative(st.Bsize)
}
