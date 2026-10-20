//go:build !windows

package platform

import (
	"errors"
	"runtime"
	"syscall"
	"testing"
)

// TestNobodyToSignalReadsDarwinsSpellingOfAnEmptyGroup pins the platform
// difference termination depends on: ESRCH means an empty group everywhere,
// EPERM means it on Darwin only, and a real error stays an error.
func TestNobodyToSignalReadsDarwinsSpellingOfAnEmptyGroup(t *testing.T) {
	if nobodyToSignal(nil) {
		t.Fatal("a delivered signal is not an empty group")
	}
	if !nobodyToSignal(syscall.ESRCH) {
		t.Fatal("ESRCH is an empty group on every unix")
	}
	if got, want := nobodyToSignal(syscall.EPERM), runtime.GOOS == "darwin"; got != want {
		t.Fatalf("EPERM read as an empty group = %v on %s, want %v", got, runtime.GOOS, want)
	}
	if nobodyToSignal(errors.New("something else")) {
		t.Fatal("an unrelated error must not pass for an empty group")
	}
}
