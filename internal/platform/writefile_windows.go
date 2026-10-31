//go:build windows

package platform

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
	"time"
	"unsafe"

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

// fileBasicInfo is FILE_BASIC_INFO; a zero time leaves that time as it is.
type fileBasicInfo struct {
	CreationTime, LastAccessTime, LastWriteTime, ChangeTime int64
	FileAttributes                                          uint32
	_                                                       uint32
}

// matchAccess gives the replacement of target the target's DACL, attributes and
// creation time. A file created in the folder takes the folder's inherited
// permissions, so a file with a DACL of its own would otherwise be open to
// whoever the folder lets in, while it is written and after the rename. It works
// on the open handles only: a call by name would need the \\?\ prefix that
// package os adds to a long path and x/sys does not.
func matchAccess(tmp, target *os.File, info fs.FileInfo) error {
	to := windows.Handle(tmp.Fd())
	if persistentACLs(to) {
		if err := copyDACL(windows.Handle(target.Fd()), to); err != nil {
			return err
		}
	}
	data, ok := info.Sys().(*syscall.Win32FileAttributeData)
	if !ok {
		return nil
	}
	basic := fileBasicInfo{
		CreationTime:   int64(data.CreationTime.HighDateTime)<<32 | int64(data.CreationTime.LowDateTime),
		FileAttributes: data.FileAttributes&keptAttributes | windows.FILE_ATTRIBUTE_ARCHIVE,
	}
	return os.NewSyscallError("SetFileInformationByHandle", windows.SetFileInformationByHandle(to, windows.FileBasicInfo,
		(*byte)(unsafe.Pointer(&basic)), uint32(unsafe.Sizeof(basic)))) //nolint:gosec // G103: FILE_BASIC_INFO is passed by address
}

// persistentACLs reports whether the volume of h keeps ACLs (NTFS, ReFS); FAT
// and exFAT do not, and there is nothing to copy.
func persistentACLs(h windows.Handle) bool {
	var flags uint32
	if err := windows.GetVolumeInformationByHandle(h, nil, 0, nil, nil, &flags, nil, 0); err != nil {
		return true
	}
	return flags&windows.FILE_PERSISTENT_ACLS != 0
}

var procReOpenFile = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReOpenFile")

// copyDACL puts the DACL of from on to, protected when it is protected there. A
// handle opened for writing carries READ_CONTROL, which reads it. Setting it
// takes a second handle on to with WRITE_DAC and READ_CONTROL, since
// SetSecurityInfo reads the descriptor it changes; no sharing rule refuses that
// handle, as it asks for no access to the data.
func copyDACL(from, to windows.Handle) error {
	sd, err := windows.GetSecurityInfo(from, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return os.NewSyscallError("GetSecurityInfo", err)
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
	info := windows.SECURITY_INFORMATION(windows.DACL_SECURITY_INFORMATION)
	if control&windows.SE_DACL_PROTECTED != 0 {
		info |= windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	r, _, callErr := procReOpenFile.Call(uintptr(to), windows.WRITE_DAC|windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, 0)
	h := windows.Handle(r)
	if h == windows.InvalidHandle {
		return os.NewSyscallError("ReOpenFile", callErr)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	return os.NewSyscallError("SetSecurityInfo", windows.SetSecurityInfo(h, windows.SE_FILE_OBJECT, info, nil, nil, dacl, nil))
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
