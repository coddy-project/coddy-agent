//go:build desktop && darwin

package desktop

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
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego/objc"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// Cocoa's application loop must stay on the process's initial thread. The
// command calls Run synchronously; servers and cancellation run on goroutines.
func init() { runtime.LockOSThread() }

const nativeAvailable = true
const nativeUnavailableHint = "macOS 14 or later (isolated persistent WebKit stores) and a GUI session are required"

// What the shell asks the window for. The geometry actions are the ones the
// drag arithmetic of drag.go answers, shared with the X11 backend.
const (
	nativeUndecorate = iota
	nativeDecorate
	nativeMinimise
	nativeMaximise
	nativeClose
	nativeActivate
	nativeMoveBegin
	nativeMoveBy
	nativeResizeBegin
	nativeResizeN
	nativeResizeS
	nativeResizeW
	nativeResizeE
	nativeResizeNW
	nativeResizeNE
	nativeResizeSW
	nativeResizeSE
)

// The one window of this process. NSApplication is a singleton with one main
// loop, so there is one window to find, and every delegate reaches it here.
var (
	currentMu sync.Mutex
	current   *nativeWindow
)

func activeWindow() *nativeWindow {
	currentMu.Lock()
	defer currentMu.Unlock()
	return current
}

func setActiveWindow(w *nativeWindow) {
	currentMu.Lock()
	defer currentMu.Unlock()
	current = w
}

func openNativeWindow(ctx context.Context, opts Options, url string) error {
	if err := ctx.Err(); err != nil {
		return nil
	}
	// A framework or symbol this macOS does not carry is an availability
	// failure like any other: `auto` moves on to the browser.
	if err := loadCocoa(); err != nil {
		return fmt.Errorf("%w: %s", errNativeUnavailable, err)
	}
	if !onCocoaMainThread() {
		return errors.New("desktop: the native window must run on the main OS thread")
	}
	// Both before NSApplication or any window: a headless launch must be able
	// to fall back to the browser backend without Cocoa terminating it, and
	// the release binary itself starts on macOS 13, where the named stores
	// that keep agent homes apart do not exist.
	if !isolatedStoresAvailable() || !guiSessionAvailable() {
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
	w, err := createNativeWindow(opts, url, identifier)
	if err != nil {
		return err
	}
	setActiveWindow(w)
	defer setActiveWindow(nil)
	if opts.Handle != nil {
		opts.Handle.Attach(w)
		defer opts.Handle.Attach(nil)
	}
	done, watcherDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(watcherDone)
		select {
		case <-ctx.Done():
			_ = w.Close()
		case <-done:
		}
	}()
	w.run()
	w.mu.Lock()
	w.object = 0
	w.mu.Unlock()
	close(done)
	<-watcherDone
	// Closing marks the window closed on the main thread. Work the delegates
	// queued still finds the window, and skips on the closed flag.
	w.release()
	return nil
}

// createNativeWindow builds the window, its WKWebView and the delegate that
// answers for both.
//
// A panic here is the Objective-C runtime answering a message this macOS
// shapes differently, which no caller can act on and a browser can serve
// instead, so it is reported as an availability failure rather than ending a
// run that has a second backend.
func createNativeWindow(opts Options, url, identifier string) (w *nativeWindow, err error) {
	defer func() {
		if r := recover(); r != nil {
			w, err = nil, fmt.Errorf("%w: creating it raised %v", errNativeUnavailable, r)
		}
	}()
	application := nsApp()
	application.Send(sel("setActivationPolicy:"), nsApplicationActivationPolicyRegular)
	controller := objc.ID(controllerClass).Send(sel("alloc")).Send(sel("init"))
	if controller == 0 {
		return nil, errors.New("desktop: the window delegate could not be created")
	}
	window := class("NSWindow").Send(sel("alloc")).Send(
		sel("initWithContentRect:styleMask:backing:defer:"),
		nsRect{Size: nsSize{Width: 1280, Height: 800}},
		nsWindowStyleMaskTitled|nsWindowStyleMaskClosable|nsWindowStyleMaskMiniaturizable|nsWindowStyleMaskResizable,
		nsBackingStoreBuffered, false)
	if window == 0 {
		return nil, errors.New("desktop: creating the native window failed")
	}
	window.Send(sel("setReleasedWhenClosed:"), false)
	window.Send(sel("setTitle:"), nsString("Coddy"))
	window.Send(sel("setMinSize:"), nsSize{Width: float64(minWindowWidth), Height: float64(minWindowHeight)})
	window.Send(sel("setDelegate:"), controller)
	decorate(window, opts.Framed)

	configuration := class("WKWebViewConfiguration").Send(sel("alloc")).Send(sel("init"))
	uuid := class("NSUUID").Send(sel("alloc")).Send(sel("initWithUUIDString:"), nsString(identifier))
	if uuid == 0 {
		window.Send(sel("close"))
		return nil, errors.New("desktop: the WebKit store identifier was refused")
	}
	configuration.Send(sel("setWebsiteDataStore:"), class("WKWebsiteDataStore").Send(sel("dataStoreForIdentifier:"), uuid))

	contentView := window.Send(sel("contentView"))
	webview := class("WKWebView").Send(sel("alloc")).Send(sel("initWithFrame:configuration:"),
		objc.Send[nsRect](contentView, sel("bounds")), configuration)
	if webview == 0 {
		window.Send(sel("close"))
		return nil, errors.New("desktop: creating the WKWebView failed")
	}
	webview.Send(sel("setAutoresizingMask:"), nsViewWidthSizable|nsViewHeightSizable)
	webview.Send(sel("setUIDelegate:"), controller)
	webview.Send(sel("setNavigationDelegate:"), controller)
	window.Send(sel("setContentView:"), webview)
	application.Send(sel("setDelegate:"), controller)
	installMenu(controller)
	window.Send(sel("center"))
	window.Send(sel("makeKeyAndOrderFront:"), objc.ID(0))
	application.Send(sel("activateIgnoringOtherApps:"), true)
	request := class("NSURLRequest").Send(sel("requestWithURL:"), class("NSURL").Send(sel("URLWithString:"), nsString(url)))
	webview.Send(sel("loadRequest:"), request)
	if opts.StartMinimised {
		window.Send(sel("miniaturize:"), objc.ID(0))
	}
	return &nativeWindow{
		object:    controller,
		window:    window,
		webview:   webview,
		downloads: map[objc.ID]string{},
		log:       opts.Log,
	}, nil
}

// All Cocoa messages are queued onto the main thread. Returning from a shell
// request means the operation was accepted; close never blocks an HTTP handler
// waiting on an application loop that has already stopped.
type nativeWindow struct {
	mu      sync.Mutex // guards the fields below against Run invalidating them
	object  objc.ID    // the delegate; zero once the run that owned it is over
	window  objc.ID
	webview objc.ID
	closed  bool
	// What each unfinished download was promised, so two of them in flight
	// never agree on the same name.
	downloads map[objc.ID]string
	log       *slog.Logger
	// Only the main-thread geometry handler reads or writes gesture geometry.
	moveStart, resizeStart windowRect
}

var _ WindowControl = (*nativeWindow)(nil)
var _ windowActivator = (*nativeWindow)(nil)

func (w *nativeWindow) Supported() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.object != 0
}

func (w *nativeWindow) isClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

func (w *nativeWindow) setClosed() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
}

// run gives Cocoa the thread until the window closes.
func (w *nativeWindow) run() {
	if !w.isClosed() {
		onMainThread("the application loop", func() { nsApp().Send(sel("run")) })
	}
	w.setClosed()
}

// release takes the window apart on the thread that built it.
func (w *nativeWindow) release() {
	onMainThread("releasing the window", func() {
		class("NSAppleEventManager").Send(sel("sharedAppleEventManager")).Send(
			sel("removeEventHandlerForEventClass:andEventID:"), kCoreEventClass, kAEQuitApplication)
		if w.webview != 0 {
			w.webview.Send(sel("stopLoading"))
			w.webview.Send(sel("setUIDelegate:"), objc.ID(0))
			w.webview.Send(sel("setNavigationDelegate:"), objc.ID(0))
		}
		if w.window != 0 {
			w.window.Send(sel("setContentView:"), objc.ID(0))
			w.window.Send(sel("setDelegate:"), objc.ID(0))
		}
		application := nsApp()
		if application.Send(sel("delegate")) == w.object {
			application.Send(sel("setDelegate:"), objc.ID(0))
		}
		application.Send(sel("setMainMenu:"), objc.ID(0))
		w.webview, w.window = 0, 0
	})
}

func (w *nativeWindow) action(action, x, y int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.object == 0 {
		return errors.New("desktop: the native window is closed")
	}
	enqueueOnMain(func() { w.perform(action, x, y) })
	return nil
}

func (w *nativeWindow) Undecorate() error        { return w.action(nativeUndecorate, 0, 0) }
func (w *nativeWindow) Decorate() error          { return w.action(nativeDecorate, 0, 0) }
func (w *nativeWindow) Minimise() error          { return w.action(nativeMinimise, 0, 0) }
func (w *nativeWindow) ToggleMaximise() error    { return w.action(nativeMaximise, 0, 0) }
func (w *nativeWindow) Close() error             { return w.action(nativeClose, 0, 0) }
func (w *nativeWindow) activate() error          { return w.action(nativeActivate, 0, 0) }
func (w *nativeWindow) BeginMove(x, y int) error { return w.action(nativeMoveBegin, x, y) }
func (w *nativeWindow) MoveBy(x, y int) error    { return w.action(nativeMoveBy, x, y) }
func (w *nativeWindow) BeginResize() error       { return w.action(nativeResizeBegin, 0, 0) }
func (w *nativeWindow) ResizeBy(dir string, x, y int) error {
	directions := map[string]int{
		"n": nativeResizeN, "s": nativeResizeS, "w": nativeResizeW, "e": nativeResizeE,
		"nw": nativeResizeNW, "ne": nativeResizeNE, "sw": nativeResizeSW, "se": nativeResizeSE,
	}
	action, ok := directions[dir]
	if !ok {
		return errors.New("desktop: unknown resize direction")
	}
	return w.action(action, x, y)
}
func (w *nativeWindow) WaitForWindow(time.Duration) bool { return w.Supported() }

// perform runs one shell request on Cocoa's main thread.
func (w *nativeWindow) perform(action, x, y int) {
	if w.isClosed() || w.window == 0 {
		return
	}
	switch action {
	case nativeUndecorate:
		decorate(w.window, false)
	case nativeDecorate:
		decorate(w.window, true)
	case nativeMinimise:
		w.window.Send(sel("miniaturize:"), objc.ID(0))
	case nativeMaximise:
		w.window.Send(sel("zoom:"), objc.ID(0))
	case nativeClose:
		w.window.Send(sel("close"))
	case nativeActivate:
		w.window.Send(sel("deminiaturize:"), objc.ID(0))
		w.window.Send(sel("makeKeyAndOrderFront:"), objc.ID(0))
		nsApp().Send(sel("activateIgnoringOtherApps:"), true)
	default:
		w.geometry(action, x, y)
	}
}

// geometry is the drag arithmetic shared with X11. Cocoa only reads and sets
// frames, translating its bottom-left origin to top-left desktop points.
func (w *nativeWindow) geometry(action, x, y int) {
	switch action {
	case nativeMoveBegin:
		cur := w.frame()
		pinned := objc.Send[bool](w.window, sel("isZoomed"))
		if pinned {
			w.window.Send(sel("zoom:"), objc.ID(0))
		}
		restored := w.frame()
		plan := planDragStart(pinned, cur, restored, x)
		restored.X, restored.Y = plan.X, plan.Y
		if plan.Restore {
			w.setFrame(restored)
		}
		w.moveStart = restored
	case nativeMoveBy:
		frame := w.frame()
		frame.X, frame.Y = clampMove(w.moveStart.X+x, w.moveStart.Y+y)
		w.setFrame(frame)
	case nativeResizeBegin:
		w.resizeStart = w.frame()
	default:
		directions := [...]string{"n", "s", "w", "e", "nw", "ne", "sw", "se"}
		direction := action - nativeResizeN
		if direction >= 0 && direction < len(directions) {
			if frame, ok := resizeRect(w.resizeStart, directions[direction], x, y); ok {
				w.setFrame(frame)
			}
		}
	}
}

func (w *nativeWindow) frame() windowRect {
	frame := objc.Send[nsRect](w.window, sel("frame"))
	top := desktopTop()
	return windowRect{
		X: int(frame.Origin.X), Y: int(top - (frame.Origin.Y + frame.Size.Height)),
		W: int(frame.Size.Width), H: int(frame.Size.Height),
	}
}

func (w *nativeWindow) setFrame(frame windowRect) {
	top := desktopTop()
	w.window.Send(sel("setFrame:display:"), nsRect{
		Origin: nsPoint{X: float64(frame.X), Y: top - float64(frame.Y) - float64(frame.H)},
		Size:   nsSize{Width: float64(frame.W), Height: float64(frame.H)},
	}, true)
}

// reserveDownload answers where a download may be written, and remembers it
// until the download ends. A destination must not exist - WebKit refuses a
// collision - and two downloads in flight must not be promised one name.
func (w *nativeWindow) reserveDownload(download objc.ID, suggested string) (string, bool) {
	directory := downloadsDirectory()
	if directory == "" {
		return "", false
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		nativeWarn("the downloads folder could not be created", err)
		return "", false
	}
	name := filepath.Base(suggested)
	if name == "" || name == "." || name == ".." || name == string(filepath.Separator) {
		name = "download"
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	promised := make(map[string]bool, len(w.downloads))
	for _, path := range w.downloads {
		promised[path] = true
	}
	extension := filepath.Ext(name)
	stem := strings.TrimSuffix(name, extension)
	destination := filepath.Join(directory, name)
	for suffix := 1; promised[destination] || pathExists(destination); suffix++ {
		destination = filepath.Join(directory, fmt.Sprintf("%s-%d%s", stem, suffix, extension))
	}
	w.downloads[download] = destination
	return destination, true
}

func (w *nativeWindow) forgetDownload(download objc.ID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.downloads, download)
}

// pathExists reports whether anything at all is at path, a dangling link
// included: WebKit is handed a name nothing occupies.
func pathExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func nativeWarn(message string, err error) {
	var log *slog.Logger
	if w := activeWindow(); w != nil {
		log = w.log
	}
	logger(log).Warn(message, "error", err)
}

// inDelegate runs the body of a delegate method. A panic must not cross back
// into Cocoa, which would end the process: the window keeps running, and the
// caller is given the answer the method's zero value stands for.
func inDelegate(what string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			nativeWarn(what+" failed", fmt.Errorf("%v", r))
		}
	}()
	fn()
}

// The delegate methods of CoddyNativeWindow. Each one is called by AppKit or
// WebKit on the main thread, with the window's own object as the receiver.

func windowWillClose(_ objc.ID, _ objc.SEL, _ objc.ID) {
	inDelegate("closing the window", func() {
		if w := activeWindow(); w != nil {
			w.setClosed()
		}
		nsApp().Send(sel("stop:"), objc.ID(0))
		wakeApplicationLoop()
	})
}

func quitAction(_ objc.ID, _ objc.SEL, _ objc.ID) {
	inDelegate("the quit request", func() {
		if w := activeWindow(); w != nil && w.window != 0 {
			w.window.Send(sel("close"))
		}
	})
}

func applicationWillFinishLaunching(self objc.ID, _ objc.SEL, _ objc.ID) {
	// Replace AppKit's quit handler after it installs the standard handlers.
	// AppKit's deferred termination enters a nested loop; the Go caller must
	// instead regain control to stop its HTTP surface and exit normally.
	inDelegate("installing the quit handler", func() {
		class("NSAppleEventManager").Send(sel("sharedAppleEventManager")).Send(
			sel("setEventHandler:andSelector:forEventClass:andEventID:"),
			self, sel("handleQuitEvent:withReplyEvent:"), kCoreEventClass, kAEQuitApplication)
	})
}

// handleQuitEvent returns normally, which acknowledges the system request
// without a veto.
func handleQuitEvent(self objc.ID, cmd objc.SEL, _, _ objc.ID) { quitAction(self, cmd, 0) }

// WebKit turns both the HTML download attribute and attachment responses into
// WKDownload objects, each announced through one of the two pairs below.

func decideNavigationAction(_ objc.ID, _ objc.SEL, _, action objc.ID, handler *nativeBlock) {
	policy := wkNavigationActionPolicyAllow
	inDelegate("deciding a navigation", func() {
		if boolOf(action, "shouldPerformDownload") {
			policy = wkNavigationActionPolicyDownload
		}
	})
	handler.call(uintptr(policy))
}

func decideNavigationResponse(_ objc.ID, _ objc.SEL, _, response objc.ID, handler *nativeBlock) {
	policy := wkNavigationResponsePolicyAllow
	inDelegate("deciding a response", func() {
		disposition := ""
		if answer := response.Send(sel("response")); answer != 0 &&
			objc.Send[bool](answer, sel("isKindOfClass:"), objc.GetClass("NSHTTPURLResponse")) {
			disposition = goString(answer.Send(sel("valueForHTTPHeaderField:"), nsString("Content-Disposition")))
		}
		if !boolOf(response, "canShowMIMEType") || strings.HasPrefix(strings.ToLower(disposition), "attachment") {
			policy = wkNavigationResponsePolicyDownload
		}
	})
	handler.call(uintptr(policy))
}

func actionBecameDownload(self objc.ID, _ objc.SEL, _, _, download objc.ID) {
	inDelegate("adopting a download", func() { download.Send(sel("setDelegate:"), self) })
}

func responseBecameDownload(self objc.ID, _ objc.SEL, _, _, download objc.ID) {
	inDelegate("adopting a download", func() { download.Send(sel("setDelegate:"), self) })
}

func decideDownloadDestination(_ objc.ID, _ objc.SEL, download, _, suggested objc.ID, handler *nativeBlock) {
	var destination objc.ID
	inDelegate("choosing a download destination", func() {
		w := activeWindow()
		if w == nil {
			return
		}
		path, ok := w.reserveDownload(download, goString(suggested))
		if !ok {
			return
		}
		destination = class("NSURL").Send(sel("fileURLWithPath:"), nsString(path))
	})
	handler.call(uintptr(destination))
}

func downloadFinished(_ objc.ID, _ objc.SEL, download objc.ID) {
	inDelegate("finishing a download", func() {
		if w := activeWindow(); w != nil {
			w.forgetDownload(download)
		}
	})
}

func downloadFailed(_ objc.ID, _ objc.SEL, download, _, _ objc.ID) {
	inDelegate("failing a download", func() {
		if w := activeWindow(); w != nil {
			w.forgetDownload(download)
		}
	})
}

// runOpenPanel answers a page's file input with the system's own picker, as a
// sheet of the window it was asked from.
func runOpenPanel(_ objc.ID, _ objc.SEL, _, parameters, _ objc.ID, handler *nativeBlock) {
	asked := false
	inDelegate("opening the file picker", func() {
		w := activeWindow()
		if w == nil || w.window == 0 {
			return
		}
		panel := class("NSOpenPanel").Send(sel("openPanel"))
		if panel == 0 {
			return
		}
		panel.Send(sel("setAllowsMultipleSelection:"), boolOf(parameters, "allowsMultipleSelection"))
		panel.Send(sel("setCanChooseDirectories:"), boolOf(parameters, "allowsDirectories"))
		panel.Send(sel("setCanChooseFiles:"), true)
		// The sheet answers after this method has returned, and WebKit's
		// handler belongs to the call: keep it alive until it is answered.
		answer := handler.retain()
		completion := objc.NewBlock(func(_ objc.Block, response int) {
			defer answer.release()
			var urls objc.ID
			if response == nsModalResponseOK {
				urls = panel.Send(sel("URLs"))
			}
			answer.call(uintptr(urls))
		})
		panel.Send(sel("beginSheetModalForWindow:completionHandler:"), w.window, completion)
		// The sheet copied the block; this run's own reference is finished.
		completion.Release()
		asked = true
	})
	if !asked {
		handler.call(0)
	}
}

// createWebView is where a page asks for a window Coddy does not give it. The
// address goes to the operator's browser when it is one a page may open, and
// nowhere otherwise.
func createWebView(_ objc.ID, _ objc.SEL, _, _, action, _ objc.ID) objc.ID {
	inDelegate("opening a link outside Coddy", func() {
		if url, ok := externalURL(action); ok {
			class("NSWorkspace").Send(sel("sharedWorkspace")).Send(sel("openURL:"), url)
		}
	})
	return 0
}

func nativeStoreID(dir string, log *slog.Logger) (string, error) {
	path := filepath.Join(dir, "id")
	b, err := os.ReadFile(path)
	if err == nil {
		value := strings.TrimSpace(string(b))
		if len(value) == 36 && value[8] == '-' && value[13] == '-' && value[18] == '-' && value[23] == '-' {
			// Only the version 4 identifier this function writes is kept.
			// +[WKWebsiteDataStore dataStoreForIdentifier:] raises
			// NSInvalidArgumentException for the all-zero identifier and for
			// ...0001, which WTF::UUID keeps for its own empty and deleted
			// values, and an uncaught Objective-C exception ends the process
			// instead of opening a window. Both fail the version and variant
			// bits below, so no identifier that reaches WebKit can raise.
			if decoded, decodeErr := hex.DecodeString(strings.ReplaceAll(value, "-", "")); decodeErr == nil &&
				len(decoded) == 16 && decoded[6]>>4 == 4 && decoded[8]>>6 == 0b10 {
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
