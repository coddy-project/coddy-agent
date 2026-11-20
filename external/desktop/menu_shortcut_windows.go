//go:build windows

package desktop

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// The Start menu entry is a per-user shell link, saved by the Windows shell
// itself so the shell owns the encoding of every field, Unicode included.
// Minimized launch keeps the console window out of the way of the desktop.

//go:embed assets/coddy-icon.png
var menuIconBig []byte

//go:embed assets/coddy-icon-48.png
var menuIconSmall []byte

const (
	// menuShortcutName is the file under Programs: what a person looks for
	// in the Start menu, the application and not the binary.
	menuShortcutName = "Coddy Desktop.lnk"
	// menuIconName is the managed icon the link draws from. A Go binary
	// carries no icon resource for the shell to fall back on, so the icon
	// is kept apart from it, under the local application data of the same
	// account.
	menuIconName = "coddy-desktop.ico"
	// menuShortcutArgs is what the entry runs: the program itself, asked
	// for its window.
	menuShortcutArgs = "desktop"
	// menuShortcutDesc is the description the menu shows with the entry.
	menuShortcutDesc = "Coddy agent in a window"
	// linkShowMinNoAct is SW_SHOWMINNOACTIVE: the console starts minimized.
	linkShowMinNoAct = 7
	// shortcutWriteTimeout bounds how long the shell may take to save the
	// link before the install reports a failure instead of stalling the
	// launch it was called from.
	shortcutWriteTimeout = 30 * time.Second
)

// menuProgramsFolder and menuIconFolder resolve where the link and its icon
// live. Variables so a test writes somewhere harmless rather than into the
// Start menu of the person running it.
var menuProgramsFolder = func() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_Programs, windows.KF_FLAG_DEFAULT)
}

var menuIconFolder = func() (string, error) {
	return windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
}

// InstallMenuShortcut installs a per-user Start menu entry for exe, or
// repairs it: the "Coddy Desktop" link in Programs launches `exe desktop`
// with the managed icon and its window mode minimized.
func InstallMenuShortcut(exe string) error {
	if !filepath.IsAbs(exe) {
		return fmt.Errorf("desktop: executable path must be absolute: %q", exe)
	}
	icon, err := writeMenuIcon()
	if err != nil {
		return fmt.Errorf("desktop: write shortcut icon: %w", err)
	}
	programs, err := menuProgramsFolder()
	if err != nil {
		return fmt.Errorf("desktop: find the Programs folder: %w", err)
	}
	if err := os.MkdirAll(programs, 0o755); err != nil {
		return fmt.Errorf("desktop: create shortcut directory: %w", err)
	}
	link := filepath.Join(programs, menuShortcutName)
	return writeMenuShortcut(link, exe, filepath.Dir(exe), icon)
}

// writeMenuIcon drops the managed icon next to the other files of this
// account's application data and returns its path. A launch that finds the
// icon already there writes nothing.
func writeMenuIcon() (string, error) {
	dir, err := menuIconFolder()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "Coddy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, menuIconName)
	data := menuIcon()
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return path, nil
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// menuIcon is the managed .ico: the PNGs the window already draws itself
// with, repacked into the container the shell reads. A .ico entry may hold
// PNG image data since Windows Vista, so the pictures come through
// untouched.
func menuIcon() []byte {
	images := []struct {
		size byte
		png  []byte
	}{
		{48, menuIconSmall},
		{128, menuIconBig},
	}
	var b bytes.Buffer
	put := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }
	put(uint16(0))                       // reserved
	put(uint16(1))                       // type 1: icons
	put(uint16(len(images)))             //nolint:gosec // G115: the icon table is two entries
	offset := uint32(6 + 16*len(images)) //nolint:gosec // G115: the icon table is two entries
	for _, img := range images {
		b.WriteByte(img.size)     // width
		b.WriteByte(img.size)     // height
		b.WriteByte(0)            // no palette
		b.WriteByte(0)            // reserved
		put(uint16(1))            // color planes
		put(uint32(32))           // bits per pixel
		put(uint32(len(img.png))) //nolint:gosec // G115: the embedded PNGs are a few KiB
		put(offset)
		offset += uint32(len(img.png)) //nolint:gosec // G115: the embedded PNGs are a few KiB
	}
	for _, img := range images {
		b.Write(img.png)
	}
	return b.Bytes()
}

// shortcutScript is a constant: every value the link needs arrives through
// the process environment, which Windows keeps in UTF-16, so nothing is
// interpolated into the script and no console code page ever touches the
// paths. A failure comes back on stdout as one Base64 UTF-8 line, the same
// transport the test that reads the link back uses.
const shortcutScript = `$ErrorActionPreference = 'Stop'
try {
	$s = (New-Object -ComObject WScript.Shell).CreateShortcut($env:CODDY_SHORTCUT_LINK)
	$s.TargetPath = $env:CODDY_SHORTCUT_TARGET
	$s.Arguments = $env:CODDY_SHORTCUT_ARGS
	$s.WorkingDirectory = $env:CODDY_SHORTCUT_WORKDIR
	$s.Description = $env:CODDY_SHORTCUT_DESC
	$s.IconLocation = $env:CODDY_SHORTCUT_ICON
	$s.WindowStyle = [int]$env:CODDY_SHORTCUT_SHOW
	$s.Save()
} catch {
	[Console]::Out.WriteLine([Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes($_.Exception.Message)))
	exit 1
}`

// writeMenuShortcut saves the link through the Windows Script Host of the
// system PowerShell. Serializing MS-SHLLINK by hand cannot spell a target
// outside the ANSI code page of the machine - its LocalBasePath turns
// Cyrillic into '?' - so the shell writes the file it will resolve itself.
func writeMenuShortcut(link, exe, workDir, icon string) error {
	system, err := windows.KnownFolderPath(windows.FOLDERID_System, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return fmt.Errorf("desktop: find the System folder: %w", err)
	}
	ps := filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe")
	ctx, cancel := context.WithTimeout(context.Background(), shortcutWriteTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, ps, "-NoProfile", "-NonInteractive", "-Command", shortcutScript)
	cmd.Env = append(os.Environ(),
		"CODDY_SHORTCUT_LINK="+link,
		"CODDY_SHORTCUT_TARGET="+exe,
		"CODDY_SHORTCUT_ARGS="+menuShortcutArgs,
		"CODDY_SHORTCUT_WORKDIR="+workDir,
		"CODDY_SHORTCUT_DESC="+menuShortcutDesc,
		"CODDY_SHORTCUT_ICON="+icon+",0",
		"CODDY_SHORTCUT_SHOW="+strconv.Itoa(linkShowMinNoAct),
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if msg := strings.TrimSpace(string(out)); msg != "" {
			if decoded, derr := base64.StdEncoding.DecodeString(msg); derr == nil {
				msg = string(decoded)
			}
			return fmt.Errorf("desktop: the shell could not write the shortcut: %w: %s", err, msg)
		}
		return fmt.Errorf("desktop: the shell could not write the shortcut: %w", err)
	}
	return nil
}
