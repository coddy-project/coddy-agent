//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

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

// The replacement has the file's DACL while it is written, so the new content
// is not open to whoever the folder lets in, nor after a crash leaves the
// temporary file behind.
func TestWriteFileReplacingTemporaryFileHasTheDACLWhileWritten(t *testing.T) {
	path := ownerOnlyFile(t)
	want := daclString(t, path)
	whileWriting(t, path, func(tmp string) {
		if got := daclString(t, tmp); got != want {
			t.Fatalf("temporary file DACL while writing:\ngot  %s\nwant %s", got, want)
		}
	})
}

// A DACL that inherits from the folder and adds an entry of its own keeps both,
// each once: the replacement is in the same folder.
func TestWriteFileReplacingKeepsAnInheritingDACLWithItsOwnEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:(A;;FR;;;BU)")
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.UNPROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Skipf("cannot set a DACL here: %v", err)
	}
	before := daclString(t, path)

	if _, err := WriteFileReplacing(path, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if after := daclString(t, path); after != before {
		t.Fatalf("DACL changed:\nbefore %s\nafter  %s", before, after)
	}
}

// The replaced file's hidden attribute and creation time survive.
func TestWriteFileReplacingKeepsTheAttributesAndTheCreationTime(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hidden.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	created := windows.NsecToFiletime(time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC).UnixNano())
	err = windows.SetFileTime(windows.Handle(f.Fd()), &created, nil, nil)
	_ = f.Close()
	if err != nil {
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
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Sys().(*syscall.Win32FileAttributeData).CreationTime; got != syscall.Filetime(created) {
		t.Fatalf("creation time = %v, want %v", got, created)
	}
}
