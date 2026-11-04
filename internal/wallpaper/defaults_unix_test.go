//go:build unix

package wallpaper

import (
	"context"
	"path/filepath"
	"syscall"
	"testing"
)

func TestEnsureDefaultsRejectsFIFO(t *testing.T) {
	home := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(home, defaultsMarker), 0600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDefaults(context.Background(), home); err == nil {
		t.Fatal("FIFO marker accepted")
	}
}
