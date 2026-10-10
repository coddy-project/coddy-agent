//go:build !windows && !linux && !darwin && !freebsd

package platform

import "fmt"

// readDiskSpace has no call to make here: the platforms this file covers
// (the other BSDs, Solaris, Plan 9, WebAssembly) are not ones Coddy ships
// for, and a caller treats the error as "not known".
func readDiskSpace(dir string) (DiskSpace, error) {
	return DiskSpace{}, fmt.Errorf("%s: %w", dir, ErrDiskSpaceUnavailable)
}
