//go:build unix

package gitws

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A .git that is not a regular file - a FIFO here - is refused at once rather
// than read: a read of it would block the caller, and every trust decision of
// the process waits behind that caller.
func TestMainCheckoutFolderDoesNotBlockOnAFifoDotGit(t *testing.T) {
	dir := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(dir, ".git"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan bool, 1)
	go func() {
		_, ok := MainCheckoutFolder(dir)
		done <- ok
	}()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("a FIFO .git answered a main checkout")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("MainCheckoutFolder blocked on a FIFO .git")
	}
}
