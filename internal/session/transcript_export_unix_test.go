//go:build !windows

package session

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A named pipe at the target must be refused, not opened and not replaced.
func TestWriteExportFileRefusesNonRegularTargets(t *testing.T) {
	cwd := t.TempDir()
	fifo := filepath.Join(cwd, "pipe.md")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	target, err := ResolveExportTarget(cwd, "pipe.md", ExportFormatMarkdown, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, err = WriteExportFile(cwd, target, []byte("x"))
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("fifo target: err = %v, want a non-regular-file refusal", err)
	}
	if st, err := os.Lstat(fifo); err != nil || st.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("the fifo was replaced: %v %v", st, err)
	}
}
