package platform

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWritePrivateFileReplacing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	restore := InjectWriteFault(1)
	_, err := WritePrivateFileReplacing(path, []byte("new secret"))
	restore()
	if !errors.Is(err, ErrInjectedWriteFault) {
		t.Fatalf("write error: %v", err)
	}
	if got := readFile(t, path); got != "old" {
		t.Fatalf("old content: %q", got)
	}
	noTemporaryFiles(t, dir)
	if _, err := WritePrivateFileReplacing(path, []byte("new secret")); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "new secret" {
		t.Fatalf("new content: %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("mode: %o", info.Mode().Perm())
	}
	if _, err := WritePrivateFileReplacing(filepath.Join(dir, "missing", "secret"), []byte("x")); err == nil {
		t.Fatal("missing parent accepted")
	}
}

func TestWritePrivateFileReplacingRefusesLinksAndDirectories(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := WritePrivateFileReplacing(link, []byte("secret")); err == nil {
		t.Fatal("symlink accepted")
	}
	if got := readFile(t, target); got != "old" {
		t.Fatalf("target changed: %q", got)
	}
	if _, err := WritePrivateFileReplacing(dir, []byte("secret")); err == nil {
		t.Fatal("directory accepted")
	}
}
