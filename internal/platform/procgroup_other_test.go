//go:build !windows

package platform

import (
	"errors"
	"runtime"
	"syscall"
	"testing"
	"time"
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

// TestTerminateProcessGroupByPIDNeverSignalsItsOwnGroup: a recorded pid that
// turns out to be Coddy's own process group (a window process that does not
// lead a group of its own) is refused, not signalled.
func TestTerminateProcessGroupByPIDNeverSignalsItsOwnGroup(t *testing.T) {
	if err := TerminateProcessGroupByPID(syscall.Getpgrp(), time.Time{}, 0); err == nil {
		t.Fatal("terminating our own process group was not refused")
	}
}
