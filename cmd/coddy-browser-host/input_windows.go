//go:build windows

package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"
)

var (
	user32      = syscall.NewLazyDLL("user32.dll")
	foreground  = user32.NewProc("GetForegroundWindow")
	windowText  = user32.NewProc("GetWindowTextW")
	windowPID   = user32.NewProc("GetWindowThreadProcessId")
	setCursor   = user32.NewProc("SetCursorPos")
	mouseEvent  = user32.NewProc("mouse_event")
	sendInput   = user32.NewProc("SendInput")
	keyEvent    = user32.NewProc("keybd_event")
	openProcess = syscall.NewLazyDLL("kernel32.dll").NewProc("OpenProcess")
	processName = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryFullProcessImageNameW")
)

func checkTarget(title string) error {
	if title == "" {
		return fmt.Errorf("browser tab has no title; cannot verify input destination")
	}
	hwnd, _, _ := foreground.Call()
	var pid uint32
	_, _, _ = windowPID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	process, _, _ := openProcess.Call(0x1000, 0, uintptr(pid))
	if process == 0 {
		return fmt.Errorf("cannot verify foreground application")
	}
	defer func() { _ = syscall.CloseHandle(syscall.Handle(process)) }()
	name := make([]uint16, 32768)
	size := uint32(len(name))
	ok, _, _ := processName.Call(process, 0, uintptr(unsafe.Pointer(&name[0])), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return fmt.Errorf("cannot read foreground application name")
	}
	application := filepath.Base(syscall.UTF16ToString(name[:size]))
	if !strings.EqualFold(application, "firefox.exe") {
		return fmt.Errorf("firefox must be the foreground application (current: %s)", application)
	}
	buffer := make([]uint16, 4096)
	_, _, _ = windowText.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if !strings.Contains(syscall.UTF16ToString(buffer), title) {
		return fmt.Errorf("active Firefox tab changed; inspect it again")
	}
	return nil
}

func move(x, y float64) error {
	if math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) || x < -32768 || y < -32768 || x > 32767 || y > 32767 {
		return fmt.Errorf("invalid screen coordinates")
	}
	ok, _, err := setCursor.Call(uintptr(int32(x)), uintptr(int32(y)))
	if ok == 0 {
		return err
	}
	return nil
}

func key(code uintptr, up bool) {
	flags := uintptr(0)
	if up {
		flags = 2
	}
	_, _, _ = keyEvent.Call(code, 0, flags, 0)
}

func execute(ctx context.Context, r request) error {
	if err := validateInput(r); err != nil {
		return err
	}
	_, _, _ = user32.NewProc("SetProcessDPIAware").Call()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := checkTarget(r.Title); err != nil {
		return err
	}
	if r.Scale <= 0 || r.Scale > 5 {
		return fmt.Errorf("invalid display scale")
	}
	if r.ScreenScale <= 0 || r.ScreenScale > 5 {
		return fmt.Errorf("invalid screen scale")
	}
	x, y := r.OriginX*r.ScreenScale+r.X*r.Scale, r.OriginY*r.ScreenScale+r.Y*r.Scale
	switch r.Action {
	case "click":
		if err := move(x, y); err != nil {
			return err
		}
		_, _, _ = mouseEvent.Call(2, 0, 0, 0, 0)
		_, _, _ = mouseEvent.Call(4, 0, 0, 0, 0)
	case "type":
		if len(r.Text) > 64<<10 {
			return fmt.Errorf("input exceeds 64 KiB")
		}
		if r.Clear {
			key(0x11, false)
			key(0x41, false)
			key(0x41, true)
			key(0x11, true)
		}
		units := utf16.Encode([]rune(r.Text))
		stride, offset := 28, 4
		if unsafe.Sizeof(uintptr(0)) == 8 {
			stride, offset = 40, 8
		}
		for _, unit := range units {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := checkTarget(r.Title); err != nil {
				return err
			}
			data := make([]byte, 2*stride)
			for i := 0; i < 2; i++ {
				base := i * stride
				binary.LittleEndian.PutUint32(data[base:], 1)
				binary.LittleEndian.PutUint16(data[base+offset+2:], unit)
				flags := uint32(4)
				if i == 1 {
					flags |= 2
				}
				binary.LittleEndian.PutUint32(data[base+offset+4:], flags)
			}
			n, _, err := sendInput.Call(2, uintptr(unsafe.Pointer(&data[0])), uintptr(stride))
			if n != 2 {
				return fmt.Errorf("native text input failed: %w", err)
			}
		}
	case "press":
		keys := map[string]uintptr{"Enter": 13, "Tab": 9, "Backspace": 8, "Delete": 46, "Escape": 27, "ArrowLeft": 37, "ArrowUp": 38, "ArrowRight": 39, "ArrowDown": 40, "Home": 36, "End": 35, "Space": 32}
		parts := strings.Split(r.Key, "+")
		name := parts[len(parts)-1]
		var modifiers []uintptr
		for _, m := range parts[:len(parts)-1] {
			code, ok := map[string]uintptr{"Ctrl": 0x11, "Control": 0x11, "Alt": 0x12, "Shift": 0x10, "Meta": 0x5b, "Cmd": 0x5b}[m]
			if !ok {
				return fmt.Errorf("unsupported modifier")
			}
			modifiers = append(modifiers, code)
		}
		for _, code := range modifiers {
			key(code, false)
		}
		defer func() {
			for i := len(modifiers) - 1; i >= 0; i-- {
				key(modifiers[i], true)
			}
		}()
		code, ok := keys[name]
		if !ok && len(name) == 1 && ((name[0] >= 'a' && name[0] <= 'z') || (name[0] >= 'A' && name[0] <= 'Z') || (name[0] >= '0' && name[0] <= '9')) {
			code, ok = uintptr(strings.ToUpper(name)[0]), true
		}
		if !ok {
			return fmt.Errorf("unsupported key")
		}
		key(code, false)
		key(code, true)
	case "scroll":
		if err := move(x, y); err != nil {
			return err
		}
		if r.DY != 0 {
			_, _, _ = mouseEvent.Call(0x800, 0, 0, uintptr(int32(-r.DY)), 0)
		}
		if r.DX != 0 {
			_, _, _ = mouseEvent.Call(0x1000, 0, 0, uintptr(int32(r.DX)), 0)
		}
	case "drag":
		if err := move(x, y); err != nil {
			return err
		}
		_, _, _ = mouseEvent.Call(2, 0, 0, 0, 0)
		defer func() { _, _, _ = mouseEvent.Call(4, 0, 0, 0, 0) }()
		for i := 1; i <= 16; i++ {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := checkTarget(r.Title); err != nil {
				return err
			}
			if err := move(r.OriginX*r.ScreenScale+(r.X+(r.ToX-r.X)*float64(i)/16)*r.Scale, r.OriginY*r.ScreenScale+(r.Y+(r.ToY-r.Y)*float64(i)/16)*r.Scale); err != nil {
				return err
			}
			time.Sleep(5 * time.Millisecond)
		}
	default:
		return fmt.Errorf("unsupported native input action")
	}
	return nil
}
