//go:build desktop

package desktop

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// WindowControl is the strip's view of the window around it; the interface
// itself lives in the shell package, which both sides already import.
type WindowControl = shell.WindowControl

// WindowHandle is what the server is given before there is a window: the page
// is served the moment the surface is up, and the browser window appears a
// second later. The launcher attaches the real control when it has one, and
// until then every button politely says it cannot be served.
type WindowHandle struct {
	mu       sync.Mutex
	ctl      WindowControl
	settings *SettingsStore
	// chords is what the page long-polls: the keys the launcher took from the
	// browser, and the messages that are not keys at all - a row of the tray's
	// menu asking for a new chat, a switch that moved where the page cannot
	// see it.
	//
	// It belongs to the handle and not to the window on purpose. While it
	// lived in the X11 control, a machine where no window can be driven - a
	// Wayland session, macOS, Windows - dropped every one of those messages,
	// though it still had a tray icon to pick New chat from and switches that
	// move outside the page.
	chords *chordQueue
}

// NewWindowHandle answers an empty handle.
func NewWindowHandle() *WindowHandle { return &WindowHandle{chords: newChordQueue()} }

// Attach gives the handle the window this run opened.
func (h *WindowHandle) Attach(ctl WindowControl) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ctl = ctl
}

func (h *WindowHandle) current() WindowControl {
	if h == nil {
		return noWindowControl{}
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ctl == nil {
		return noWindowControl{}
	}
	return h.ctl
}

// AttachSettings gives the handle the switches of the menu. The page reads and
// writes them through the same handler the window buttons go through, because
// that is what the server was handed.
func (h *WindowHandle) AttachSettings(s *SettingsStore) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.settings = s
}

func (h *WindowHandle) Settings() Settings {
	h.mu.Lock()
	s := h.settings
	h.mu.Unlock()
	return s.Settings()
}

func (h *WindowHandle) SetSettings(next Settings) error {
	h.mu.Lock()
	s := h.settings
	h.mu.Unlock()
	if s == nil {
		return errors.New("desktop: this window keeps no settings")
	}
	return s.SetSettings(next)
}

// RequestClose is what the shell's close button means. A visible tray gives
// the window somewhere to stay; without one the request remains a real close,
// so a saved preference cannot strand a hidden process.
func (h *WindowHandle) RequestClose() error {
	h.mu.Lock()
	settings := h.settings
	h.mu.Unlock()
	if settings != nil {
		return settings.requestClose(h)
	}
	return h.Close()
}

// NextChord waits for the next thing the strip is told.
//
// The page asks the moment it is drawn, which is before the browser has
// mapped a window and therefore before anything can be grabbed, so this waits
// rather than answering no: the long poll the page is already holding is the
// one that delivers the first chord.
func (h *WindowHandle) NextChord(ctx context.Context) shell.Chord {
	if h == nil || h.chords == nil {
		<-ctx.Done()
		return shell.Chord{}
	}
	return h.chords.next(ctx)
}

// pushGrabbed is how a key taken at the X server reaches the strip. The window
// control calls it; nothing else does.
func (h *WindowHandle) pushGrabbed(name string) {
	h.push(shell.Chord{Name: name})
}

func (h *WindowHandle) push(msg shell.Chord) {
	if h == nil || h.chords == nil {
		return
	}
	h.chords.push(msg)
}

// OpenExternal hands a link out of Coddy to this machine's own browser.
//
// It does not go through the window at all - the window is a browser in
// application mode on this one address, and loading a site into it would put
// that site under a tab strip with no address bar and no way back. The
// opener is the desktop's own (`xdg-open` and its kin), so the link lands in
// whatever browser the person actually uses. The address has been through
// shell.ExternalURL before it reaches here.
func (h *WindowHandle) OpenExternal(url string) error {
	return platform.OpenBrowser(url)
}

// Activate brings the window back, for the tray icon to call. A window that
// cannot be asked is not an error here: there is nothing to bring back.
func (h *WindowHandle) Activate() error {
	if a, ok := h.current().(windowActivator); ok {
		return a.activate()
	}
	return nil
}

// RequestChord asks the strip for what that chord does, from somewhere that is
// not a key: a row of the tray's menu, or a switch that moved where the page
// cannot see it. It travels down the same queue a grabbed key does, so the
// strip needs to know nothing about where it came from.
func (h *WindowHandle) RequestChord(name string) { h.pushGrabbed(name) }

// OpenTab asks the strip for a new chat in a folder, which is what a row of
// the task bar's menu means by a recent workspace. It travels down the same
// queue, with the folder on it.
func (h *WindowHandle) OpenTab(cwd string) error {
	h.push(shell.Chord{Name: chordNewTab, CWD: cwd})
	return nil
}

func (h *WindowHandle) Supported() bool                    { return h.current().Supported() }
func (h *WindowHandle) Undecorate() error                  { return h.current().Undecorate() }
func (h *WindowHandle) Decorate() error                    { return h.current().Decorate() }
func (h *WindowHandle) Minimise() error                    { return h.current().Minimise() }
func (h *WindowHandle) ToggleMaximise() error              { return h.current().ToggleMaximise() }
func (h *WindowHandle) Close() error                       { return h.current().Close() }
func (h *WindowHandle) BeginMove(x, y int) error           { return h.current().BeginMove(x, y) }
func (h *WindowHandle) MoveBy(dx, dy int) error            { return h.current().MoveBy(dx, dy) }
func (h *WindowHandle) WaitForWindow(b time.Duration) bool { return h.current().WaitForWindow(b) }

// noWindowControl is every platform the window is not ported to, and the
// moment before a window exists. It answers no to everything and fails
// nothing destructive.
type noWindowControl struct{}

func (noWindowControl) Supported() bool                  { return false }
func (noWindowControl) Undecorate() error                { return errWindowUnsupported }
func (noWindowControl) Decorate() error                  { return errWindowUnsupported }
func (noWindowControl) Minimise() error                  { return errWindowUnsupported }
func (noWindowControl) ToggleMaximise() error            { return errWindowUnsupported }
func (noWindowControl) Close() error                     { return errWindowUnsupported }
func (noWindowControl) BeginMove(_, _ int) error         { return errWindowUnsupported }
func (noWindowControl) MoveBy(_, _ int) error            { return errWindowUnsupported }
func (noWindowControl) WaitForWindow(time.Duration) bool { return false }

// cornerRounder is the part of a window control that can cut the window's
// corners. Only the X11 one can, so the launcher asks for it by assertion
// rather than growing the interface every platform has to satisfy.
type cornerRounder interface {
	roundCorners()
	watchShape(stop <-chan struct{})
}

// chordGrabber is the part of a window control that can take a chord away
// from the browser before the browser answers it itself. It is given the
// place to put what it took, which is the handle's queue: one queue for the
// keys and for everything else the strip is told.
type chordGrabber interface {
	grabChords(deliver func(string))
}

// windowIconSetter is the part of a window control that can put Coddy's mark
// on the window itself, for a task bar that has no installed application
// entry to take an icon from. Only the X11 one can.
type windowIconSetter interface {
	setIcon()
}

// windowActivator is the part of a window control that can bring the window
// back from the tray or from behind another window.
type windowActivator interface {
	activate() error
}
