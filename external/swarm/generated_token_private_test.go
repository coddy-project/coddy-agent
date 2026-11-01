//go:build swarm

package swarm

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestGeneratedTokenReplacesExistingWideFilePrivately(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, generatedTokenFile)
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	written, err := announceGeneratedToken(home, "new-secret", slog.New(slog.NewTextHandler(io.Discard, nil)), io.Discard, false)
	if err != nil || !written {
		t.Fatalf("written=%v err=%v", written, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new-secret\n" {
		t.Fatalf("data=%q err=%v", data, err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %04o, want 0600", info.Mode().Perm())
	}
}
