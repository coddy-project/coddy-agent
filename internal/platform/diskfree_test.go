package platform

import (
	"errors"
	"testing"
)

// The folder of a test is on a disk that reports a size and some room, never
// more room than the disk holds. A platform with no call to ask skips.
func TestDiskFreeOfAnExistingFolder(t *testing.T) {
	available, size, err := DiskFree(t.TempDir())
	if errors.Is(err, ErrDiskFreeUnsupported) {
		t.Skipf("no free-space call on this platform: %v", err)
	}
	if err != nil {
		t.Fatalf("DiskFree: %v", err)
	}
	if available == 0 || available > size {
		t.Fatalf("available = %d, size = %d; want 0 < available <= size", available, size)
	}
}

// A filesystem that reports no size at all (some network and virtual ones) is
// not known, which no caller may read as full. More room than the disk holds
// is a rounding of the kernel's, and reads as the whole disk.
func TestNormalizeDiskFree(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		available, size         uint64
		wantAvailable, wantSize uint64
		wantUnsupported         bool
	}{
		{name: "room within the disk is kept", available: 40, size: 100, wantAvailable: 40, wantSize: 100},
		{name: "room beyond the disk is clamped to it", available: 150, size: 100, wantAvailable: 100, wantSize: 100},
		{name: "a disk with no size is unavailable", available: 40, size: 0, wantUnsupported: true},
		{name: "no room on a disk is full, not unavailable", available: 0, size: 100, wantAvailable: 0, wantSize: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			available, size, err := normalizeDiskFree(tc.available, tc.size)
			if errors.Is(err, ErrDiskFreeUnsupported) != tc.wantUnsupported || (err != nil) != tc.wantUnsupported {
				t.Fatalf("err = %v, want unsupported = %v", err, tc.wantUnsupported)
			}
			if available != tc.wantAvailable || size != tc.wantSize {
				t.Errorf("available, size = %d, %d; want %d, %d", available, size, tc.wantAvailable, tc.wantSize)
			}
		})
	}
}
