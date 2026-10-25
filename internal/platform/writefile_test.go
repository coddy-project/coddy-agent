package platform

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode/utf8"
)

// The fault seam is process-wide, so the tests that arm it are not parallel.

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func noTemporaryFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

// A write that fails after half the bytes leaves the old content byte for byte
// and no temporary file: the failure a full disk produces.
func TestWriteFileReplacingKeepsOldContentWhenTheWriteFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("old content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	restore := InjectWriteFault(4)
	_, err := WriteFileReplacing(path, []byte("brand new content that is longer\n"), 0o644)
	restore()

	if !errors.Is(err, ErrInjectedWriteFault) {
		t.Fatalf("err = %v, want ErrInjectedWriteFault", err)
	}
	if got := readFile(t, path); got != "old content\n" {
		t.Fatalf("file = %q, want the old content", got)
	}
	noTemporaryFiles(t, dir)
}

// The same failure on a path that does not exist yet creates nothing.
func TestWriteFileReplacingLeavesNoNewFileWhenTheWriteFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.txt")

	restore := InjectWriteFault(0)
	_, err := WriteFileReplacing(path, []byte("content"), 0o644)
	restore()

	if err == nil {
		t.Fatal("want an error")
	}
	if _, statErr := os.Stat(path); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("target exists after a failed create: %v", statErr)
	}
	noTemporaryFiles(t, dir)
}

func TestWriteFileReplacingReplacesContent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := WriteFileReplacing(path, []byte("new"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "new" {
		t.Fatalf("file = %q", got)
	}
	if note := report.Note(); note != "" {
		t.Fatalf("note = %q for a single-name file", note)
	}
}

// A new file gets perm under the umask; an existing one keeps its own mode.
func TestWriteFileReplacingModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	dir := t.TempDir()

	previous := setUmask(0o077)
	created := filepath.Join(dir, "created.txt")
	_, err := WriteFileReplacing(created, []byte("x"), 0o666)
	setUmask(previous)
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

func modeOf(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// A read-only file in a writable folder is refused and unchanged: a rename
// needs write access to the folder, not to the file.
func TestWriteFileReplacingRefusesAReadOnlyFile(t *testing.T) {
	if runtime.GOOS != "windows" && os.Geteuid() == 0 {
		t.Skip("root writes a 0444 file")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "readonly.txt")
	if err := os.WriteFile(path, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o644) })

	if _, err := WriteFileReplacing(path, []byte("overwrite"), 0o644); err == nil {
		t.Fatal("want a refusal")
	}
	if got := readFile(t, path); got != "keep" {
		t.Fatalf("file = %q, want unchanged", got)
	}
	noTemporaryFiles(t, dir)
}

// Writing through a link changes the target and keeps the link; a link whose
// target does not exist yet creates it.
func TestWriteFileReplacingThroughSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link.txt")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := WriteFileReplacing(link, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link was replaced: %v %v", info, err)
	}
	if got := readFile(t, target); got != "new" {
		t.Fatalf("target = %q, want new", got)
	}

	dangling := filepath.Join(dir, "dangling.txt")
	missing := filepath.Join(dir, "missing.txt")
	if err := os.Symlink("missing.txt", dangling); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := WriteFileReplacing(dangling, []byte("created"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, missing); got != "created" {
		t.Fatalf("created target = %q", got)
	}
}

// A file with two names becomes a new file under the written name: the other
// name keeps the old content, and the report says the file had two names.
func TestWriteFileReplacingReportsAHardLinkedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.txt")
	other := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, other); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}

	report, err := WriteFileReplacing(path, []byte("new"), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, other); got != "old" {
		t.Fatalf("other name = %q, want the old content", got)
	}
	if report.Links != 2 || !strings.Contains(report.Note(), "2 names") {
		t.Fatalf("report = %+v, note = %q", report, report.Note())
	}
}

// A name close to the 255-byte limit of a component stays writable: the
// temporary file keeps only the start of it.
func TestWriteFileReplacingLongName(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), strings.Repeat("n", 240)+".txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Skipf("this file system refuses the name itself: %v", err)
	}
	if _, err := WriteFileReplacing(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "new" {
		t.Fatalf("file = %q", got)
	}
	if stem := tempStem(strings.Repeat("ж", 100)); len(stem) > maxTempStem || !utf8.ValidString(stem) {
		t.Fatalf("tempStem cut a rune or kept too much: %d bytes", len(stem))
	}
}

// Without a folder to write the temporary file in, the write fails and says why.
func TestWriteFileReplacingNeedsTheFolder(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "missing", "file.txt")
	if _, err := WriteFileReplacing(path, []byte("x"), 0o644); err == nil {
		t.Fatal("want an error for a missing folder")
	}
}
