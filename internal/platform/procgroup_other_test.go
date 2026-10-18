//go:build !windows

package platform

import (
	"errors"
	"runtime"
	"strings"
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

// TestTerminateProcessGroupByPIDRefusesPID1AndItsOwnGroup: a recorded pid of
// 1 (kill(-1) reaches every process the user may signal) or of Coddy's own
// group (a window process that does not lead a group of its own) is refused,
// not signalled. The decision is tested as a predicate: a regression must
// fail this test, not signal the test run's own group.
func TestTerminateProcessGroupByPIDRefusesPID1AndItsOwnGroup(t *testing.T) {
	if !refusedGroup(1) || !refusedGroup(syscall.Getpgrp()) {
		t.Fatal("pid 1 or our own group is not refused")
	}
	if refusedGroup(syscall.Getpgrp() + 100000) {
		t.Fatal("another group is refused")
	}
	if err := TerminateProcessGroupByPID(1, time.Time{}, 0); err == nil || !strings.Contains(err.Error(), "not one to signal") {
		t.Fatalf("pid 1: %v", err)
	}
}
