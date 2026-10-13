//go:build !windows

package session_test

import (
	"path/filepath"
	"syscall"
	"testing"
)

// A FIFO at the follow-up path would block the finished session's turn for
// good: the chain refuses it without opening it.
func TestGoalChainRefusesAFIFO(t *testing.T) {
	h := newChainHarness(t)
	h.writeAt(".coddy/keep", "")
	if err := syscall.Mkfifo(filepath.Join(h.cwd, filepath.FromSlash(nextTask)), 0o644); err != nil {
		t.Skip("mkfifo:", err)
	}
	h.expectRefusedFile("is not a regular file")
}
