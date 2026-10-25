//go:build windows

package platform

import (
	"errors"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Windows rejects renaming over a file another handle holds open with
// ERROR_ACCESS_DENIED (5) or ERROR_SHARING_VIOLATION (32), and ReplaceFileW
// reports the same case as ERROR_UNABLE_TO_REMOVE_REPLACED (1175); all are
// transient under concurrent access and worth a brief retry.
func isRetryableRenameError(err error) bool {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.Errno(5) || errno == syscall.Errno(32) || errno == syscall.Errno(1175)
	}
	return false
}

var procReplaceFileW = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

// commitReplace puts tmpPath in the place of target. An existing target is
// replaced with ReplaceFileW, which keeps the replaced file's security
// descriptor (an explicit DACL included), attributes and creation time: a plain
// rename would give the file the folder's inherited permissions instead. A new
// target is a plain rename.
func commitReplace(tmpPath, target string, existing bool) error {
	if !existing {
		return retryReplace(func() error { return os.Rename(tmpPath, target) })
	}
	replaced, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	replacement, err := windows.UTF16PtrFromString(tmpPath)
	if err != nil {
		return err
	}
	return retryReplace(func() error {
		r, _, callErr := procReplaceFileW.Call(
			uintptr(unsafe.Pointer(replaced)),    //nolint:gosec // G103: ReplaceFileW takes the UTF-16 path of the replaced file
			uintptr(unsafe.Pointer(replacement)), //nolint:gosec // G103: ReplaceFileW takes the UTF-16 path of the replacement
			0, 0, 0, 0)
		if r != 0 {
			return nil
		}
		return &os.LinkError{Op: "replace", Old: tmpPath, New: target, Err: callErr}
	})
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
