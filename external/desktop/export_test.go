//go:build desktop

package desktop

import (
	"context"
	"sync"

	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
)

// What the godog harness of features/desktop_window.feature drives directly.
// The window itself needs an X server and a running window manager, so the
// arithmetic of a drag is what the scenarios can hold: planDragStart decides
// what happens when a pinned window is grabbed, clampMove where a move may put
// it.

type WindowRect = windowRect

type DragStart = dragStart

func PlanDragStart(pinned bool, cur, restored WindowRect, pointerX int) DragStart {
	return planDragStart(pinned, cur, restored, pointerX)
}

func ClampMove(x, y int) (int, int) { return clampMove(x, y) }

// The chords the launcher takes from the browser: what a key means, and the
// queue that carries it from the X event loop to the page's long poll. The
// grab itself needs an X server and a browser that answers the key, which is
// what the probe in docs/plans/desktop.md measures; the scenario holds what
// happens once a key has been taken.

func ChordOf(sym uint32, shift bool) (string, bool) { return chordOf(sym, shift) }

func KeysymOf(key string) uint32 {
	switch key {
	case "T", "t":
		return keysymT
	case "W", "w":
		return keysymW
	case "Tab":
		return keysymTab
	case "R", "r":
		return keysymR
	}
	return 0
}

// ChordHandle is a window that takes chords, for a scenario to press keys on.
type ChordHandle struct {
	WindowControl
	queue *chordQueue
}

func NewChordHandle() *ChordHandle {
	return &ChordHandle{WindowControl: noWindowControl{}, queue: newChordQueue()}
}

// Press is what the grab does when the key is pressed.
func (h *ChordHandle) Press(chord string) { h.queue.push(shell.Chord{Name: chord}) }

func (h *ChordHandle) NextChord(ctx context.Context) shell.Chord { return h.queue.next(ctx) }

// GrabMasks is the modifier combinations one chord is grabbed under.
func GrabMasks(base uint16, locks ...uint16) []uint16 { return grabMasks(base, locks...) }

// The menu, which is drawn twice - by the button in the bar and by the tray
// icon - off one list. What a scenario needs is a window that says it can be
// asked and remembers what the menu told it, since the real one is an X11
// session with a tray on its message bus.

// MenuWindow is that window.
type MenuWindow struct {
	noWindowControl
	queue *chordQueue

	mu        sync.Mutex
	activated int
	minimised int
	closed    int
	events    []string
}

func NewMenuWindow() *MenuWindow { return &MenuWindow{queue: newChordQueue()} }

func (w *MenuWindow) Supported() bool   { return true }
func (w *MenuWindow) Decorate() error   { return nil }
func (w *MenuWindow) Undecorate() error { return nil }

func (w *MenuWindow) activate() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.activated++
	w.events = append(w.events, "activate")
	return nil
}

// RecordEvent lets a tray double share the window's operation journal.
func (w *MenuWindow) RecordEvent(event string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.events = append(w.events, event)
}

// Events returns the window and tray operations in their actual order.
func (w *MenuWindow) Events() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.events...)
}

func (w *MenuWindow) Minimise() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.minimised++
	w.events = append(w.events, "minimise")
	return nil
}

func (w *MenuWindow) NextChord(ctx context.Context) shell.Chord { return w.queue.next(ctx) }

// Shown and Hidden are how many times the menu asked for each.
func (w *MenuWindow) Shown() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.activated
}

func (w *MenuWindow) Hidden() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.minimised
}

func (w *MenuWindow) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed++
	w.events = append(w.events, "close")
	return nil
}

// Closed is how many times the menu asked for the application to end.
func (w *MenuWindow) Closed() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

// BindWindow is what the launcher does once the browser has a window: it gives
// the settings store the window a switch and a menu row act on.
func (s *SettingsStore) BindWindow(h *WindowHandle) { s.bind(h, nil) }

// TrayPresence is a tray the close scenarios can make visible without a
// session bus. It satisfies the store's small tray interface.
type TrayPresence struct{ visible bool }

// TrayControl lets a blocking test double drive the same store boundary.
type TrayControl = trayControl

func NewTrayPresence(visible bool) *TrayPresence { return &TrayPresence{visible: visible} }
func (t *TrayPresence) Show() error              { t.visible = true; return nil }
func (t *TrayPresence) Hide()                    { t.visible = false }
func (t *TrayPresence) Visible() bool            { return t.visible }

// BindWindowAndTray gives the settings store both sides of a close request.
func (s *SettingsStore) BindWindowAndTray(h *WindowHandle, tray TrayControl) {
	s.bind(h, tray)
}

// ShellWindow is a window the shell's own routes can be driven against: it can
// be asked, and it writes down what it was asked for rather than touching a
// display or starting a browser.
type ShellWindow struct {
	noWindowControl

	mu     sync.Mutex
	moves  [][2]int
	opened []string
	tabs   []string
}

func NewShellWindow() *ShellWindow { return &ShellWindow{} }

func (w *ShellWindow) Supported() bool { return true }

func (w *ShellWindow) BeginMove(_, _ int) error { return nil }

func (w *ShellWindow) MoveBy(dx, dy int) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.moves = append(w.moves, [2]int{dx, dy})
	return nil
}

// OpenTab is the strip being asked for a chat in a folder.
func (w *ShellWindow) OpenTab(cwd string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.tabs = append(w.tabs, cwd)
	return nil
}

func (w *ShellWindow) Tabs() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.tabs...)
}

// OpenExternal is this machine's own browser being started on a link.
func (w *ShellWindow) OpenExternal(url string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.opened = append(w.opened, url)
	return nil
}

func (w *ShellWindow) Moves() [][2]int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([][2]int(nil), w.moves...)
}

func (w *ShellWindow) Opened() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.opened...)
}
