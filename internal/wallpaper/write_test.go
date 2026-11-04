package wallpaper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"
)

func galleryNames(t *testing.T, home string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, "wallpapers"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestUploadCreatesDeduplicatesAndPreservesCollisions(t *testing.T) {
	home := t.TempDir()
	original := encodePicture(t, "png", 1, 1)
	other := encodePicture(t, "png", 2, 1)
	for _, tc := range []struct {
		name    string
		body    []byte
		want    string
		created bool
	}{
		{"sky.png", original, "sky.png", true},
		{"different.jpg", original, "sky.png", false},
		{"sky.png", other, "sky-2.png", true},
		{"another.png", other, "sky-2.png", false},
	} {
		result, err := Upload(context.Background(), home, tc.name, bytes.NewReader(tc.body))
		if err != nil || result.Wallpaper.Name != tc.want || result.Created != tc.created {
			t.Fatalf("%s: result %+v, error %v", tc.name, result, err)
		}
	}
	if got := galleryNames(t, home); !reflect.DeepEqual(got, []string{"sky-2.png", "sky.png"}) {
		t.Fatalf("files or temporary leftovers: %v", got)
	}
	for name, want := range map[string][]byte{"sky.png": original, "sky-2.png": other} {
		got, err := os.ReadFile(filepath.Join(home, "wallpapers", name))
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s was overwritten: %v", name, err)
		}
	}
	if runtime.GOOS != "windows" {
		for path, mode := range map[string]os.FileMode{"wallpapers": 0o700, filepath.Join("wallpapers", "sky.png"): 0o600} {
			info, err := os.Stat(filepath.Join(home, path))
			if err != nil || info.Mode().Perm() != mode {
				t.Fatalf("%s permissions: %v, %v", path, info, err)
			}
		}
	}
}

func TestUploadDeduplicatesPreinstalledImages(t *testing.T) {
	home := t.TempDir()
	raw := encodePicture(t, "png", 1, 1)
	writePicture(t, home, "Z.png", raw)
	writePicture(t, home, "A.png", raw)
	result, err := Upload(context.Background(), home, "new.png", bytes.NewReader(raw))
	if err != nil || result.Created || result.Wallpaper.Name != "A.png" {
		t.Fatalf("preinstalled duplicate: %+v, %v", result, err)
	}
	if got := galleryNames(t, home); !reflect.DeepEqual(got, []string{"A.png", "Z.png"}) {
		t.Fatalf("duplicate created files: %v", got)
	}
}

func TestUploadPreservesNonImageAndDirectoryCollisions(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(fmt.Sprint(directory), func(t *testing.T) {
			home := t.TempDir()
			writePicture(t, home, "name.png", []byte("not an image"))
			path := filepath.Join(home, "wallpapers", "name.png")
			if directory {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			result, err := Upload(context.Background(), home, "name.png", bytes.NewReader(encodePicture(t, "png", 1, 1)))
			if err != nil || result.Wallpaper.Name != "name-2.png" || !result.Created {
				t.Fatalf("collision: %+v, %v", result, err)
			}
			info, err := os.Stat(path)
			if err != nil || info.IsDir() != directory {
				t.Fatalf("collision replaced: %v", err)
			}
			if !directory {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != "not an image" {
					t.Fatalf("non-image overwritten: %v", err)
				}
			}
		})
	}
}

func TestConcurrentUploadsDeduplicate(t *testing.T) {
	home := t.TempDir()
	raw := encodePicture(t, "png", 1, 1)
	type outcome struct {
		result UploadResult
		err    error
	}
	const count = 12
	results := make(chan outcome, count)
	start := make(chan struct{})
	for i := 0; i < count; i++ {
		go func() {
			<-start
			result, err := Upload(context.Background(), home, fmt.Sprintf("picture-%d.png", i), bytes.NewReader(raw))
			results <- outcome{result, err}
		}()
	}
	close(start)
	created, name := 0, ""
	for i := 0; i < count; i++ {
		got := <-results
		if got.err != nil {
			t.Fatal(got.err)
		}
		if got.result.Created {
			created++
		}
		if name == "" {
			name = got.result.Wallpaper.Name
		}
		if got.result.Wallpaper.Name != name {
			t.Fatalf("different duplicate names: %q, %q", name, got.result.Wallpaper.Name)
		}
	}
	if created != 1 || !reflect.DeepEqual(galleryNames(t, home), []string{name}) {
		t.Fatalf("created %d files: %v", created, galleryNames(t, home))
	}
}

type pausingUploadReader struct {
	io.Reader
	once    sync.Once
	ready   chan struct{}
	release chan struct{}
}

func (r *pausingUploadReader) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.ready); <-r.release })
	return r.Reader.Read(p)
}

func TestUploadDoesNotHoldMutationLockWhileReadingBody(t *testing.T) {
	home := t.TempDir()
	body := &pausingUploadReader{Reader: bytes.NewReader(encodePicture(t, "png", 1, 1)), ready: make(chan struct{}), release: make(chan struct{})}
	done := make(chan error, 1)
	go func() { _, err := Upload(context.Background(), home, "slow.png", body); done <- err }()
	<-body.ready
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := Upload(ctx, home, "fast.png", bytes.NewReader(encodePicture(t, "png", 2, 1)))
	close(body.release)
	firstErr := <-done
	if err != nil || firstErr != nil {
		t.Fatalf("body held publication lock: second %v, first %v", err, firstErr)
	}
}

type cancelAtEOF struct {
	io.Reader
	cancel context.CancelFunc
}

func (r cancelAtEOF) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		r.cancel()
	}
	return n, err
}

func TestUploadFailuresLeaveNoTemporaryFiles(t *testing.T) {
	raw := encodePicture(t, "png", 1, 1)
	for _, scenario := range []string{"invalid name", "invalid header", "body failure", "cancelled body", "cancelled before start"} {
		t.Run(scenario, func(t *testing.T) {
			home := t.TempDir()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			name, want := "picture.png", ErrInvalidImage
			var reader io.Reader = bytes.NewReader(raw)
			switch scenario {
			case "invalid name":
				name, want = "../escape.png", ErrInvalidName
			case "invalid header":
				reader = bytes.NewReader(raw[:20])
			case "body failure":
				reader, want = io.MultiReader(bytes.NewReader(raw), failedHeaderReader{}), ErrUnavailable
			case "cancelled body":
				reader, want = cancelAtEOF{reader, cancel}, context.Canceled
			case "cancelled before start":
				cancel()
				want = context.Canceled
			}
			if _, err := Upload(ctx, home, name, reader); !errors.Is(err, want) {
				t.Fatalf("error %v, want %v", err, want)
			}
			if got := galleryNames(t, home); len(got) != 0 {
				t.Fatalf("failed upload left files: %v", got)
			}
		})
	}
}

func TestMutationLockWaitCanBeCancelled(t *testing.T) {
	if err := lockMutations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer unlockMutations()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- lockMutations(ctx) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("lock error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled mutation still waits for the lock")
	}
}

type zeroUploadReader struct{}

func (zeroUploadReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestUploadLargeOriginalHasBoundedAllocations(t *testing.T) {
	home := t.TempDir()
	raw := encodePicture(t, "png", 1, 1)
	const trailing = 32 << 20
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for _, name := range []string{"large.png", "duplicate.png"} {
		body := io.MultiReader(bytes.NewReader(raw), io.LimitReader(zeroUploadReader{}, trailing))
		result, err := Upload(context.Background(), home, name, body)
		if err != nil || result.Wallpaper.Name != "large.png" || result.Created != (name == "large.png") {
			t.Fatalf("large upload: %+v, %v", result, err)
		}
	}
	runtime.ReadMemStats(&after)
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
		t.Fatalf("32 MiB upload and duplicate allocated %d bytes", allocated)
	}
	info, err := os.Stat(filepath.Join(home, "wallpapers", "large.png"))
	if err != nil || info.Size() != int64(len(raw))+trailing {
		t.Fatalf("original size: %v, %v", info, err)
	}
	if got := galleryNames(t, home); !reflect.DeepEqual(got, []string{"large.png"}) {
		t.Fatalf("files: %v", got)
	}
}

func TestDeleteChecksNamedImage(t *testing.T) {
	home := t.TempDir()
	raw := encodePicture(t, "png", 1, 1)
	writePicture(t, home, "picture.png", raw)
	writePicture(t, home, "notes.txt", []byte("not an image"))
	if err := os.Mkdir(filepath.Join(home, "wallpapers", "folder.png"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		want error
	}{
		{"../picture.png", ErrInvalidName}, {".hidden", ErrInvalidName}, {"missing.png", ErrNotFound}, {"notes.txt", ErrInvalidImage}, {"folder.png", ErrNotFound},
	} {
		if err := Delete(context.Background(), home, tc.name); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", tc.name, err, tc.want)
		}
	}
	if err := Delete(context.Background(), home, "picture.png"); err != nil {
		t.Fatal(err)
	}
	if got := galleryNames(t, home); !reflect.DeepEqual(got, []string{"folder.png", "notes.txt"}) {
		t.Fatalf("deleted wrong files: %v", got)
	}
}

func TestUploadAndDeleteRefuseLinkedGallery(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	raw := encodePicture(t, "png", 1, 1)
	if err := os.WriteFile(filepath.Join(outside, "picture.png"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, "wallpapers")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink privilege unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if _, err := Upload(context.Background(), home, "new.png", bytes.NewReader(raw)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("linked gallery upload: %v", err)
	}
	if err := Delete(context.Background(), home, "picture.png"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("linked gallery delete: %v", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 1 || entries[0].Name() != "picture.png" {
		t.Fatalf("outside modified: %v, %v", entries, err)
	}
}

func TestDeleteRefusesLinkedImage(t *testing.T) {
	home := t.TempDir()
	writePicture(t, home, "picture.png", encodePicture(t, "png", 1, 1))
	if err := os.Symlink("picture.png", filepath.Join(home, "wallpapers", "link.png")); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink privilege unavailable: %v", err)
		}
		t.Fatal(err)
	}
	if err := Delete(context.Background(), home, "link.png"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("linked image delete: %v", err)
	}
	if got := galleryNames(t, home); !reflect.DeepEqual(got, []string{"link.png", "picture.png"}) {
		t.Fatalf("link or target deleted: %v", got)
	}
}

func TestUploadScanIOErrorDoesNotPublish(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows chmod does not remove read access")
	}
	home := t.TempDir()
	raw := encodePicture(t, "png", 1, 1)
	writePicture(t, home, "unreadable.png", raw)
	path := filepath.Join(home, "wallpapers", "unreadable.png")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(path, 0o600) }()
	if f, err := os.Open(path); err == nil {
		_ = f.Close()
		t.Skip("process can read mode-000 files")
	}
	if _, err := Upload(context.Background(), home, "new.png", bytes.NewReader(raw)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("scan failure: %v", err)
	}
	if got := galleryNames(t, home); !reflect.DeepEqual(got, []string{"unreadable.png"}) {
		t.Fatalf("failed scan published or left temporary file: %v", got)
	}
}

func TestMutationsRefuseInvalidGallery(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "wallpapers"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Upload(context.Background(), home, "image.png", bytes.NewReader(encodePicture(t, "png", 1, 1))); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("upload to non-directory: %v", err)
	}
	if err := Delete(context.Background(), home, "image.png"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("delete in non-directory: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(home, "wallpapers"))
	if err != nil || string(raw) != "keep" {
		t.Fatalf("invalid gallery was replaced: %q %v", raw, err)
	}
}

func TestUploadPreservesLinkedNameCollision(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	writePicture(t, home, "seed.png", encodePicture(t, "png", 2, 1))
	target := filepath.Join(outside, "target.txt")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "wallpapers", "picture.png")
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink privilege unavailable: %v", err)
		}
		t.Fatal(err)
	}
	result, err := Upload(context.Background(), home, "picture.png", bytes.NewReader(encodePicture(t, "png", 1, 1)))
	if err != nil || result.Wallpaper.Name != "picture-2.png" {
		t.Fatalf("linked collision: %+v %v", result, err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link overwritten: %v", err)
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "keep" {
		t.Fatalf("outside changed: %q %v", raw, err)
	}
}

type eofUploadReader struct {
	io.Reader
	eof  chan struct{}
	once sync.Once
}

func (r *eofUploadReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if errors.Is(err, io.EOF) {
		r.once.Do(func() { close(r.eof) })
	}
	return n, err
}

func TestCancelledPublicationWaitCleansTemporaryFile(t *testing.T) {
	home := t.TempDir()
	raw := encodePicture(t, "png", 1, 1)
	if err := lockMutations(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer unlockMutations()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &eofUploadReader{Reader: bytes.NewReader(raw), eof: make(chan struct{})}
	done := make(chan error, 1)
	go func() { _, err := Upload(ctx, home, "cancelled.png", reader); done <- err }()
	<-reader.eof
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("publication wait: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled upload still waits for publication")
	}
	if got := galleryNames(t, home); len(got) != 0 {
		t.Fatalf("cancelled publication left files: %v", got)
	}
}

func TestCancelledDeletePreservesImage(t *testing.T) {
	home := t.TempDir()
	writePicture(t, home, "keep.png", encodePicture(t, "png", 1, 1))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Delete(ctx, home, "keep.png"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled delete: %v", err)
	}
	if got := galleryNames(t, home); !reflect.DeepEqual(got, []string{"keep.png"}) {
		t.Fatalf("cancelled delete changed files: %v", got)
	}
}
