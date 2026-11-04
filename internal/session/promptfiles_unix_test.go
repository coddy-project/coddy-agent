//go:build !windows

package session_test

import (
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A FIFO in the workspace would block the read until something wrote to it,
// holding the request and its goroutine, so only a regular file is opened.
func TestReadWorkspaceUTF8DoesNotBlockOnAFIFO(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, err := session.ReadWorkspaceUTF8(root, "pipe")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a FIFO was read as a text file")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a FIFO of the workspace blocked")
	}
}
