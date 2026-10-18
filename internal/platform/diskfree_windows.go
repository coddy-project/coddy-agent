//go:build windows

package platform

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// readDiskFree asks Windows for the disk under path (GetDiskFreeSpaceEx). The
// room is the figure "available to the caller", which is what is left of the
// account's quota on the disk, not the disk's own free space.
func readDiskFree(path string) (available, size uint64, err error) {
	// A UNC share is only accepted with a trailing backslash, a folder of a
	// drive with or without one.
	root := path
	if !strings.HasSuffix(root, `\`) {
		root += `\`
	}
	ptr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, 0, fmt.Errorf("disk free %s: %w", path, err)
	}
	if err := windows.GetDiskFreeSpaceEx(ptr, &available, &size, nil); err != nil {
		return 0, 0, fmt.Errorf("GetDiskFreeSpaceEx %s: %w", path, err)
	}
	return available, size, nil
}
