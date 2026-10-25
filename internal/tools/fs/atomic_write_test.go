package fs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// These tests arm the process-wide write fault of internal/platform, so none
// of them is parallel: Go runs the serial tests of a package before it releases
// the parallel ones.

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

func marshalArgs(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
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
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestWriteToolLeavesTheFileWhenTheWriteFails(t *testing.T) {
	dir, path, env := failingWriteFixture(t)
	defer platform.InjectWriteFault(5)()

	_, err := executeWrite(context.Background(), marshalArgs(t, writeArgs{Path: path, Content: "replacement content that is long"}), env)
	assertKept(t, dir, path, err)
}

func TestEditToolLeavesTheFileWhenTheWriteFails(t *testing.T) {
	dir, path, env := failingWriteFixture(t)
	defer platform.InjectWriteFault(5)()

	_, err := executeEdit(context.Background(), marshalArgs(t, editArgs{Path: path, OldString: "line two", NewString: "line 2 rewritten"}), env)
	assertKept(t, dir, path, err)
}

func TestApplyPatchToolLeavesTheFileWhenTheWriteFails(t *testing.T) {
	dir, path, env := failingWriteFixture(t)
	defer platform.InjectWriteFault(5)()

	patch := "--- a/file.txt\n+++ b/file.txt\n@@ -1,3 +1,3 @@\n line one\n-line two\n+line 2\n line three\n"
	_, err := executeApplyPatch(context.Background(), marshalArgs(t, map[string]string{"path": path, "patch": patch}), env)
	assertKept(t, dir, path, err)
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

func TestWriteToolReportsAFileWithTwoNames(t *testing.T) {
	dir, path, env := failingWriteFixture(t)
	other := filepath.Join(dir, "other.txt")
	if err := os.Link(path, other); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}

	out, err := executeWrite(context.Background(), marshalArgs(t, writeArgs{Path: path, Content: "new"}), env)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "2 names") {
		t.Fatalf("result = %q, want the two-names note", out)
	}
}
