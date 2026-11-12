//go:build desktop && desktop.native && darwin && cgo

package desktop

/*
#cgo darwin CFLAGS: -fobjc-arc -fblocks
#cgo darwin LDFLAGS: -framework Cocoa -framework WebKit
#include <stdlib.h>
#include "native_darwin.h"
*/
import "C"

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"runtime/cgo"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// Cocoa's application loop must stay on the process's initial thread. The
// command calls Run synchronously; servers and cancellation run on goroutines.
func init() { runtime.LockOSThread() }

const nativeAvailable = true
const nativeUnavailableHint = "macOS 14 or later (isolated persistent WebKit stores) and a GUI session are required"

func openNativeWindow(ctx context.Context, opts Options, url string) error {
	if err := ctx.Err(); err != nil {
		return nil
	}
	if C.coddy_native_main_thread() == 0 {
		return errors.New("desktop: the native window must run on the main OS thread")
	}
	if C.coddy_native_available() == 0 {
		return fmt.Errorf("%w: %s", errNativeUnavailable, nativeUnavailableHint)
	}
	// The home keeps the identifier of its system-managed WebKit store, so
	// cookies and localStorage are not mixed between unrelated agent homes.
	store := filepath.Join(opts.Home, "desktop", "webkit")
	if err := os.MkdirAll(store, 0o700); err != nil {
		return err
	}
	identifier, err := nativeStoreID(store, opts.Log)
	if err != nil {
		return err
	}
	cu, cs := C.CString(url), C.CString(identifier)
	defer C.free(unsafe.Pointer(cu))
	defer C.free(unsafe.Pointer(cs))
	framed, minimised := C.int(0), C.int(0)
	if opts.Framed {
		framed = 1
	}
	if opts.StartMinimised {
		minimised = 1
	}
	ptr := C.coddy_native_create(cu, cs, framed, minimised, minWindowWidth, minWindowHeight)
	if ptr == nil {
		return errors.New("desktop: creating the native window failed")
	}
	ctl := &nativeWindow{ptr: ptr}
	callback := cgo.NewHandle(ctl)
	ctl.callback = callback
	if opts.Handle != nil {
		opts.Handle.Attach(ctl)
		defer opts.Handle.Attach(nil)
	}
	done, watcherDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			_ = ctl.Close()
		case <-done:
		}
	}()
	C.coddy_native_run(ptr)
	ctl.mu.Lock()
	ctl.ptr = nil
	ctl.mu.Unlock()
	close(done)
	<-watcherDone
	// Closing marks the controller closed on the main thread. Pending blocks
	// retain it but skip callbacks, so none can use this handle after deletion.
	callback.Delete()
	C.coddy_native_release(ptr)
	return nil
}

// All Cocoa messages are queued onto the main thread. Returning from a shell
// request means the operation was accepted; close never blocks an HTTP handler
// waiting on an application loop that has already stopped.
type nativeWindow struct {
	mu       sync.Mutex // guards enqueueing against Run invalidating ptr
	ptr      unsafe.Pointer
	callback cgo.Handle
	// Only the main-thread callback reads or writes gesture geometry.
	moveStart, resizeStart windowRect
}

var _ WindowControl = (*nativeWindow)(nil)
var _ windowActivator = (*nativeWindow)(nil)

func (w *nativeWindow) Supported() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ptr != nil
}

func (w *nativeWindow) action(action C.int, x, y int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.ptr == nil {
		return errors.New("desktop: the native window is closed")
	}
	C.coddy_native_action(w.ptr, C.uintptr_t(w.callback), action, C.int(x), C.int(y))
	return nil
}
func (w *nativeWindow) Undecorate() error        { return w.action(C.CoddyUndecorate, 0, 0) }
func (w *nativeWindow) Decorate() error          { return w.action(C.CoddyDecorate, 0, 0) }
func (w *nativeWindow) Minimise() error          { return w.action(C.CoddyMinimise, 0, 0) }
func (w *nativeWindow) ToggleMaximise() error    { return w.action(C.CoddyMaximise, 0, 0) }
func (w *nativeWindow) Close() error             { return w.action(C.CoddyClose, 0, 0) }
func (w *nativeWindow) activate() error          { return w.action(C.CoddyActivate, 0, 0) }
func (w *nativeWindow) BeginMove(x, y int) error { return w.action(C.CoddyMoveBegin, x, y) }
func (w *nativeWindow) MoveBy(x, y int) error    { return w.action(C.CoddyMoveBy, x, y) }
func (w *nativeWindow) BeginResize() error       { return w.action(C.CoddyResizeBegin, 0, 0) }
func (w *nativeWindow) ResizeBy(dir string, x, y int) error {
	directions := map[string]C.int{"n": C.CoddyResizeN, "s": C.CoddyResizeS, "w": C.CoddyResizeW, "e": C.CoddyResizeE, "nw": C.CoddyResizeNW, "ne": C.CoddyResizeNE, "sw": C.CoddyResizeSW, "se": C.CoddyResizeSE}
	action, ok := directions[dir]
	if !ok {
		return errors.New("desktop: unknown resize direction")
	}
	return w.action(action, x, y)
}
func (w *nativeWindow) WaitForWindow(time.Duration) bool { return w.Supported() }

func nativeFrame(ptr unsafe.Pointer) windowRect {
	frame := C.coddy_native_frame(ptr)
	return windowRect{int(frame.x), int(frame.y), int(frame.width), int(frame.height)}
}
func setNativeFrame(ptr unsafe.Pointer, frame windowRect) {
	C.coddy_native_set_frame(ptr, C.CoddyFrame{x: C.int(frame.X), y: C.int(frame.Y), width: C.int(frame.W), height: C.int(frame.H)})
}

//export coddy_native_geometry
func coddy_native_geometry(callback C.uintptr_t, ptr unsafe.Pointer, action, x, y C.int) {
	// Called only by an open controller's queued block on Cocoa's main thread.
	w := cgo.Handle(callback).Value().(*nativeWindow)
	switch action {
	case C.CoddyMoveBegin:
		cur := nativeFrame(ptr)
		pinned := C.coddy_native_zoomed(ptr) != 0
		if pinned {
			C.coddy_native_unzoom(ptr)
		}
		restored := nativeFrame(ptr)
		plan := planDragStart(pinned, cur, restored, int(x))
		restored.X, restored.Y = plan.X, plan.Y
		if plan.Restore {
			setNativeFrame(ptr, restored)
		}
		w.moveStart = restored
	case C.CoddyMoveBy:
		frame := nativeFrame(ptr)
		frame.X, frame.Y = clampMove(w.moveStart.X+int(x), w.moveStart.Y+int(y))
		setNativeFrame(ptr, frame)
	case C.CoddyResizeBegin:
		w.resizeStart = nativeFrame(ptr)
	default:
		directions := [...]string{"n", "s", "w", "e", "nw", "ne", "sw", "se"}
		direction := int(action - C.CoddyResizeN)
		if direction >= 0 && direction < len(directions) {
			if frame, ok := resizeRect(w.resizeStart, directions[direction], int(x), int(y)); ok {
				setNativeFrame(ptr, frame)
			}
		}
	}
}

func nativeStoreID(dir string, log *slog.Logger) (string, error) {
	path := filepath.Join(dir, "id")
	b, err := os.ReadFile(path)
	if err == nil {
		value := strings.TrimSpace(string(b))
		if len(value) == 36 && value[8] == '-' && value[13] == '-' && value[18] == '-' && value[23] == '-' {
			if decoded, decodeErr := hex.DecodeString(strings.ReplaceAll(value, "-", "")); decodeErr == nil && len(decoded) == 16 {
				return value, nil
			}
		}
		err = errors.New("invalid WebKit store identifier")
	}
	if !errors.Is(err, os.ErrNotExist) && log != nil {
		log.Warn("the WebKit store identifier could not be used; creating a new store", "path", path, "error", err)
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", err
	}
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	value := fmt.Sprintf("%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	if _, err := platform.WritePrivateFileReplacing(path, []byte(value+"\n")); err != nil {
		return "", err
	}
	return value, nil
}
