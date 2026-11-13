//go:build unix

package platform

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestWritePrivateFileReplacingRefusesFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := WritePrivateFileReplacing(path, []byte("secret")); err == nil {
		t.Fatal("FIFO accepted")
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("FIFO changed: %v %v", info, err)
	}
}
