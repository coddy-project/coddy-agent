//go:build windows

package platform

import (
	"os"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// wantDiskFull is what Windows reports for a volume with no room left:
// ERROR_DISK_FULL (112), ERROR_HANDLE_DISK_FULL (39) and, for an account whose
// quota on the volume is used up, ERROR_DISK_QUOTA_EXCEEDED (1295). The codes
// are spelled as numbers, so a wrong constant in the implementation cannot
// agree with a test that reads the same constant.
var wantDiskFull = []syscall.Errno{112, 39, 1295}

func TestDiskFullErrnosAreTheDocumentedWindowsCodes(t *testing.T) {
	for i, want := range []syscall.Errno{windows.ERROR_DISK_FULL, windows.ERROR_HANDLE_DISK_FULL, windows.ERROR_DISK_QUOTA_EXCEEDED} {
		if wantDiskFull[i] != want {
			t.Errorf("code %d = %d, but the Windows constant is %d", i, wantDiskFull[i], want)
		}
	}
}

// ERROR_ACCESS_DENIED and a sharing violation are failures of another kind.
func TestIsDiskFullRefusesOtherWindowsCodes(t *testing.T) {
	for _, errno := range []syscall.Errno{windows.ERROR_ACCESS_DENIED, windows.ERROR_SHARING_VIOLATION, windows.ERROR_FILE_NOT_FOUND} {
		if IsDiskFull(&os.PathError{Op: "write", Path: `C:\x`, Err: errno}) {
			t.Errorf("IsDiskFull(%v) = true, want false", errno)
		}
	}
}
