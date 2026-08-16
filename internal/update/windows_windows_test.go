//go:build windows

package update

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyUpdateHelperUsesSystemTemp(t *testing.T) {
	t.Parallel()
	source := filepath.Join(t.TempDir(), "coddy.exe")
	if err := os.WriteFile(source, []byte("current Coddy"), 0o755); err != nil {
		t.Fatal(err)
	}
	helper, err := copyUpdateHelper(source)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(helper) })

	rel, err := filepath.Rel(os.TempDir(), helper)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		t.Fatalf("helper %q is outside system temp %q", helper, os.TempDir())
	}
	got, err := os.ReadFile(helper)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "current Coddy" {
		t.Fatalf("helper contents = %q", got)
	}
}

func TestIsTemporaryHelper(t *testing.T) {
	t.Parallel()
	valid := filepath.Join(os.TempDir(), "coddy-update-helper-123.exe")
	if !isTemporaryHelper(valid) {
		t.Fatalf("isTemporaryHelper(%q) = false", valid)
	}
	for _, path := range []string{
		filepath.Join(os.TempDir(), "other.exe"),
		filepath.Join(filepath.Dir(os.TempDir()), "not-temp", "coddy-update-helper-123.exe"),
	} {
		if isTemporaryHelper(path) {
			t.Fatalf("isTemporaryHelper(%q) = true", path)
		}
	}
}

func TestInstallWindowsUpdateRestoresPreviousBinaryWhenRestartFails(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "coddy.exe")
	staged := filepath.Join(dir, "coddy.new.exe")
	if err := os.WriteFile(target, []byte("previous Coddy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("not an executable"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	err := installWindowsUpdate(windowsUpdateRequest{
		Restart:    true,
		StagedPath: staged,
		TargetPath: target,
	}, "", &out)
	if err == nil {
		t.Fatal("expected restart failure")
	}
	if !strings.Contains(err.Error(), "restored the previous version") {
		t.Fatalf("error = %v", err)
	}
	got, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "previous Coddy" {
		t.Fatalf("target after rollback = %q", got)
	}
}
