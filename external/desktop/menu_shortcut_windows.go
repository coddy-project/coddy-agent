//go:build desktop && windows

package desktop

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unsafe"

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

// These vtables follow the Windows SDK declarations, including inherited
// IUnknown and IPersist slots. Unused slots are kept in their ABI positions.
type shellLinkW struct {
	vtbl *shellLinkWVtbl
}

type shellLinkWVtbl struct {
	queryInterface, _, release               uintptr // IUnknown
	getPath                                  uintptr
	_, _                                     uintptr // GetIDList, SetIDList
	getDescription, setDescription           uintptr
	getWorkingDirectory, setWorkingDirectory uintptr
	getArguments, setArguments               uintptr
	_, _                                     uintptr // GetHotkey, SetHotkey
	getShowCmd, setShowCmd                   uintptr
	getIconLocation, setIconLocation         uintptr
	_, _                                     uintptr // SetRelativePath, Resolve
	setPath                                  uintptr
}

type persistFile struct {
	vtbl *persistFileVtbl
}

type persistFileVtbl struct {
	_, _, release uintptr // IUnknown
	_             uintptr // IPersist.GetClassID
	_             uintptr // IsDirty
	load, save    uintptr
}

var (
	ole32            = windows.NewLazySystemDLL("ole32.dll")
	coInitializeEx   = ole32.NewProc("CoInitializeEx")
	coUninitialize   = ole32.NewProc("CoUninitialize")
	coCreateInstance = ole32.NewProc("CoCreateInstance")

	clsidShellLink = windows.GUID{Data1: 0x00021401, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidShellLinkW  = windows.GUID{Data1: 0x000214f9, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
	iidPersistFile = windows.GUID{Data1: 0x0000010b, Data4: [8]byte{0xc0, 0, 0, 0, 0, 0, 0, 0x46}}
)

func hresultCode(hr uintptr) uint32 {
	return uint32(hr) //nolint:gosec // G115: only the low 32 bits of an HRESULT are defined.
}

func shellLinkResult(method string, hr uintptr) error {
	code := hresultCode(hr)
	if code&0x80000000 != 0 {
		return fmt.Errorf("desktop: %s failed (HRESULT 0x%08X): %w", method, code, syscall.Errno(code))
	}
	return nil
}

//nolint:gosec // G103: The COM ABI requires native interface and GUID pointers.
func withShellLink(use func(*shellLinkW, *persistFile) error) error {
	const (
		coinitApartmentThreaded = 0x2
		coinitDisableOLE1DDE    = 0x4
		clsctxInprocServer      = 0x1
		rpcEChangedMode         = 0x80010106
	)
	// COM initialization, object use, and release all belong to this thread.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := coInitializeEx.Call(0, coinitApartmentThreaded|coinitDisableOLE1DDE)
	if hresultCode(hr) != rpcEChangedMode {
		if err := shellLinkResult("CoInitializeEx", hr); err != nil {
			return err
		}
		// Both S_OK and S_FALSE must be balanced. If the thread already
		// has a different apartment, use it without balancing a failed call.
		defer func() { _, _, _ = coUninitialize.Call() }() // CoUninitialize returns void.
	}

	var link *shellLinkW
	hr, _, _ = coCreateInstance.Call(uintptr(unsafe.Pointer(&clsidShellLink)), 0, clsctxInprocServer,
		uintptr(unsafe.Pointer(&iidShellLinkW)), uintptr(unsafe.Pointer(&link)))
	if err := shellLinkResult("CoCreateInstance(ShellLink)", hr); err != nil {
		return err
	}
	defer func() { _, _, _ = syscall.SyscallN(link.vtbl.release, uintptr(unsafe.Pointer(link))) }()

	var file *persistFile
	hr, _, _ = syscall.SyscallN(link.vtbl.queryInterface, uintptr(unsafe.Pointer(link)),
		uintptr(unsafe.Pointer(&iidPersistFile)), uintptr(unsafe.Pointer(&file)))
	if err := shellLinkResult("IShellLinkW.QueryInterface(IPersistFile)", hr); err != nil {
		return err
	}
	defer func() { _, _, _ = syscall.SyscallN(file.vtbl.release, uintptr(unsafe.Pointer(file))) }()
	return use(link, file)
}

// Let the shell choose the on-disk link layout, passing values directly to
// the Unicode interface rather than through the Windows Script Host.
//
//nolint:gosec // G103: Native setters and IPersistFile.Save require UTF-16 pointers.
func writeMenuShortcut(path, exe, workDir, icon string) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fmt.Errorf("desktop: encode shortcut filename: %w", err)
	}
	return withShellLink(func(link *shellLinkW, file *persistFile) error {
		vt := link.vtbl
		for _, field := range []struct {
			method string
			proc   uintptr
			value  string
		}{
			{"SetPath", vt.setPath, exe},
			{"SetArguments", vt.setArguments, menuShortcutArgs},
			{"SetWorkingDirectory", vt.setWorkingDirectory, workDir},
			{"SetDescription", vt.setDescription, menuShortcutDesc},
		} {
			value, err := windows.UTF16PtrFromString(field.value)
			if err != nil {
				return fmt.Errorf("desktop: encode shortcut value for %s: %w", field.method, err)
			}
			hr, _, _ := syscall.SyscallN(field.proc, uintptr(unsafe.Pointer(link)), uintptr(unsafe.Pointer(value)))
			if err := shellLinkResult("IShellLinkW."+field.method, hr); err != nil {
				return err
			}
		}
		iconPath, err := windows.UTF16PtrFromString(icon)
		if err != nil {
			return fmt.Errorf("desktop: encode shortcut icon path: %w", err)
		}
		hr, _, _ := syscall.SyscallN(vt.setIconLocation, uintptr(unsafe.Pointer(link)), uintptr(unsafe.Pointer(iconPath)), 0)
		if err := shellLinkResult("IShellLinkW.SetIconLocation", hr); err != nil {
			return err
		}
		hr, _, _ = syscall.SyscallN(vt.setShowCmd, uintptr(unsafe.Pointer(link)), linkShowMinNoAct)
		if err := shellLinkResult("IShellLinkW.SetShowCmd", hr); err != nil {
			return err
		}
		// Save a copy (fRemember = FALSE): the object is not reused.
		hr, _, _ = syscall.SyscallN(file.vtbl.save, uintptr(unsafe.Pointer(file)), uintptr(unsafe.Pointer(name)), 0)
		if hresultCode(hr) == 1 { // IPersistFile.Save can report an unsuccessful save as S_FALSE.
			return fmt.Errorf("desktop: IPersistFile.Save did not save %q (S_FALSE)", path)
		}
		return shellLinkResult("IPersistFile.Save", hr)
	})
}
