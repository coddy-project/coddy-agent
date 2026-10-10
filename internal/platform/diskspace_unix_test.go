//go:build linux || darwin || freebsd

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The kernel declares its block counts as signed on some platforms and in 32
// bits on others; a count below zero (FreeBSD reports the reserve eaten into
// that way) is no room, not an enormous one.
func TestNonNegativeClampsAndWidens(t *testing.T) {
	if got := nonNegative(int64(-4096)); got != 0 {
		t.Errorf("nonNegative(-4096) = %d, want 0", got)
	}
	if got := nonNegative(int32(-1)); got != 0 {
		t.Errorf("nonNegative(int32(-1)) = %d, want 0", got)
	}
	if got := nonNegative(uint32(4096)); got != 4096 {
		t.Errorf("nonNegative(uint32(4096)) = %d, want 4096", got)
	}
	if got := nonNegative(uint64(1) << 40); got != 1<<40 {
		t.Errorf("nonNegative(1<<40) = %d, want %d", got, uint64(1)<<40)
	}
}

// A folder Coddy cannot even look at is not one it can store sessions in:
// the walk up does not skip past it to a volume further up, it says why.
func TestReadDiskSpaceReportsAFolderItCannotLookInto(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can look into any folder")
	}
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	_, err := ReadDiskSpace(filepath.Join(locked, "sessions"))
	if err == nil {
		t.Fatal("ReadDiskSpace succeeded below a folder nobody can enter")
	}
	if errors.Is(err, ErrDiskSpaceUnavailable) {
		t.Fatalf("a permission failure reads as \"not known\": %v", err)
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("error = %v, want the permission failure", err)
	}
}
