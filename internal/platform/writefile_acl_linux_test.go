package platform

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// aclXattr encodes a POSIX access ACL as the kernel stores it: a version, then
// (tag, perm, id) entries ordered by tag.
func aclXattr(entries ...[3]uint32) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, uint32(2))
	for _, e := range entries {
		_ = binary.Write(&b, binary.LittleEndian, uint16(e[0]))
		_ = binary.Write(&b, binary.LittleEndian, uint16(e[1]))
		_ = binary.Write(&b, binary.LittleEndian, e[2])
	}
	return b.Bytes()
}

// A file shared with one more user through an ACL keeps that ACL. Without it
// the mode would carry the ACL's mask in its group bits and open the file to
// the owning group, which the ACL kept out.
func TestWriteFileReplacingKeepsAPOSIXACL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.txt")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	const none = 0xffffffff
	acl := aclXattr(
		[3]uint32{0x01, 6, none},  // owner rw
		[3]uint32{0x02, 6, 65534}, // user nobody rw
		[3]uint32{0x04, 0, none},  // owning group nothing
		[3]uint32{0x10, 6, none},  // mask rw
		[3]uint32{0x20, 0, none},  // others nothing
	)
	if err := unix.Setxattr(path, posixACL, acl, 0); err != nil {
		t.Skipf("no POSIX ACLs here: %v", err)
	}
	before := readACL(t, path)
	mode := modeOf(t, path)

	if _, err := WriteFileReplacing(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "new" {
		t.Fatalf("file = %q", got)
	}
	if after := readACL(t, path); !bytes.Equal(after, before) {
		t.Fatalf("ACL changed:\nbefore %x\nafter  %x", before, after)
	}
	if got := modeOf(t, path); got != mode {
		t.Fatalf("mode = %o, want %o", got, mode)
	}
}

// A file with no ACL in a folder with a default ACL stays without one: its
// replacement is created under the default ACL, and the file's mode would then
// open it to the named users of the folder's default.
func TestWriteFileReplacingDropsTheFolderDefaultACL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "private.txt")
	if err := os.WriteFile(path, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	const none = 0xffffffff
	def := aclXattr(
		[3]uint32{0x01, 7, none},  // owner rwx
		[3]uint32{0x02, 7, 65534}, // user nobody rwx
		[3]uint32{0x04, 5, none},  // owning group r-x
		[3]uint32{0x10, 7, none},  // mask rwx
		[3]uint32{0x20, 0, none},  // others nothing
	)
	if err := unix.Setxattr(dir, "system.posix_acl_default", def, 0); err != nil {
		t.Skipf("no POSIX ACLs here: %v", err)
	}

	if _, err := WriteFileReplacing(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := unix.Getxattr(path, posixACL, make([]byte, 256)); !errors.Is(err, unix.ENODATA) {
		t.Fatalf("the replacement carries an ACL the file did not have (err = %v)", err)
	}
	if mode := modeOf(t, path); mode != 0o640 {
		t.Fatalf("mode = %o, want 640", mode)
	}
}

func readACL(t *testing.T, path string) []byte {
	t.Helper()
	buf := make([]byte, 256)
	n, err := unix.Getxattr(path, posixACL, buf)
	if err != nil {
		t.Fatalf("read the ACL of %s: %v", path, err)
	}
	return buf[:n]
}
