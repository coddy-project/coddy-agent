//go:build windows

package platform

import (
	"os"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// wantDiskFull is what Windows reports for a volume with no room left:
// ERROR_DISK_FULL (112) and ERROR_HANDLE_DISK_FULL (39).
var wantDiskFull = []syscall.Errno{windows.ERROR_DISK_FULL, windows.ERROR_HANDLE_DISK_FULL}

func TestDiskFullErrnosAreTheDocumentedWindowsCodes(t *testing.T) {
	if wantDiskFull[0] != 112 || wantDiskFull[1] != 39 {
		t.Fatalf("codes = %d, %d, want 112 and 39", wantDiskFull[0], wantDiskFull[1])
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
