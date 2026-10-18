//go:build !windows && !linux && !darwin && !freebsd

package platform

// readDiskFree has no call to make here: the platforms this file covers (the
// other BSDs, Solaris, Plan 9, WebAssembly) are not ones Coddy ships for, and
// a caller treats the error as "not known".
func readDiskFree(string) (available, size uint64, err error) {
	return 0, 0, ErrDiskFreeUnsupported
}
