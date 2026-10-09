//go:build !windows

package platform

import (
	"errors"
	"os"
	"runtime"
	"syscall"
	"testing"
)

// wantDiskFull is what this platform reports for a volume with no room
// left: ENOSPC, and EDQUOT for an exhausted quota.
var wantDiskFull = []syscall.Errno{syscall.ENOSPC, syscall.EDQUOT}

// /dev/full answers every write with ENOSPC, so a real *os.PathError out of
// the kernel is classified, not only one built by hand.
func TestIsDiskFullRecognisesTheKernelsAnswerFromDevFull(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/dev/full is a Linux device")
	}
	f, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Skipf("/dev/full is not available: %v", err)
	}
	defer func() { _ = f.Close() }()
	_, err = f.Write([]byte("x"))
	if err == nil {
		t.Fatal("a write to /dev/full succeeded")
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("write error is %T, want *os.PathError", err)
	}
	if !IsDiskFull(err) {
		t.Fatalf("IsDiskFull(%v) = false, want true", err)
	}
}
