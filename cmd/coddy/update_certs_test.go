//go:build !windows

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeBinary(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "coddy")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// The update asks the new binary: `tls ensure --if-used`, and shows what it printed.
func TestRenewCertificatesAsksTheNewBinaryAndShowsWhatItDid(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	bin := fakeBinary(t, `echo "$@" > `+args+"\necho 'issue-server: it ends soon'\n")
	var out bytes.Buffer
	renewCertificates(context.Background(), bin, &out)
	if got, _ := os.ReadFile(args); strings.TrimSpace(string(got)) != "tls ensure --if-used" {
		t.Errorf("the new binary was run with %q", got)
	}
	if !strings.Contains(out.String(), "issue-server: it ends soon") {
		t.Errorf("what the binary printed was not shown: %q", out.String())
	}
}

func TestRenewCertificatesSaysNothingWhenThereWasNothingToDo(t *testing.T) {
	var out bytes.Buffer
	renewCertificates(context.Background(), fakeBinary(t, "exit 0\n"), &out)
	if out.Len() != 0 {
		t.Errorf("printed %q", out.String())
	}
}

// A failure of the new binary is a warning with its remedy, never an error of the update.
func TestRenewCertificatesWarnsWhenTheNewBinaryFails(t *testing.T) {
	var out bytes.Buffer
	renewCertificates(context.Background(), fakeBinary(t, "echo 'read-only home' >&2\nexit 3\n"), &out)
	got := out.String()
	if !strings.Contains(got, "warning:") || !strings.Contains(got, "coddy tls ensure") || !strings.Contains(got, "read-only home") {
		t.Errorf("warning = %q", got)
	}
}
