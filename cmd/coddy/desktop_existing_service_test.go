//go:build desktop && http && ui

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExistingUserServiceWithoutIPCIsNotDuplicated(t *testing.T) {
	home := t.TempDir()
	alias := filepath.Join(t.TempDir(), "agent-home")
	if err := os.Symlink(home, alias); err != nil {
		alias = home
	}
	calls := 0
	active := func() (string, bool) {
		calls++
		return "the user service is active", true
	}
	if err := rejectExistingServiceWithoutIPC(alias, home, active); err == nil || !strings.Contains(err.Error(), "restart") {
		t.Fatalf("active service in the same home must be restarted: %v", err)
	}
	if calls != 1 {
		t.Fatalf("service state checked %d times", calls)
	}
	if err := rejectExistingServiceWithoutIPC(t.TempDir(), home, active); err != nil || calls != 1 {
		t.Fatalf("a different home must not be blocked: %v, calls %d", err, calls)
	}
	if err := rejectExistingServiceWithoutIPC(home, home, func() (string, bool) { return "", false }); err != nil {
		t.Fatalf("an inactive service must not block a new runtime: %v", err)
	}
}
