//go:build unix

package wallpaper

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestFIFOReplacementDoesNotBlock(t *testing.T) {
	home := t.TempDir()
	writePicture(t, home, "picture", []byte("original"))
	root, err := openGallery(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	before, err := root.Lstat("picture")
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Remove("picture"); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(home, "wallpapers", "picture"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		file, _, err := openChecked(root, "picture", before)
		if file != nil {
			_ = file.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO replacement accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO replacement blocked at open")
	}
}

func TestSpecialFilesDoNotBlock(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "wallpapers")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(dir, "pipe.png"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := Read(home, "pipe.png"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("FIFO read blocked")
	}
	rows, err := List(home)
	if err != nil || len(rows) != 0 {
		t.Fatalf("FIFO listing: %v, %v", rows, err)
	}
}

func TestMutationsRefuseFIFOAndPreserveCollision(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "wallpapers")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(dir, "pipe.png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Delete(context.Background(), home, "pipe.png"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("FIFO delete: %v", err)
	}
	result, err := Upload(context.Background(), home, "pipe.png", bytes.NewReader(encodePicture(t, "png", 1, 1)))
	if err != nil || result.Wallpaper.Name != "pipe-2.png" {
		t.Fatalf("FIFO collision: %+v, %v", result, err)
	}
	if info, err := os.Lstat(filepath.Join(dir, "pipe.png")); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("FIFO replaced: %v", err)
	}
}
