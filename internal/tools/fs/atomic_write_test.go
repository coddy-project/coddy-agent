package fs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// The failures of write, edit and apply_patch are the scenarios of
// features/atomic_file_writes.feature; these tests cover what the feature does
// not: the copy of a move across disks and the note on a file with two names.
// They arm the process-wide write fault of internal/platform, so none of them is
// parallel: Go runs the serial tests of a package before it releases the
// parallel ones.

const keptContent = "line one\nline two\nline three\n"

func failingWriteFixture(t *testing.T) (dir, path string, env *tooling.Env) {
	t.Helper()
	dir = t.TempDir()
	path = filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte(keptContent), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir, path, &tooling.Env{CWD: dir}
}

func assertKept(t *testing.T, dir, path string, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want an error from the failed write")
	}
	if !errors.Is(err, platform.ErrInjectedWriteFault) {
		t.Fatalf("err = %v, want the write fault", err)
	}
	if !strings.Contains(err.Error(), "the file was left as it was") {
		t.Fatalf("err = %q, want it to say the file was left as it was", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != keptContent {
		t.Fatalf("file = %q, want the old content", got)
	}
	if leftover, err := temporaryFileIn(dir); err != nil || leftover != "" {
		t.Fatalf("temporary file left behind: %q (%v)", leftover, err)
	}
}

// A cross-device move copies the file over its destination; a failed copy keeps
// the destination as it was and keeps the source.
func TestMoveCopyLeavesTheDestinationWhenTheWriteFails(t *testing.T) {
	dir, dst, _ := failingWriteFixture(t)
	src := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(src, []byte("source content that is long"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer platform.InjectWriteFault(5)()

	_, err := copyThenRemove(src, dst)
	assertKept(t, dir, dst, err)
	if _, statErr := os.Stat(src); statErr != nil {
		t.Fatalf("source lost after a failed copy: %v", statErr)
	}
}

// The copy of a move across disks reports a destination that had two names, as
// the write tool does, and keeps the source until the copy is in place.
func TestMoveCopyReportsADestinationWithTwoNames(t *testing.T) {
	dir, dst, _ := failingWriteFixture(t)
	if err := os.Link(dst, filepath.Join(dir, "other.txt")); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	src := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(src, []byte("moved"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := copyThenRemove(src, dst)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withNote("moved", report), "2 names") {
		t.Fatalf("result = %q, want the two-names note", withNote("moved", report))
	}
	if _, statErr := os.Stat(src); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("source still there after a finished move: %v", statErr)
	}
}

// A move whose destination is the source under another name leaves it alone:
// the copy would replace the file by itself, and the remove would then take
// the only copy.
func TestMoveCopyRefusesADestinationThatIsTheSource(t *testing.T) {
	dir, src, _ := failingWriteFixture(t)
	dst := filepath.Join(dir, "link.txt")
	if err := os.Symlink(src, dst); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := copyThenRemove(src, dst); err == nil || !strings.Contains(err.Error(), "same file") {
		t.Fatalf("err = %v, want a refusal naming the same file", err)
	}
	got, err := os.ReadFile(src)
	if err != nil || string(got) != keptContent {
		t.Fatalf("source = %q (%v), want it untouched", got, err)
	}
}

func TestWriteToolReportsAFileWithTwoNames(t *testing.T) {
	dir, path, env := failingWriteFixture(t)
	other := filepath.Join(dir, "other.txt")
	if err := os.Link(path, other); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}

	args, err := jsonArgs(writeArgs{Path: path, Content: "new"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := executeWrite(context.Background(), args, env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2 names") {
		t.Fatalf("result = %q, want the two-names note", out)
	}
}
