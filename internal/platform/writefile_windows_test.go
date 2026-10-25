//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func daclString(t *testing.T, path string) string {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo: %v", err)
	}
	return sd.String()
}

// ownerOnlyFile is a file whose protected DACL lets in only its owner and the
// system, in a folder that lets in more.
func ownerOnlyFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;FA;;;OW)(A;;FA;;;SY)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Skipf("cannot set a DACL here: %v", err)
	}
	return path
}

// Replacing a file that carries its own protected DACL keeps that DACL: a rename
// of a sibling would hand the file the folder's inherited permissions.
func TestWriteFileReplacingKeepsAnExplicitDACL(t *testing.T) {
	path := ownerOnlyFile(t)
	before := daclString(t, path)

	if _, err := WriteFileReplacing(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "new" {
		t.Fatalf("file = %q", got)
	}
	if after := daclString(t, path); after != before {
		t.Fatalf("DACL changed:\nbefore %s\nafter  %s", before, after)
	}
}

// The replacement has the file's DACL before it has any content, so the new
// content is never open to whoever the folder lets in, not even while it is
// written or after a crash leaves the temporary file behind.
func TestWriteFileReplacingTemporaryFileHasTheDACLWhileWritten(t *testing.T) {
	path := ownerOnlyFile(t)
	want := daclString(t, path)
	whileWriting(t, path, func(tmp string) {
		if got := daclString(t, tmp); got != want {
			t.Fatalf("temporary file DACL while writing:\ngot  %s\nwant %s", got, want)
		}
	})
}

// The replaced file's attributes survive.
func TestWriteFileReplacingKeepsTheHiddenAttribute(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hidden.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetFileAttributes(name, windows.FILE_ATTRIBUTE_HIDDEN); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteFileReplacing(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	attrs, err := windows.GetFileAttributes(name)
	if err != nil {
		t.Fatal(err)
	}
	if attrs&windows.FILE_ATTRIBUTE_HIDDEN == 0 {
		t.Fatalf("attributes = %#x, the hidden bit was lost", attrs)
	}
}
