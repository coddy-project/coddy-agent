//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package platform

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A new file takes its mode from perm under the umask; the replacement must not
// widen it afterwards.
func TestWriteFileReplacingNewFileKeepsTheUmask(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "created.txt")

	previous := syscall.Umask(0o077)
	_, err := WriteFileReplacing(path, []byte("x"), 0o666)
	syscall.Umask(previous)
	if err != nil {
		t.Fatal(err)
	}
	if mode := modeOf(t, path); mode != 0o600 {
		t.Fatalf("mode = %o, want 600 under umask 077", mode)
	}
}

// The replacement of a private file is private from the moment it exists: its
// content must never be readable through a wider mode, even before the old mode
// is applied.
func TestWriteFileReplacingTemporaryFileIsPrivateForAnExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	var seen os.FileMode
	src := readerFunc(func(p []byte) (int, error) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return 0, err
		}
		for _, e := range entries {
			if strings.Contains(e.Name(), ".tmp.") {
				info, err := e.Info()
				if err != nil {
					return 0, err
				}
				seen = info.Mode().Perm()
			}
		}
		return 0, io.EOF
	})
	if _, err := ReplaceFileFrom(path, src, 0o644); err != nil {
		t.Fatal(err)
	}
	if seen != 0o600 {
		t.Fatalf("temporary file mode while writing = %o, want 600", seen)
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

// A ".." in a relative link climbs from the folder the link really sits in, not
// from the folder it was reached through.
func TestWriteFileReplacingRelativeLinkThroughALinkedFolder(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "data", "sub")
	work := filepath.Join(root, "work")
	for _, d := range []string{sub, work} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(sub, filepath.Join(work, "alias")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../victim", filepath.Join(sub, "link")); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteFileReplacing(filepath.Join(work, "alias", "link"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(root, "data", "victim")); got != "x" {
		t.Fatalf("data/victim = %q, want x", got)
	}
	if _, err := os.Stat(filepath.Join(work, "victim")); err == nil {
		t.Fatal("the write landed in work/victim, a folder the link never pointed at")
	}
}

// A pipe is written through, as os.WriteFile does; replacing it would destroy it.
func TestWriteFileReplacingWritesAPipeInPlace(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no pipes here: %v", err)
	}

	got := make(chan string, 1)
	go func() {
		f, err := os.Open(fifo)
		if err != nil {
			got <- "open: " + err.Error()
			return
		}
		defer func() { _ = f.Close() }()
		b, _ := io.ReadAll(f)
		got <- string(b)
	}()

	done := make(chan error, 1)
	go func() {
		_, err := WriteFileReplacing(fifo, []byte("through the pipe"), 0o644)
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the write to a pipe did not finish")
	}
	if s := <-got; s != "through the pipe" {
		t.Fatalf("the reader got %q", s)
	}
	info, err := os.Lstat(fifo)
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("the pipe was replaced: %v %v", info, err)
	}
}
