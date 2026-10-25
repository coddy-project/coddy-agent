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

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// A new file takes its mode from perm under the umask, which the replacement
// must not widen afterwards; an existing file keeps its own mode.
func TestWriteFileReplacingModes(t *testing.T) {
	dir := t.TempDir()
	created := filepath.Join(dir, "created.txt")
	previous := syscall.Umask(0o077)
	_, err := WriteFileReplacing(created, []byte("x"), 0o666)
	syscall.Umask(previous)
	if err != nil {
		t.Fatal(err)
	}
	if mode := modeOf(t, created); mode != 0o600 {
		t.Fatalf("new file mode = %o, want 600 under umask 077", mode)
	}

	existing := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(existing, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(existing, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteFileReplacing(existing, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if mode := modeOf(t, existing); mode != 0o640 {
		t.Fatalf("existing file mode = %o, want 640", mode)
	}
}

// The replacement of a private file is private from the moment it exists: its
// content must never be readable through a wider mode, even before the old mode
// is applied.
func TestWriteFileReplacingTemporaryFileIsPrivateForAnExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	whileWriting(t, path, func(tmp string) {
		if mode := modeOf(t, tmp); mode != 0o600 {
			t.Fatalf("temporary file mode while writing = %o, want 600", mode)
		}
	})
}

// A ".." on the way to a file climbs from where the components before it lead,
// as the kernel walks it, never lexically: in a link's text, in a relative link
// reached through a linked folder, and in the raw path itself. work/alias links
// to data/sub, and a decoy in work/ holds the name a lexical walk would hit.
func TestWriteFileReplacingDotDotFollowsTheLinks(t *testing.T) {
	for _, c := range []struct {
		name     string
		link     string // the link, relative to the root
		linkText string
		path     string // what is written, relative to the root and uncleaned
		want     string // the file that must change, relative to the root
		existing bool
	}{
		{"link text, existing target", "work/link", "alias/../victim", "work/link", "data/victim", true},
		{"link text, dangling", "work/link", "alias/../victim", "work/link", "data/victim", false},
		{"relative link through a linked folder", "data/sub/link", "../victim", "work/alias/link", "data/victim", false},
		{"raw path to a dangling link", "data/link", "victim", "work/alias/../link", "data/victim", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			for _, d := range []string{"data/sub", "work"} {
				if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(filepath.Join(root, "data", "sub"), filepath.Join(root, "work", "alias")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(c.linkText, filepath.Join(root, c.link)); err != nil {
				t.Fatal(err)
			}
			decoy := filepath.Join(root, "work", "victim")
			if err := os.WriteFile(decoy, []byte("decoy"), 0o644); err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(root, c.want)
			if c.existing {
				if err := os.WriteFile(want, []byte("old"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			if _, err := WriteFileReplacing(root+"/"+c.path, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, want); got != "x" {
				t.Fatalf("%s = %q, want x", c.want, got)
			}
			if got := readFile(t, decoy); got != "decoy" {
				t.Fatalf("work/victim = %q: the write landed where no link points", got)
			}
		})
	}
}

// A failed write in place leaves no promise that the file was kept: the file
// behind a device or a pipe is written as it is.
func TestWriteFileReplacingInPlaceErrorMakesNoPromise(t *testing.T) {
	if _, err := os.Stat("/dev/full"); err != nil {
		t.Skip("no /dev/full here")
	}
	_, err := WriteFileReplacing("/dev/full", []byte("x"), 0o644)
	if err == nil {
		t.Fatal("want the error of a full device")
	}
	if strings.Contains(err.Error(), "left as it was") {
		t.Fatalf("err = %q, an in-place write cannot promise that", err)
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
