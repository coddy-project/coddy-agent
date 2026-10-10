package platform

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// readOrSkip reads the room of path, and skips the test on a platform that
// has no way to ask (ErrDiskSpaceUnavailable from the fallback file).
func readOrSkip(t *testing.T, path string) DiskSpace {
	t.Helper()
	ds, err := ReadDiskSpace(path)
	if errors.Is(err, ErrDiskSpaceUnavailable) {
		t.Skipf("no free-space probe on this platform: %v", err)
	}
	if err != nil {
		t.Fatalf("ReadDiskSpace(%q): %v", path, err)
	}
	return ds
}

// The volume of an existing folder reports a size, some of it free (never
// more than the volume holds), and a name for itself.
func TestReadDiskSpaceOfAnExistingFolder(t *testing.T) {
	ds := readOrSkip(t, t.TempDir())
	if ds.TotalBytes == 0 {
		t.Fatalf("TotalBytes = 0: %+v", ds)
	}
	if ds.FreeBytes > ds.TotalBytes {
		t.Fatalf("FreeBytes %d is more than TotalBytes %d", ds.FreeBytes, ds.TotalBytes)
	}
	if ds.Volume == "" {
		t.Fatalf("Volume is empty: %+v", ds)
	}
}

// A folder Coddy has not made yet (the sessions folder of a first start) lives
// on the volume of its nearest existing ancestor, and so does a path below a
// file, which no folder can come out of.
func TestReadDiskSpaceWalksUpToTheNearestExistingFolder(t *testing.T) {
	root := t.TempDir()
	want := readOrSkip(t, root)

	missing := filepath.Join(root, "not", "made", "yet")
	if got := readOrSkip(t, missing); got.Volume != want.Volume || got.TotalBytes != want.TotalBytes {
		t.Fatalf("missing path: %+v, want the volume of its ancestor %+v", got, want)
	}

	file := filepath.Join(root, "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readOrSkip(t, file); got.Volume != want.Volume {
		t.Fatalf("file path: volume %q, want %q", got.Volume, want.Volume)
	}
	if got := readOrSkip(t, filepath.Join(file, "child")); got.Volume != want.Volume {
		t.Fatalf("path below a file: volume %q, want %q", got.Volume, want.Volume)
	}
}

// Two folders on one volume name it the same way, which is how a caller
// tells the sessions folder and the home folder apart from the one volume
// they often share.
func TestReadDiskSpaceNamesOneVolumeTheSameWay(t *testing.T) {
	a := t.TempDir()
	b := filepath.Join(a, "inner")
	if err := os.Mkdir(b, 0o755); err != nil {
		t.Fatal(err)
	}
	if va, vb := readOrSkip(t, a).Volume, readOrSkip(t, b).Volume; va != vb {
		t.Fatalf("volumes differ for nested folders: %q vs %q", va, vb)
	}
}

// A relative path is read from the working directory, like every other path.
func TestReadDiskSpaceAcceptsARelativePath(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	want := readOrSkip(t, root)
	if got := readOrSkip(t, "."); got.Volume != want.Volume {
		t.Fatalf("relative path: volume %q, want %q", got.Volume, want.Volume)
	}
}

func TestReadDiskSpaceRefusesAnEmptyPath(t *testing.T) {
	for _, p := range []string{"", "   "} {
		if _, err := ReadDiskSpace(p); err == nil || errors.Is(err, ErrDiskSpaceUnavailable) {
			t.Fatalf("ReadDiskSpace(%q) = %v, want a plain error about the empty path", p, err)
		}
	}
}
