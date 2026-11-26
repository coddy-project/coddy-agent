//go:build windows

package desktop

import (
	"bytes"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Use a short prefix instead of the test name in the temporary paths, so
// this tests Unicode separately from the shell's legacy MAX_PATH limits.
func isolateMenuFolders(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("", "cd-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	abs, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	wasPrograms, wasIcons := menuProgramsFolder, menuIconFolder
	menuProgramsFolder = func() (string, error) { return filepath.Join(abs, "Меню"), nil }
	menuIconFolder = func() (string, error) { return filepath.Join(abs, "Данные, 日本"), nil }
	t.Cleanup(func() { menuProgramsFolder, menuIconFolder = wasPrograms, wasIcons })
	return abs
}

// A real PE file lets the shell build the same target metadata as an install.
// A distinct copy (not a hard link) also makes the repair identity check useful.
func copyShortcutTarget(t *testing.T, dst string) {
	t.Helper()
	src, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInstallMenuShortcutUnicode(t *testing.T) {
	root := isolateMenuFolders(t)
	checkMenuShortcut(t, root, filepath.Join(root, "Папка с пробелом", "程序.exe"))
}

func TestInstallMenuShortcutRepairsTarget(t *testing.T) {
	root := isolateMenuFolders(t)
	checkMenuShortcut(t, root, filepath.Join(root, "old", "coddy.exe"))
	checkMenuShortcut(t, root, filepath.Join(root, "new", "coddy.exe"))
}

func TestInstallMenuShortcutNeedsAbsolutePath(t *testing.T) {
	isolateMenuFolders(t)
	if err := InstallMenuShortcut(`coddy.exe`); err == nil {
		t.Fatal("a relative executable was accepted")
	}
}

func checkMenuShortcut(t *testing.T, root, exe string) {
	t.Helper()
	link := filepath.Join(root, "Меню", "Coddy Desktop.lnk")
	icon := filepath.Join(root, "Данные, 日本", "Coddy", "coddy-desktop.ico")
	units := func(s string) int { return len(utf16.Encode([]rune(s))) }
	t.Logf("path lengths (UTF-16 code units): link=%d target=%d workdir=%d icon=%d",
		units(link), units(exe), units(filepath.Dir(exe)), units(icon))
	copyShortcutTarget(t, exe)
	if err := InstallMenuShortcut(exe); err != nil {
		t.Fatal(err)
	}
	got := readMenuShortcut(t, link)
	assertSameFile(t, got.target, exe)
	assertSameFile(t, got.workDir, filepath.Dir(exe))
	assertSameFile(t, got.icon, icon)
	if got.args != "desktop" || got.index != 0 || got.show != 7 || got.description != "Coddy agent in a window" {
		t.Errorf("shell read arguments=%q, icon index=%d, show command=%d, description=%q; want desktop, 0, SW_SHOWMINNOACTIVE (7), Coddy agent in a window",
			got.args, got.index, got.show, got.description)
	}
	ico, err := os.ReadFile(icon)
	if err != nil {
		t.Fatalf("no managed icon was written: %v", err)
	}
	if !bytes.HasPrefix(ico, []byte{0, 0, 1, 0, 2, 0}) {
		t.Fatalf("managed icon does not have a two-image ICO header: %x", ico[:min(6, len(ico))])
	}
}

type savedMenuShortcut struct {
	target, workDir, args, description, icon string
	index, show                              int32
}

// Open the file in a fresh native Shell object. This uses only the reader
// methods and the saved file, never the writer's in-memory state or layout.
func readMenuShortcut(t *testing.T, path string) savedMenuShortcut {
	t.Helper()
	check := func(method string, result uintptr) {
		t.Helper()
		if uint32(result) != 0 { // GetPath can return S_FALSE when no path is available.
			t.Fatalf("%s returned HRESULT 0x%08x", method, uint32(result))
		}
	}
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	var got savedMenuShortcut
	err = withShellLink(func(link *shellLinkW, file *persistFile) error {
		hr, _, _ := syscall.SyscallN(file.vtbl.load, uintptr(unsafe.Pointer(file)), uintptr(unsafe.Pointer(name)), 0) // STGM_READ
		check("IPersistFile.Load", hr)

		self := unsafe.Pointer(link)
		var buf [windows.MAX_PATH]uint16
		hr, _, _ = syscall.SyscallN(link.vtbl.getPath, uintptr(self), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), 0, 0)
		check("IShellLinkW.GetPath", hr)
		got.target = windows.UTF16ToString(buf[:])

		for _, field := range []struct {
			name string
			proc uintptr
			dest *string
		}{
			{"GetWorkingDirectory", link.vtbl.getWorkingDirectory, &got.workDir},
			{"GetArguments", link.vtbl.getArguments, &got.args},
			{"GetDescription", link.vtbl.getDescription, &got.description},
		} {
			clear(buf[:])
			hr, _, _ = syscall.SyscallN(field.proc, uintptr(self), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
			check("IShellLinkW."+field.name, hr)
			*field.dest = windows.UTF16ToString(buf[:])
		}

		clear(buf[:])
		hr, _, _ = syscall.SyscallN(link.vtbl.getIconLocation, uintptr(self), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&got.index)))
		check("IShellLinkW.GetIconLocation", hr)
		got.icon = windows.UTF16ToString(buf[:])
		hr, _, _ = syscall.SyscallN(link.vtbl.getShowCmd, uintptr(self), uintptr(unsafe.Pointer(&got.show)))
		check("IShellLinkW.GetShowCmd", hr)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// The shell may expand Windows 8.3 short names or change path casing.
func assertSameFile(t *testing.T, got, want string) {
	t.Helper()
	gotInfo, err := os.Stat(got)
	if err != nil {
		t.Fatalf("cannot stat shell path %q: %v", got, err)
	}
	wantInfo, err := os.Stat(want)
	if err != nil {
		t.Fatalf("cannot stat expected path %q: %v", want, err)
	}
	if !os.SameFile(gotInfo, wantInfo) {
		t.Fatalf("shell path %q does not identify %q", got, want)
	}
}
