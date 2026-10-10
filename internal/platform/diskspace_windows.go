//go:build windows

package platform

import (
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// readDiskSpace asks Windows for the volume under dir (GetDiskFreeSpaceEx).
// The free figure is the one "available to the caller", which is what is left
// of the account's quota on the volume, not the volume's own free space.
func readDiskSpace(dir string) (DiskSpace, error) {
	// A UNC share is only accepted with a trailing backslash, a folder of a
	// drive with or without one.
	root := dir
	if !strings.HasSuffix(root, `\`) {
		root += `\`
	}
	ptr, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return DiskSpace{}, fmt.Errorf("disk space: %w", err)
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &available, &total, &free); err != nil {
		return DiskSpace{}, fmt.Errorf("disk space: GetDiskFreeSpaceEx %s: %w", dir, err)
	}
	return DiskSpace{TotalBytes: total, FreeBytes: available, Volume: volumeName(ptr, dir)}, nil
}

// volumeName names the volume under a path by its mount point ("C:\", a
// folder a drive is mounted on, a share), lower-cased because Windows paths
// are case-insensitive. Should the call fail, the drive letter or the share
// name stands in: coarser for a volume mounted on a folder, never wrong.
func volumeName(path *uint16, dir string) string {
	buf := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumePathName(path, &buf[0], uint32(len(buf))); err == nil {
		if name := windows.UTF16ToString(buf); name != "" {
			return strings.ToLower(name)
		}
	}
	return strings.ToLower(filepath.VolumeName(dir))
}
