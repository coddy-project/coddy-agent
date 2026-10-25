//go:build windows

package platform

import (
	"errors"
	"os"
	"syscall"
	"time"

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

// commitReplace renames tmpPath over target in one MoveFileEx, retrying briefly
// while another handle holds the target, as the session writer does.
func commitReplace(tmpPath, target string) error {
	const maxAttempts = 20
	var err error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err = os.Rename(tmpPath, target); err == nil || !isRetryableRenameError(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * time.Millisecond)
	}
	return err
}

// keptAttributes are the attributes of a replaced file its replacement takes;
// the archive bit is set on it whatever the old file had, since it is new.
const keptAttributes = windows.FILE_ATTRIBUTE_HIDDEN | windows.FILE_ATTRIBUTE_SYSTEM |
	windows.FILE_ATTRIBUTE_NOT_CONTENT_INDEXED

// matchAccess gives the replacement of target the target's DACL and attributes.
// A file created in the folder takes the folder's inherited permissions, so a
// file with a DACL of its own would otherwise be open to whoever the folder lets
// in, while it is written and after the rename.
func matchAccess(tmp *os.File, target string) error {
	if persistentACLs(tmp) {
		if err := copyDACL(target, tmp.Name()); err != nil {
			return err
		}
	}
	from, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	attrs, err := windows.GetFileAttributes(from)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(tmp.Name())
	if err != nil {
		return err
	}
	return windows.SetFileAttributes(to, attrs&keptAttributes|windows.FILE_ATTRIBUTE_ARCHIVE)
}

// persistentACLs reports whether the volume of f keeps ACLs (NTFS, ReFS); FAT
// and exFAT do not, and there is nothing to copy.
func persistentACLs(f *os.File) bool {
	var flags uint32
	if err := windows.GetVolumeInformationByHandle(windows.Handle(f.Fd()), nil, 0, nil, nil, &flags, nil, 0); err != nil {
		return true
	}
	return flags&windows.FILE_PERSISTENT_ACLS != 0
}

// copyDACL puts the DACL of from on to, protected when it is protected there:
// an unprotected one gets its inherited entries from the folder, which is the
// same folder.
func copyDACL(from, to string) error {
	sd, err := windows.GetNamedSecurityInfo(from, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if errors.Is(err, windows.ERROR_OBJECT_NOT_FOUND) {
		return nil // no DACL to copy
	}
	if err != nil {
		return err
	}
	control, _, err := sd.Control()
	if err != nil {
		return err
	}
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION | windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
	if control&windows.SE_DACL_PROTECTED != 0 {
		info = windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	return windows.SetNamedSecurityInfo(to, windows.SE_FILE_OBJECT, info, nil, nil, dacl, nil)
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
