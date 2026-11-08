//go:build desktop

package desktop

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
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
}

// NewWindowHandle answers an empty handle.
func NewWindowHandle() *WindowHandle { return &WindowHandle{} }

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

// NextChord waits for a key the launcher took from the browser.
//
// The page asks for one the moment it is drawn, which is before the browser
// has mapped a window and therefore before anything can be grabbed, so a
// handle with no window yet waits rather than answering no: the long poll the
// page is already holding is the one that delivers the first chord.
func (h *WindowHandle) NextChord(ctx context.Context) string {
	for {
		if src, ok := h.current().(shell.ChordSource); ok {
			return src.NextChord(ctx)
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(chordAttachPoll):
		}
	}
}

// chordAttachPoll is how often a page that is already waiting looks again for
// a window to take its chords from.
const chordAttachPoll = 250 * time.Millisecond

// Activate brings the window back, for the tray icon to call. A window that
// cannot be asked is not an error here: there is nothing to bring back.
func (h *WindowHandle) Activate() error {
	if a, ok := h.current().(windowActivator); ok {
		return a.activate()
	}
	return nil
}

// RequestChord asks the strip for what that chord does, from somewhere that is
// not a key: a row of the tray's menu. It travels down the same queue a grabbed
// key does, so the strip needs to know nothing about where it came from.
func (h *WindowHandle) RequestChord(name string) {
	if p, ok := h.current().(chordPusher); ok {
		p.pushChord(name)
	}
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

// chordGrabber is the part of a window control that can take a chord away from
// the browser before the browser answers it itself.
type chordGrabber interface {
	grabChords()
}

// chordPusher is the part of a window control that holds the queue the strip
// polls, so something that is not a key can ask the strip for the same thing.
type chordPusher interface {
	pushChord(name string)
}

// windowActivator is the part of a window control that can bring the window
// back from the tray or from behind another window.
type windowActivator interface {
	activate() error
}
