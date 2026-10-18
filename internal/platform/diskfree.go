package platform

import "errors"

// ErrDiskFreeUnsupported says the free space of a path cannot be read here:
// the platform has no call for it, or the filesystem under the path reports no
// size at all (some network and virtual filesystems). A caller treats it as
// "not known", never as "full".
var ErrDiskFreeUnsupported = errors.New("the free space of this disk cannot be read")

// DiskFree reports what the disk holding path has: the bytes a process without
// privileges can still write there (the reserve of the superuser on Unix and
// the share over an account's quota on Windows do not count) and the size of
// the disk. The path must exist. It is one system call that reads no file, so
// it is cheap enough to ask on every scrape.
func DiskFree(path string) (availableBytes, sizeBytes uint64, err error) {
	available, size, err := readDiskFree(path)
	if err != nil {
		return 0, 0, err
	}
	return normalizeDiskFree(available, size)
}

// normalizeDiskFree turns what the kernel reported into figures a caller can
// trust: a disk of no size is unavailable, and more room than the disk holds
// reads as the whole disk.
func normalizeDiskFree(available, size uint64) (uint64, uint64, error) {
	if size == 0 {
		return 0, 0, ErrDiskFreeUnsupported
	}
	return min(available, size), size, nil
}
