//go:build unix

package gitws

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fifoAt makes a FIFO with no writer at path. The cleanup opens it for both
// directions so an open still blocked on it (the failure this guards) ends
// with the test instead of living on.
func fifoAt(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	t.Cleanup(func() {
		if f, err := os.OpenFile(path, os.O_RDWR, 0); err == nil {
			_ = f.Close()
		}
	})
}

// The comparison asks gitDirs before it runs git, and gitDirs reads the .git
// pointer and the commondir pointer itself, outside any cancellable call. A
// FIFO in either place used to hold the caller in the open for good - the
// goal gate with the turn lock held. They are refused at once now: a path that
// is not a regular file answers "not a repository", never a hang.
func TestComparisonDoesNotBlockOnAFifoPointerFile(t *testing.T) {
	const head = "0123456789abcdef0123456789abcdef01234567"
	cases := []struct {
		name  string
		build func(t *testing.T, dir string)
	}{
		{"a .git that is a FIFO", func(t *testing.T, dir string) {
			fifoAt(t, filepath.Join(dir, ".git"))
		}},
		{"a commondir that is a FIFO", func(t *testing.T, dir string) {
			if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
			fifoAt(t, filepath.Join(dir, ".git", "commondir"))
		}},
	}
	for _, tc := range cases {
		for _, call := range []struct {
			name string
			run  func(ctx context.Context, dir string) error
		}{
			{"Baseline", func(ctx context.Context, dir string) error {
				_, err := Baseline(ctx, dir, []string{"migrations"})
				return err
			}},
			{"ChangedSince", func(ctx context.Context, dir string) error {
				_, err := ChangedSince(ctx, dir, FileBaseline{Head: head}, []string{"migrations"})
				return err
			}},
		} {
			t.Run(tc.name+"/"+call.name, func(t *testing.T) {
				dir := t.TempDir()
				tc.build(t, dir)
				done := make(chan error, 1)
				go func() { done <- call.run(context.Background(), dir) }()
				select {
				case err := <-done:
					if !errors.Is(err, ErrNotRepository) {
						t.Fatalf("err = %v, want ErrNotRepository", err)
					}
				case <-time.After(3 * time.Second):
					t.Fatalf("%s blocked on %s", call.name, strings.TrimPrefix(tc.name, "a "))
				}
			})
		}
	}
}
