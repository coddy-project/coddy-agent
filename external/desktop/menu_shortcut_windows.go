//go:build windows

package desktop

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// The Start menu entry of this user: a shell link named after the
// application in the Programs folder of the account that runs Coddy. Where
// the XDG entry is a text file any launcher reads, this is the binary shell
// link format (MS-SHLLINK), written directly the way the tray speaks its
// own protocol elsewhere in this package - nothing in the toolchain calls
// the shell's COM API, and a cross-compile produces exactly the file a
// Windows installer would.
//
// Two things the format buys here. The link names its target twice - once
// as the shell item list Explorer builds first, once as the LinkInfo the
// resolver falls back on - so it opens the binary it was written for even
// after the menu stops trusting the list. And its run mode is Minimized:
// coddy is one program for the terminal and the desktop both, so the
// console subsystem stays (a CLI launched from a terminal still works), and
// what keeps a Start menu launch from opening a console over the window is
// this field, not a second binary.

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
	path := filepath.Join(programs, menuShortcutName)
	if err := os.WriteFile(path, menuShortcut(exe, filepath.Dir(exe), icon), 0o600); err != nil {
		return fmt.Errorf("desktop: write shortcut: %w", err)
	}
	return nil
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
		put(uint16(32))           // bits per pixel
		put(uint32(len(img.png))) //nolint:gosec // G115: the embedded PNGs are a few KiB
		put(offset)
		offset += uint32(len(img.png)) //nolint:gosec // G115: the embedded PNGs are a few KiB
	}
	for _, img := range images {
		b.Write(img.png)
	}
	return b.Bytes()
}

// Shell link flags and constants (MS-SHLLINK).
const (
	linkHasIDList    = 0x01 // a LinkTargetIDList follows the header
	linkHasLinkInfo  = 0x02 // a LinkInfo structure follows
	linkHasName      = 0x04 // NAME_STRING string data
	linkHasWorkDir   = 0x10 // WORKING_DIR string data
	linkHasArgs      = 0x20 // COMMAND_LINE_ARGUMENTS string data
	linkHasIcon      = 0x40 // ICON_LOCATION string data
	linkIsUnicode    = 0x80 // string data is UTF-16LE
	linkShowMinNoAct = 7    // SW_SHOWMINNOACTIVE: the console starts minimized
)

// menuShortcut is the shell link itself: the 76-byte header, the link-target
// ID list the path builds, the LinkInfo that still names the target where
// the list cannot, the counted strings its flags announce, and the terminal
// block that ends the file.
func menuShortcut(exe, workDir, icon string) []byte {
	var b bytes.Buffer
	put := func(v any) { _ = binary.Write(&b, binary.LittleEndian, v) }

	idList := linkTargetIDList(exe)
	flags := uint32(linkHasLinkInfo | linkHasName | linkHasWorkDir | linkHasArgs | linkHasIcon | linkIsUnicode)
	if idList != nil {
		flags |= linkHasIDList
	}

	// SHELL_LINK_HEADER.
	put(uint32(0x4c)) // header size
	b.Write([]byte{0x01, 0x14, 0x02, 0x00, 0x00, 0x00, 0x00, 0x00,
		0xc0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x46}) // CLSID_ShellLink
	put(flags)
	put(uint32(0x20))         // FileAttributes: FILE_ATTRIBUTE_ARCHIVE
	b.Write(make([]byte, 24)) // creation, access and write times
	put(uint32(0))            // FileSize
	put(int32(0))             // IconIndex
	put(uint32(linkShowMinNoAct))
	put(uint16(0)) // HotKey
	put(uint16(0)) // Reserved1
	put(uint32(0)) // Reserved2
	put(uint32(0)) // Reserved3

	b.Write(idList)
	b.Write(linkInfo(exe))
	b.Write(shortcutString(menuShortcutDesc)) // NAME_STRING
	b.Write(shortcutString(workDir))          // WORKING_DIR
	b.Write(shortcutString(menuShortcutArgs)) // COMMAND_LINE_ARGUMENTS
	b.Write(shortcutString(icon + ",0"))      // ICON_LOCATION
	put(uint32(0))                            // TERMINAL_BLOCK
	return b.Bytes()
}

// linkTargetIDList is the shell item list of the target: My Computer, the
// drive, then a file entry per component. Nil for a path it cannot split -
// a relative name or a UNC share - and the link resolves through its
// LinkInfo instead, which is what the resolver reads next.
func linkTargetIDList(exe string) []byte {
	vol := filepath.VolumeName(exe)
	if len(vol) != 2 || vol[1] != ':' {
		return nil
	}
	var items bytes.Buffer
	// The computer: a 0x1F50 item naming CLSID_MyComputer.
	writeItemID(&items, []byte{0x1f, 0x50, 0xe0, 0x4f, 0xd0, 0x20,
		0xea, 0x3a, 0x69, 0x10, 0xa2, 0xd8, 0x08, 0x00, 0x2b, 0x30, 0x30, 0x9d})
	// The drive: a 0x2F item holding "C:\" in a fixed-size field.
	var drive [23]byte
	drive[0] = 0x2f
	copy(drive[1:], strings.ToUpper(vol)+`\`)
	writeItemID(&items, drive[:])
	parts := strings.Split(strings.TrimPrefix(exe[len(vol):], `\`), `\`)
	for i, part := range parts {
		writeItemID(&items, fileEntryItem(part, i < len(parts)-1))
	}
	var out bytes.Buffer
	_ = binary.Write(&out, binary.LittleEndian, uint16(items.Len()+2)) //nolint:gosec // G115: an ID list of path parts is far under 64 KiB
	out.Write(items.Bytes())
	out.Write([]byte{0, 0}) // TerminalID
	return out.Bytes()
}

// writeItemID appends one shell item: its size, including the size field
// itself, then its data.
func writeItemID(b *bytes.Buffer, data []byte) {
	_ = binary.Write(b, binary.LittleEndian, uint16(len(data)+2)) //nolint:gosec // G115: a shell item is a path part, far under 64 KiB
	b.Write(data)
}

// fileEntryItem is the FAT-variant file entry shell item for one component
// of the target path: 0x31 for a directory, 0x32 for the file, the name in
// ANSI and the item padded to an even size.
func fileEntryItem(name string, dir bool) []byte {
	var d bytes.Buffer
	put := func(v any) { _ = binary.Write(&d, binary.LittleEndian, v) }
	if dir {
		d.WriteByte(0x31)
	} else {
		d.WriteByte(0x32)
	}
	put(uint32(0)) // size of the target
	put(uint32(0)) // its FAT timestamp
	if dir {
		put(uint16(0x10)) // FILE_ATTRIBUTE_DIRECTORY
	} else {
		put(uint16(0x20)) // FILE_ATTRIBUTE_ARCHIVE
	}
	d.Write(ansiBytes(name))
	d.WriteByte(0)
	if d.Len()%2 == 1 {
		d.WriteByte(0)
	}
	return d.Bytes()
}

// linkInfo is the LinkInfo structure: a fixed drive volume and the target's
// path, in ANSI for old readers and in Unicode for the ones that count. It
// is what resolves the target when there is no working ID list to walk.
func linkInfo(exe string) []byte {
	base := ansiBytes(exe)
	suffix := ansiBytes(filepath.Base(exe))
	baseU := utf16.Encode([]rune(exe))
	suffixU := utf16.Encode([]rune(filepath.Base(exe)))

	volume := make([]byte, 20)
	binary.LittleEndian.PutUint32(volume[0:], 20)    // VolumeIDSize
	binary.LittleEndian.PutUint32(volume[4:], 3)     // DRIVE_FIXED
	binary.LittleEndian.PutUint32(volume[12:], 0x10) // VolumeLabelOffset: the empty label below

	header := 9 * 4 // every offset field through the Unicode suffix
	volumeOff := header
	baseOff := volumeOff + len(volume)
	suffixOff := baseOff + len(base) + 1
	baseUOff := suffixOff + len(suffix) + 1
	suffixUOff := baseUOff + 2*(len(baseU)+1)
	size := suffixUOff + 2*(len(suffixU)+1)

	var b bytes.Buffer
	put := func(v uint32) { _ = binary.Write(&b, binary.LittleEndian, v) }
	put(uint32(size)) //nolint:gosec // G115: a LinkInfo is the size of a path
	put(uint32(header))
	put(1) // VolumeIDAndLocalBasePath
	put(uint32(volumeOff))
	put(uint32(baseOff))    //nolint:gosec // G115: a LinkInfo is the size of a path
	put(0)                  // no common network relative link
	put(uint32(baseUOff))   //nolint:gosec // G115: a LinkInfo is the size of a path
	put(uint32(suffixOff))  //nolint:gosec // G115: a LinkInfo is the size of a path
	put(uint32(suffixUOff)) //nolint:gosec // G115: a LinkInfo is the size of a path
	b.Write(volume)
	b.Write(base)
	b.WriteByte(0)
	b.Write(suffix)
	b.WriteByte(0)
	put16 := func(c uint16) { _ = binary.Write(&b, binary.LittleEndian, c) }
	for _, c := range baseU {
		put16(c)
	}
	put16(0)
	for _, c := range suffixU {
		put16(c)
	}
	put16(0)
	return b.Bytes()
}

// shortcutString is one counted string of the string data section: the
// number of UTF-16 code units, then the units without a terminator.
func shortcutString(s string) []byte {
	u := utf16.Encode([]rune(s))
	out := make([]byte, 2+2*len(u))
	binary.LittleEndian.PutUint16(out, uint16(len(u))) //nolint:gosec // G115: a counted string is a path or a name, far under 64 KiB
	for i, c := range u {
		binary.LittleEndian.PutUint16(out[2+2*i:], c)
	}
	return out
}

// ansiBytes is the ANSI form of a name the format asks for twice. The system
// code page is unknowable here; a byte a page cannot spell becomes '?', and
// the Unicode twin of the same field - always written - carries the real
// name.
func ansiBytes(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r > 0xff {
			out = append(out, '?')
		} else {
			out = append(out, byte(r)) //nolint:gosec // G115: the rune is at most 0xff here
		}
	}
	return out
}
