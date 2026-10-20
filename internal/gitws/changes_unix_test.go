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

// fileHash looks at a path (Lstat) and opens it afterwards. A FIFO renamed over
// a regular file between the two used to hold the open for good, again with the
// goal gate's turn lock held. The open is non-blocking and checked on the
// opened file now: the swap answers with an error, never a hang and never
// "unchanged". swapAt is the call of fileHash the swap lands in.
func TestComparisonDoesNotBlockOnAFileSwappedForAFifo(t *testing.T) {
	const name = "migrations/0001_init.sql"
	cases := []struct {
		name   string
		swapAt int
		run    func(ctx context.Context, dir string, base FileBaseline) error
	}{
		{"Baseline", 1, func(ctx context.Context, dir string, _ FileBaseline) error {
			_, err := Baseline(ctx, dir, migrationFolders)
			return err
		}},
		{"ChangedSince candidate", 1, func(ctx context.Context, dir string, base FileBaseline) error {
			_, err := ChangedSince(ctx, dir, base, migrationFolders)
			return err
		}},
		{"ChangedSince dirty at the start", 2, func(ctx context.Context, dir string, base FileBaseline) error {
			_, err := ChangedSince(ctx, dir, base, migrationFolders)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := commitRepo(t, map[string]string{name: "create table users();\n"})
			writeFile(t, dir, name, "create table users(id int);\n")
			base := mustBaseline(t, dir)
			path := filepath.Join(dir, filepath.FromSlash(name))
			spare := filepath.Join(dir, "spare.fifo")
			fifoAt(t, spare)
			t.Cleanup(func() { // the FIFO sits at path after the swap
				if f, err := os.OpenFile(path, os.O_RDWR, 0); err == nil {
					_ = f.Close()
				}
			})
			calls := 0
			afterInspect = func(p string) {
				if p != path {
					return
				}
				if calls++; calls == tc.swapAt {
					if err := os.Rename(spare, p); err != nil {
						t.Errorf("swap: %v", err)
					}
				}
			}
			t.Cleanup(func() { afterInspect = nil })

			done := make(chan error, 1)
			go func() { done <- tc.run(context.Background(), dir, base) }()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "not a regular file") {
					t.Fatalf("err = %v, want a refusal of the swapped file", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("the comparison blocked on a file swapped for a FIFO")
			}
		})
	}
}

// fileHash looks at a path (Lstat) and opens it afterwards, through a root
// that follows a link inside it. A symlink renamed over a regular file between
// the two used to be opened as the copy it points at, and a copy that holds the
// text the comparison expects reads as "unchanged" while the migration itself is
// gone. The opened file must be the one looked at now: the swap answers with an
// error, never with the copy's hash. linked is the text of the file the link points
// at: what makes the swap read as no change at that step.
func TestComparisonDoesNotFollowASymlinkSwappedInAfterTheLook(t *testing.T) {
	const (
		name      = "migrations/0001_init.sql"
		committed = "create table users();\n"
		dirty     = "create table users(id int);\n"
	)
	cases := []struct {
		name   string
		swapAt int
		linked string
		run    func(ctx context.Context, dir string, base FileBaseline) error
	}{
		{"Baseline", 1, committed, func(ctx context.Context, dir string, _ FileBaseline) error {
			_, err := Baseline(ctx, dir, migrationFolders)
			return err
		}},
		{"ChangedSince candidate", 1, committed, func(ctx context.Context, dir string, base FileBaseline) error {
			_, err := ChangedSince(ctx, dir, base, migrationFolders)
			return err
		}},
		{"ChangedSince dirty at the start", 2, dirty, func(ctx context.Context, dir string, base FileBaseline) error {
			_, err := ChangedSince(ctx, dir, base, migrationFolders)
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := commitRepo(t, map[string]string{name: committed})
			writeFile(t, dir, name, dirty)
			base := mustBaseline(t, dir)
			path := filepath.Join(dir, filepath.FromSlash(name))
			// The link points into the root, where a root follows it.
			writeFile(t, dir, "spare.sql", tc.linked)
			calls := 0
			afterInspect = func(p string) {
				if p != path {
					return
				}
				if calls++; calls != tc.swapAt {
					return
				}
				link := p + ".swap"
				if err := os.Symlink("../spare.sql", link); err != nil {
					t.Errorf("symlink: %v", err)
					return
				}
				if err := os.Rename(link, p); err != nil {
					t.Errorf("swap: %v", err)
				}
			}
			t.Cleanup(func() { afterInspect = nil })

			err := tc.run(context.Background(), dir, base)
			if err == nil || !strings.Contains(err.Error(), "was replaced") {
				t.Fatalf("err = %v, want a refusal of the file swapped for a link", err)
			}
		})
	}
}
