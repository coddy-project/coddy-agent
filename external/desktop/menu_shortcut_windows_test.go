//go:build windows

package desktop

import (
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

// The Start menu link this user gets: written by the program itself, read
// back two ways - by walking the bytes, and by the shell's own COM API,
// which is the check a cross-compile cannot do.

// isolateMenuFolders points the link and its icon at folders of this test's
// own, so a suite neither writes into nor reads the Start menu and the
// application data of the person running it.
func isolateMenuFolders(t *testing.T) (programs, icons string) {
	t.Helper()
	programs = t.TempDir()
	icons = t.TempDir()
	wasPrograms, wasIcons := menuProgramsFolder, menuIconFolder
	menuProgramsFolder = func() (string, error) { return programs, nil }
	menuIconFolder = func() (string, error) { return icons, nil }
	t.Cleanup(func() {
		menuProgramsFolder, menuIconFolder = wasPrograms, wasIcons
	})
	return programs, icons
}

func TestTheMenuShortcutIsWrittenWhereTheShellReadsIt(t *testing.T) {
	programs, icons := isolateMenuFolders(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := InstallMenuShortcut(exe); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(programs, menuShortcutName))
	if err != nil {
		t.Fatalf("no shortcut was written: %v", err)
	}
	got := parseMenuShortcut(t, body)
	if got.target != exe {
		t.Fatalf("the shortcut opens %q, not %q", got.target, exe)
	}
	if got.args != "desktop" {
		t.Fatalf("the shortcut's arguments are %q: it would not open a window", got.args)
	}
	if got.workDir != filepath.Dir(exe) {
		t.Fatalf("the shortcut's working folder is %q, not the program's own %q", got.workDir, filepath.Dir(exe))
	}
	if got.show != linkShowMinNoAct {
		t.Fatalf("the shortcut's run mode is %d: the console would cover the window", got.show)
	}
	icon := filepath.Join(icons, "Coddy", menuIconName)
	if got.icon != icon+",0" {
		t.Fatalf("the shortcut's icon is %q, not %q", got.icon, icon+",0")
	}
	ico, err := os.ReadFile(icon)
	if err != nil {
		t.Fatalf("no managed icon was written: %v", err)
	}
	if len(ico) < 6 || !bytes.Equal(ico[:4], []byte{0, 0, 1, 0}) || ico[4] != 2 || ico[5] != 0 {
		t.Fatalf("the managed icon is not the .ico it should be: %x", ico[:6])
	}
}

// A repair run rewrites the link for the binary it is given now: the
// migration of an old install depends on that.
func TestTheShortcutFollowsTheBinaryItNames(t *testing.T) {
	programs, _ := isolateMenuFolders(t)
	exe := `C:\Tools\Coddy\coddy.exe`
	if err := InstallMenuShortcut(exe); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(programs, menuShortcutName))
	if err != nil {
		t.Fatal(err)
	}
	if got := parseMenuShortcut(t, body).target; got != exe {
		t.Fatalf("the shortcut opens %q, not %q", got, exe)
	}
}

func TestTheShortcutNeedsAnAbsolutePath(t *testing.T) {
	isolateMenuFolders(t)
	if err := InstallMenuShortcut(`coddy.exe`); err == nil {
		t.Fatal("a relative executable was accepted: the menu cannot find it later")
	}
}

// The shell reads the file back: the fields the resolver acts on are the
// ones the link claims.
func TestTheShellReadsTheShortcutBack(t *testing.T) {
	ps, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Skip("powershell is not there to ask the shell what the link opens")
	}
	programs, icons := isolateMenuFolders(t)
	exe := filepath.Join(t.TempDir(), "Папка с пробелом", "программа.exe")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("test"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallMenuShortcut(exe); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(programs, menuShortcutName)
	script := "[Console]::OutputEncoding=[System.Text.Encoding]::UTF8;$s=(New-Object -ComObject WScript.Shell).CreateShortcut('" +
		strings.ReplaceAll(link, "'", "''") +
		"');Write-Output ($s.TargetPath,$s.Arguments,$s.WorkingDirectory,$s.WindowStyle,$s.IconLocation -join [char]30)"
	out, err := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", script).Output()
	if err != nil {
		t.Fatalf("the shell could not read the shortcut: %v", err)
	}
	fields := strings.Split(strings.TrimSpace(string(out)), "\x1e")
	if len(fields) != 5 {
		t.Fatalf("the shell read %q back", strings.TrimSpace(string(out)))
	}
	if !strings.EqualFold(fields[0], exe) {
		t.Fatalf("the shell opens %q, not %q", fields[0], exe)
	}
	if fields[1] != "desktop" {
		t.Fatalf("the shell runs the program with %q", fields[1])
	}
	if !strings.EqualFold(fields[2], filepath.Dir(exe)) {
		t.Fatalf("the shell starts in %q", fields[2])
	}
	if fields[3] != "7" {
		t.Fatalf("the shell starts the link with window mode %q", fields[3])
	}
	if !strings.Contains(strings.ToLower(fields[4]), "coddy-desktop.ico") {
		t.Fatalf("the shell draws the icon from %q", fields[4])
	}
	if !strings.HasPrefix(fields[4], filepath.Join(icons, "Coddy")) {
		t.Fatalf("the icon is not the managed one: %q", fields[4])
	}
}

// parsedShortcut is the part of a shell link the test reads back.
type parsedShortcut struct {
	target, args, workDir, icon, name string
	show                              uint32
}

func parseMenuShortcut(t *testing.T, b []byte) parsedShortcut {
	t.Helper()
	var got parsedShortcut
	if len(b) < 76 || binary.LittleEndian.Uint32(b) != 0x4c {
		t.Fatalf("not a shell link: %x", b[:min(16, len(b))])
	}
	clsid := []byte{0x01, 0x14, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00,
		0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}
	if !bytes.Equal(b[4:20], clsid) {
		t.Fatalf("the file is a link of a different class: %x", b[4:20])
	}
	flags := binary.LittleEndian.Uint32(b[20:])
	got.show = binary.LittleEndian.Uint32(b[60:])
	off := 76
	if flags&linkHasIDList != 0 {
		n := int(binary.LittleEndian.Uint16(b[off:]))
		off += 2 + n
	}
	if flags&linkHasLinkInfo != 0 {
		info := b[off:]
		got.target = utf16String(t, info[int(binary.LittleEndian.Uint32(info[0x1c:])):])
		off += int(binary.LittleEndian.Uint32(info))
	}
	// The counted strings, in flag order: name, relative path, working
	// directory, arguments, icon location.
	for _, f := range []struct {
		flag uint32
		dst  *string
	}{
		{linkHasName, &got.name},
		{0x08, new(string)},
		{linkHasWorkDir, &got.workDir},
		{linkHasArgs, &got.args},
		{linkHasIcon, &got.icon},
	} {
		if flags&f.flag == 0 {
			continue
		}
		n := int(binary.LittleEndian.Uint16(b[off:]))
		u16dst(t, f.dst, b[off+2:off+2+2*n])
		off += 2 + 2*n
	}
	return got
}

// utf16String reads a NUL-terminated UTF-16LE string.
func utf16String(t *testing.T, b []byte) string {
	t.Helper()
	var u []uint16
	for len(b) >= 2 && binary.LittleEndian.Uint16(b) != 0 {
		u = append(u, binary.LittleEndian.Uint16(b))
		b = b[2:]
	}
	if len(b) < 2 {
		t.Fatal("the string is not terminated")
	}
	return string(utf16.Decode(u))
}

// u16dst decodes n UTF-16 code units into dst.
func u16dst(t *testing.T, dst *string, b []byte) {
	t.Helper()
	u := make([]uint16, len(b)/2)
	for i := range u {
		u[i] = binary.LittleEndian.Uint16(b[2*i:])
	}
	*dst = string(utf16.Decode(u))
}
