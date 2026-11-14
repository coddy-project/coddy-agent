//go:build desktop && darwin

package desktop

// Cocoa and WebKit, reached through the Objective-C runtime rather than a C
// bridge. Every class, selector and framework here is resolved at run time, so
// the window compiles with CGO_ENABLED=0 and the release cross-compiles it on
// the Linux runner like every other target (issue 744).
//
// Everything in this file runs on the thread init locked, which is the thread
// Cocoa calls its main one. The only exception is enqueueOnMain, which exists
// precisely so that a goroutine can ask for work there.

import (
	"fmt"
	"strings"
	"structs"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// The AppKit and WebKit values the window uses. Each one is an ABI constant of
// a released framework: they are what a header would have supplied.
const (
	nsApplicationActivationPolicyRegular = 0

	nsWindowStyleMaskTitled              = 1 << 0
	nsWindowStyleMaskClosable            = 1 << 1
	nsWindowStyleMaskMiniaturizable      = 1 << 2
	nsWindowStyleMaskResizable           = 1 << 3
	nsWindowStyleMaskFullSizeContentView = 1 << 15

	nsBackingStoreBuffered = 2

	nsWindowTitleVisible = 0
	nsWindowTitleHidden  = 1

	nsViewWidthSizable  = 1 << 1
	nsViewHeightSizable = 1 << 4

	nsEventTypeApplicationDefined = 15

	nsModalResponseOK = 1

	nsDownloadsDirectory = 15
	nsUserDomainMask     = 1

	wkNavigationActionPolicyAllow      = 1
	wkNavigationActionPolicyDownload   = 2
	wkNavigationResponsePolicyAllow    = 1
	wkNavigationResponsePolicyDownload = 2

	// 'aevt' and 'quit', the Apple event the Dock and ⌘Q send.
	kCoreEventClass    = 0x61657674
	kAEQuitApplication = 0x71756974

	kCFStringEncodingUTF8 = 0x08000100
)

// The geometry structures of AppKit, laid out as C sees them. purego passes a
// struct this size in registers, which is the ABI AppKit expects.
type (
	nsPoint struct {
		_    structs.HostLayout
		X, Y float64
	}
	nsSize struct {
		_             structs.HostLayout
		Width, Height float64
	}
	nsRect struct {
		_      structs.HostLayout
		Origin nsPoint
		Size   nsSize
	}
)

// nativeBlock is the header of an Objective-C block, which is what WebKit
// hands a delegate as a completion handler. Only its invoke function is read:
// the block belongs to WebKit, and calling it is the whole of the contract.
// Nothing here has a pointer field, so a block of WebKit's memory never gives
// Go's collector anything to trace.
type nativeBlock struct {
	_          structs.HostLayout
	isa        uintptr
	flags      int32
	reserved   int32
	invoke     uintptr
	descriptor uintptr
}

// call runs the block with arguments that are integers or object pointers,
// which is every completion handler the window is given.
func (b *nativeBlock) call(args ...uintptr) {
	if b == nil || b.invoke == 0 {
		return
	}
	purego.SyscallN(b.invoke, append([]uintptr{uintptr(unsafe.Pointer(b))}, args...)...)
}

// retain copies a block onto the heap, which is what keeps a completion
// handler alive past the delegate call that was given it. release gives that
// copy back.
func (b *nativeBlock) retain() *nativeBlock {
	if b == nil {
		return nil
	}
	return blockCopy(uintptr(unsafe.Pointer(b)))
}

func (b *nativeBlock) release() {
	if b != nil {
		blockRelease(uintptr(unsafe.Pointer(b)))
	}
}

var (
	cocoaOnce sync.Once
	cocoaErr  error

	dispatchAsyncF func(queue, context, fn uintptr)
	mainQueue      uintptr
	mainTrampoline uintptr

	blockCopy    func(block uintptr) *nativeBlock
	blockRelease func(block uintptr)

	cgSessionCopyCurrentDictionary func() uintptr
	cfDictionaryGetValue           func(dictionary, key uintptr) uintptr
	cfBooleanGetValue              func(boolean uintptr) uint8
	cfRelease                      func(ref uintptr)
	cfStringCreateWithCString      func(allocator uintptr, text string, encoding uint32) uintptr

	autoreleasePoolPush func() uintptr
	autoreleasePoolPop  func(pool uintptr)

	controllerClass objc.Class
)

// loadCocoa opens the frameworks the window needs and registers its delegate
// class, once per process. Every later call answers what the first one found.
func loadCocoa() error {
	cocoaOnce.Do(func() { cocoaErr = openCocoa() })
	return cocoaErr
}

func openCocoa() (err error) {
	// A framework this binary was not linked against is opened by name, so a
	// macOS that does not carry one reports it here instead of crashing later.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("desktop: the Objective-C runtime refused the window: %v", r)
		}
	}()
	handles := map[string]uintptr{}
	for _, path := range []string{
		"/usr/lib/libSystem.B.dylib",
		"/usr/lib/libobjc.A.dylib",
		"/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation",
		"/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics",
		"/System/Library/Frameworks/Cocoa.framework/Cocoa",
		"/System/Library/Frameworks/WebKit.framework/WebKit",
	} {
		handle, err := purego.Dlopen(path, purego.RTLD_GLOBAL|purego.RTLD_LAZY)
		if err != nil {
			return fmt.Errorf("desktop: loading %s: %w", path, err)
		}
		handles[path] = handle
	}
	system := handles["/usr/lib/libSystem.B.dylib"]
	objcLib := handles["/usr/lib/libobjc.A.dylib"]
	coreFoundation := handles["/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation"]
	coreGraphics := handles["/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics"]

	purego.RegisterLibFunc(&dispatchAsyncF, system, "dispatch_async_f")
	purego.RegisterLibFunc(&blockCopy, system, "_Block_copy")
	purego.RegisterLibFunc(&blockRelease, system, "_Block_release")
	purego.RegisterLibFunc(&autoreleasePoolPush, objcLib, "objc_autoreleasePoolPush")
	purego.RegisterLibFunc(&autoreleasePoolPop, objcLib, "objc_autoreleasePoolPop")
	purego.RegisterLibFunc(&cgSessionCopyCurrentDictionary, coreGraphics, "CGSessionCopyCurrentDictionary")
	purego.RegisterLibFunc(&cfDictionaryGetValue, coreFoundation, "CFDictionaryGetValue")
	purego.RegisterLibFunc(&cfBooleanGetValue, coreFoundation, "CFBooleanGetValue")
	purego.RegisterLibFunc(&cfRelease, coreFoundation, "CFRelease")
	purego.RegisterLibFunc(&cfStringCreateWithCString, coreFoundation, "CFStringCreateWithCString")

	// dispatch_get_main_queue() is a C inline that takes the address of this
	// object, so the symbol's own address is the queue.
	queue, err := purego.Dlsym(system, "_dispatch_main_q")
	if err != nil || queue == 0 {
		return fmt.Errorf("desktop: the main dispatch queue is missing: %w", err)
	}
	mainQueue = queue
	mainTrampoline = purego.NewCallback(runOnMain)
	return registerControllerClass()
}

var (
	selMu    sync.Mutex
	selNames = map[string]objc.SEL{}
)

// sel answers a cached selector. objc_registerName takes the runtime's global
// lock, and the window asks for the same few dozen selectors over and over.
func sel(name string) objc.SEL {
	selMu.Lock()
	defer selMu.Unlock()
	if cached, ok := selNames[name]; ok {
		return cached
	}
	registered := objc.RegisterName(name)
	selNames[name] = registered
	return registered
}

func class(name string) objc.ID { return objc.ID(objc.GetClass(name)) }

func nsApp() objc.ID { return class("NSApplication").Send(sel("sharedApplication")) }

func nsString(s string) objc.ID {
	return class("NSString").Send(sel("stringWithUTF8String:"), s)
}

// goString reads an NSString, answering "" for nil the way the Objective-C
// side treated a nil string as absent.
func goString(id objc.ID) string {
	if id == 0 {
		return ""
	}
	return objc.Send[string](id, sel("UTF8String"))
}

func boolOf(id objc.ID, name string) bool { return objc.Send[bool](id, sel(name)) }

// registerControllerClass builds the one class that answers for the window:
// its delegate, the application's, and WebKit's UI, navigation and download
// delegates, exactly as the Objective-C interface declared them.
func registerControllerClass() error {
	methods := []objc.MethodDef{
		{Cmd: sel("windowWillClose:"), Fn: windowWillClose},
		{Cmd: sel("quit:"), Fn: quitAction},
		{Cmd: sel("applicationWillFinishLaunching:"), Fn: applicationWillFinishLaunching},
		{Cmd: sel("handleQuitEvent:withReplyEvent:"), Fn: handleQuitEvent},
		{Cmd: sel("webView:runOpenPanelWithParameters:initiatedByFrame:completionHandler:"), Fn: runOpenPanel},
		{Cmd: sel("webView:decidePolicyForNavigationAction:decisionHandler:"), Fn: decideNavigationAction},
		{Cmd: sel("webView:decidePolicyForNavigationResponse:decisionHandler:"), Fn: decideNavigationResponse},
		{Cmd: sel("webView:navigationAction:didBecomeDownload:"), Fn: actionBecameDownload},
		{Cmd: sel("webView:navigationResponse:didBecomeDownload:"), Fn: responseBecameDownload},
		{Cmd: sel("download:decideDestinationUsingResponse:suggestedFilename:completionHandler:"), Fn: decideDownloadDestination},
		{Cmd: sel("downloadDidFinish:"), Fn: downloadFinished},
		{Cmd: sel("download:didFailWithError:resumeData:"), Fn: downloadFailed},
		{Cmd: sel("webView:createWebViewWithConfiguration:forNavigationAction:windowFeatures:"), Fn: createWebView},
	}
	// The protocols are declared where the runtime carries them, so that
	// -conformsToProtocol: answers for this class as it did for the compiled
	// one. A framework that does not publish one is no reason to refuse: AppKit
	// and WebKit dispatch a delegate through -respondsToSelector:.
	var protocols []*objc.Protocol
	for _, name := range []string{"NSWindowDelegate", "NSApplicationDelegate", "WKUIDelegate", "WKNavigationDelegate", "WKDownloadDelegate"} {
		if protocol := objc.GetProtocol(name); protocol != nil {
			protocols = append(protocols, protocol)
		}
	}
	registered, err := objc.RegisterClass("CoddyNativeWindow", objc.GetClass("NSObject"), protocols, nil, methods)
	if err != nil {
		return fmt.Errorf("desktop: registering the window delegate: %w", err)
	}
	controllerClass = registered
	return nil
}

var (
	queuedMu   sync.Mutex
	queuedNext uintptr
	queuedWork = map[uintptr]func(){}
)

// enqueueOnMain runs fn on Cocoa's main queue. Returning from this does not
// mean fn has run: a shell request is accepted here and carried out there, so
// an HTTP handler never waits on an application loop that may have stopped.
func enqueueOnMain(fn func()) {
	queuedMu.Lock()
	queuedNext++
	token := queuedNext
	queuedWork[token] = fn
	queuedMu.Unlock()
	dispatchAsyncF(mainQueue, token, mainTrampoline)
}

// runOnMain is what the main queue calls. The token stands in for the function
// because a dispatch context is a C pointer, and a Go closure is not one.
func runOnMain(token uintptr) {
	queuedMu.Lock()
	fn := queuedWork[token]
	delete(queuedWork, token)
	queuedMu.Unlock()
	if fn == nil {
		return
	}
	onMainThread("a queued window action", fn)
}

// onMainThread runs work under an autorelease pool, and keeps a failure inside
// the window: a panic crossing back into Cocoa would end the process, and the
// run this belongs to can still close its window and stop its servers.
func onMainThread(what string, fn func()) {
	pool := autoreleasePoolPush()
	defer autoreleasePoolPop(pool)
	defer func() {
		if r := recover(); r != nil {
			nativeWarn(what+" failed", fmt.Errorf("%v", r))
		}
	}()
	fn()
}

// guiSessionAvailable answers whether this login session owns the console, so
// that a headless launch falls back to the browser instead of Cocoa ending the
// process when it cannot reach the window server.
//
// A missing dictionary is the headless case: CGSessionCopyCurrentDictionary
// answers nothing over a plain ssh login. The console flag refines that to the
// session a person is actually looking at.
//
// Its key cannot be read from the framework: CGSession.h defines these keys as
// CFSTR(...) macros rather than exported symbols, which a compiler expands and
// dlsym cannot find. The string the macro expands to is built here instead,
// under both spellings the header has used, and a dictionary whose key is
// neither answers for the session it is - there is one, or there would be no
// dictionary.
func guiSessionAvailable() bool {
	session := cgSessionCopyCurrentDictionary()
	if session == 0 {
		return false
	}
	defer cfRelease(session)
	for _, name := range []string{"kCGSSessionOnConsoleKey", "kCGSessionOnConsoleKey"} {
		key := cfStringCreateWithCString(0, name, kCFStringEncodingUTF8)
		if key == 0 {
			continue
		}
		onConsole := cfDictionaryGetValue(session, key)
		cfRelease(key)
		if onConsole != 0 {
			return cfBooleanGetValue(onConsole) != 0
		}
	}
	return true
}

// isolatedStoresAvailable answers whether WebKit can keep this agent home's
// cookies and localStorage apart from another's, which is the macOS 14 class
// method the window is gated on. Asking the class itself is exact: the release
// binary starts on macOS 13, where the selector is absent.
func isolatedStoresAvailable() bool {
	store := objc.GetClass("WKWebsiteDataStore")
	if store == 0 {
		return false
	}
	return objc.Send[bool](objc.ID(store), sel("respondsToSelector:"), sel("dataStoreForIdentifier:"))
}

func onCocoaMainThread() bool {
	return objc.Send[bool](class("NSThread"), sel("isMainThread"))
}

// desktopTop is the highest visible point of every display, which turns
// Cocoa's bottom-left origin into the top-left one the shared drag arithmetic
// and the X11 backend both work in. One origin for all displays keeps gesture
// coordinates stable as a window crosses between them.
func desktopTop() float64 {
	top := 0.0
	screens := class("NSScreen").Send(sel("screens"))
	for i, n := 0, int(objc.Send[uint64](screens, sel("count"))); i < n; i++ {
		frame := objc.Send[nsRect](screens.Send(sel("objectAtIndex:"), i), sel("visibleFrame"))
		if maxY := frame.Origin.Y + frame.Size.Height; maxY > top {
			top = maxY
		}
	}
	return top
}

// decorate puts the system frame on the window or takes it off. The window
// keeps NSWindowStyleMaskTitled either way: a borderless window cannot become
// the key window, which would leave the WKWebView unable to accept input.
func decorate(window objc.ID, framed bool) {
	mask := nsWindowStyleMaskTitled | nsWindowStyleMaskClosable |
		nsWindowStyleMaskMiniaturizable | nsWindowStyleMaskResizable
	if !framed {
		mask |= nsWindowStyleMaskFullSizeContentView
	}
	window.Send(sel("setStyleMask:"), mask)
	title := nsWindowTitleHidden
	if framed {
		title = nsWindowTitleVisible
	}
	window.Send(sel("setTitleVisibility:"), title)
	window.Send(sel("setTitlebarAppearsTransparent:"), !framed)
	for _, button := range []int{0, 1, 2} { // close, miniaturise, zoom
		if view := window.Send(sel("standardWindowButton:"), button); view != 0 {
			view.Send(sel("setHidden:"), !framed)
		}
	}
}

// installMenu gives the application the menu bar macOS expects. The shell
// interprets the letter, digit and Ctrl+Tab chords it receives itself;
// command shortcuts may be consumed by WebKit before they reach the page.
func installMenu(controller objc.ID) {
	menu := class("NSMenu").Send(sel("alloc")).Send(sel("init"))
	application := class("NSMenuItem").Send(sel("alloc")).Send(sel("init"))
	menu.Send(sel("addItem:"), application)
	applicationMenu := class("NSMenu").Send(sel("alloc")).Send(sel("initWithTitle:"), nsString("Coddy"))
	application.Send(sel("setSubmenu:"), applicationMenu)
	quit := applicationMenu.Send(sel("addItemWithTitle:action:keyEquivalent:"), nsString("Quit Coddy"), sel("quit:"), nsString("q"))
	quit.Send(sel("setTarget:"), controller)

	edit := class("NSMenuItem").Send(sel("alloc")).Send(sel("init"))
	menu.Send(sel("addItem:"), edit)
	editMenu := class("NSMenu").Send(sel("alloc")).Send(sel("initWithTitle:"), nsString("Edit"))
	edit.Send(sel("setSubmenu:"), editMenu)
	for _, entry := range []struct{ title, action, key string }{
		{"Undo", "undo:", "z"},
		{"Cut", "cut:", "x"},
		{"Copy", "copy:", "c"},
		{"Paste", "paste:", "v"},
		{"Select All", "selectAll:", "a"},
	} {
		editMenu.Send(sel("addItemWithTitle:action:keyEquivalent:"), nsString(entry.title), sel(entry.action), nsString(entry.key))
	}
	nsApp().Send(sel("setMainMenu:"), menu)
}

// wakeApplicationLoop posts an event the loop has no use for. -[NSApplication
// stop:] takes effect only after the current event is dispatched, and a close
// asked for from the main queue is not an event, so a loop idle in
// nextEventMatchingMask would otherwise never look at the flag.
func wakeApplicationLoop() {
	event := class("NSEvent").Send(sel("otherEventWithType:location:modifierFlags:timestamp:windowNumber:context:subtype:data1:data2:"),
		nsEventTypeApplicationDefined, nsPoint{}, uint64(0), float64(0), 0, objc.ID(0), int16(0), 0, 0)
	if event != 0 {
		nsApp().Send(sel("postEvent:atStart:"), event, false)
	}
}

// externalURL answers the address a page asked to open in a new window, and
// "" for everything else. It matches shell.ExternalURL: only a bounded,
// absolute HTTP(S) link leaves Coddy, so a page can invoke neither file: nor
// another application's URL scheme.
func externalURL(action objc.ID) (objc.ID, bool) {
	if action.Send(sel("targetFrame")) != 0 {
		return 0, false
	}
	url := action.Send(sel("request")).Send(sel("URL"))
	if url == 0 {
		return 0, false
	}
	raw := goString(url.Send(sel("absoluteString")))
	scheme := strings.ToLower(goString(url.Send(sel("scheme"))))
	host := goString(url.Send(sel("host")))
	if len(raw) > 4096 || host == "" || (scheme != "http" && scheme != "https") {
		return 0, false
	}
	return url, true
}

// downloadsDirectory is where this user's downloads go, as Foundation reports
// it: a relocated Downloads folder is honoured, and a sandboxed or relocated
// home (CFFIXED_USER_HOME) answers with its own.
func downloadsDirectory() string {
	manager := class("NSFileManager").Send(sel("defaultManager"))
	urls := manager.Send(sel("URLsForDirectory:inDomains:"), nsDownloadsDirectory, nsUserDomainMask)
	first := urls.Send(sel("firstObject"))
	if first == 0 {
		return ""
	}
	return goString(first.Send(sel("path")))
}
