//go:build windows

package platform

import (
	"errors"
	"syscall"

	"golang.org/x/sys/windows"
)

// Windows rejects renaming over a file another handle holds open with
// ERROR_ACCESS_DENIED (5) or ERROR_SHARING_VIOLATION (32); both are transient
// under concurrent access and worth a brief retry.
func isRetryableRenameError(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.Errno(5) || errno == syscall.Errno(32)
	}
	return false
}

// linkCount is the number of names the file at path has, 0 when unknown.
func linkCount(path string) uint64 {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0
	}
	h, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 0
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return 0
	}
	return uint64(info.NumberOfLinks)
}

// syncDir has nothing to flush: Windows offers no folder handle to fsync.
func syncDir(string) error { return nil }
