//go:build desktop && linux && !android

package desktop

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// The window of a browser started in application mode belongs to the window
// manager, not to the page inside it, so everything a frameless window needs -
// taking the frame off, minimising, maximising, moving - is asked of the window
// manager over the protocol a task bar or a pager uses: EWMH client messages
// (freedesktop.org's Extended Window Manager Hints) and, for the frame itself,
// the older Motif hint every manager still honours.
//
// It is X11 only, and deliberately so: on Wayland a client cannot ask the
// compositor to move or undecorate somebody else's surface at all, by design.
// A Wayland session therefore keeps the system frame, which is what
// Supported() reports, and the window around the tab strip is an ordinary
// decorated window. The native window of the desktop.native tag is what closes
// that gap, because there the window is this process's own.

var errWindowUnsupported = errors.New("desktop: this session cannot be asked to change the window")

// x11Control talks to the X server about one window: the browser window this
// run started, found by the process id the window names.
type x11Control struct {
	log *slog.Logger
	pid int

	conn *xgb.Conn
	root xproto.Window
	win  xproto.Window

	// shapeOnce loads the Shape extension on first use; shaped says whether
	// this server has it (window_shape_x11.go).
	shapeOnce sync.Once
	shaped    bool

	// deliver is where a grabbed key goes: the handle's queue, which the page
	// long-polls. It is set when the chords are grabbed.
	deliver func(string)
	// icon is the _NET_WM_ICON property's bytes, built once (window_icon_x11.go).
	icon []byte

	// shapeRects is the shape the window has now, kept so a drag can lay it
	// again per frame without asking the server about the window first
	// (window_shape_x11.go). Nil while the window has no corners to keep.
	shapeRects []xproto.Rectangle

	// mu guards where a drag started from, which the page sets once and then
	// measures every move against.
	mu               sync.Mutex
	originX, originY int
}

// newWindowControl answers the control for this session. A session that is not
// X11, or a server that will not talk to us, answers the no-op control, so a
// caller never has to ask which platform it is on.
func newWindowControl(log *slog.Logger, pid int) WindowControl {
	c, err := xgb.NewConn()
	if err != nil {
		logger(log).Debug("the desktop cannot reach an X server, so the window keeps its own frame", "error", err)
		return noWindowControl{}
	}
	return &x11Control{
		log:  log,
		pid:  pid,
		conn: c,
		root: xproto.Setup(c).DefaultScreen(c).Root,
	}
}

func (c *x11Control) Supported() bool { return c != nil && c.conn != nil }

// atom interns a name once per call; the X server caches them, and a desktop
// asks for a handful of them over its lifetime.
func (c *x11Control) atom(name string) (xproto.Atom, error) {
	// G115: every name this is called with is one of the EWMH and Motif
	// constants spelled out in this file, none of them longer than thirty
	// characters.
	r, err := xproto.InternAtom(c.conn, false, uint16(len(name)), name).Reply() //nolint:gosec // G115: see above.
	if err != nil {
		return 0, err
	}
	return r.Atom, nil
}

// ours reports whether a window's process belongs to this run.
//
// It is a walk up the process tree rather than one comparison, because the
// process a browser names in its window is not the process we started: the
// launcher starts /usr/bin/google-chrome and the window belongs to a browser
// process that one brought up, a different pid of the same family. Matching
// the pid alone found no window at all, and matching the window class instead
// would take the window of another agent home, since the class of an
// application-mode window comes from the address and two desktops share it.
func (c *x11Control) ours(pid int) bool {
	for hop := 0; pid > 1 && hop < 24; hop++ {
		if pid == c.pid || pid == os.Getpid() {
			return true
		}
		parent, ok := parentOf(pid)
		if !ok {
			return false
		}
		pid = parent
	}
	return false
}

// parentOf reads a process's parent out of /proc. The field is the fourth of
// stat, after the executable name, which is in brackets and may itself hold
// spaces or brackets - so the line is read from its last bracket on.
func parentOf(pid int) (int, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, false
	}
	line := string(b)
	i := strings.LastIndex(line, ")")
	if i < 0 {
		return 0, false
	}
	fields := strings.Fields(line[i+1:])
	if len(fields) < 2 {
		return 0, false
	}
	parent, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, false
	}
	return parent, true
}

// window finds the browser window of this run and remembers it.
func (c *x11Control) window() (xproto.Window, error) {
	if c.win != 0 {
		if _, err := xproto.GetWindowAttributes(c.conn, c.win).Reply(); err == nil {
			return c.win, nil
		}
		c.win = 0
	}
	list, err := c.atom("_NET_CLIENT_LIST")
	if err != nil {
		return 0, err
	}
	pidAtom, err := c.atom("_NET_WM_PID")
	if err != nil {
		return 0, err
	}
	reply, err := xproto.GetProperty(c.conn, false, c.root, list, xproto.GetPropertyTypeAny, 0, 1<<16).Reply()
	if err != nil {
		return 0, err
	}
	for i := 0; i+4 <= len(reply.Value); i += 4 {
		w := xproto.Window(xgb.Get32(reply.Value[i:]))
		p, err := xproto.GetProperty(c.conn, false, w, pidAtom, xproto.GetPropertyTypeAny, 0, 1).Reply()
		if err != nil || len(p.Value) < 4 {
			continue
		}
		if c.ours(int(xgb.Get32(p.Value))) {
			c.win = w
			return w, nil
		}
	}
	return 0, fmt.Errorf("desktop: no window of process %d is on this display", c.pid)
}

// WaitForWindow gives the browser time to map its window before the first
// request to it. A window that never appears is not an error: the frame stays
// as it is and the buttons say so.
func (c *x11Control) WaitForWindow(bound time.Duration) bool {
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if _, err := c.window(); err == nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// message sends an EWMH client message to the root window, which is how a
// pager, a task bar and this strip all ask the window manager for something.
func (c *x11Control) message(name string, data ...uint32) error {
	win, err := c.window()
	if err != nil {
		return err
	}
	typ, err := c.atom(name)
	if err != nil {
		return err
	}
	var d [5]uint32
	copy(d[:], data)
	ev := xproto.ClientMessageEvent{
		Format: 32, Window: win, Type: typ,
		Data: xproto.ClientMessageDataUnionData32New(d[:]),
	}
	return xproto.SendEventChecked(c.conn, false, c.root,
		uint32(xproto.EventMaskSubstructureNotify|xproto.EventMaskSubstructureRedirect),
		string(ev.Bytes())).Check()
}

// setMotif writes the hint that decides whether the window manager draws a
// frame, and makes the manager read it. The window keeps its state (maximised,
// focused) across it, because that state is the manager's and the window is the
// same window.
func (c *x11Control) setMotif(decorations bool) error {
	win, err := c.window()
	if err != nil {
		return err
	}
	hints, err := c.atom("_MOTIF_WM_HINTS")
	if err != nil {
		return err
	}
	// flags = MWM_HINTS_DECORATIONS, then functions, decorations, input mode,
	// status: five 32-bit words, little-endian like every X property here.
	data := make([]byte, 20)
	xgb.Put32(data[0:], 2)
	if decorations {
		xgb.Put32(data[8:], 1)
	}
	// The order is the whole trick, and it is not the obvious one.
	//
	// A window manager reads this hint when it takes a window under management
	// and never watches it afterwards, so writing it to a window already on
	// screen changes nothing - which is what a first attempt looks like when it
	// quietly does not work. Unmapping and mapping the window makes the manager
	// manage it afresh. But the browser owns this property too and writes its
	// own value (decorated) whenever it settles its window, so writing first
	// and mapping second loses the race about half the time: the manager reads
	// the browser's value, and the frame comes back.
	//
	// Written while the window is down, and mapped straight after, the manager
	// reads ours. A later write by the browser changes nothing, because by then
	// the manager has stopped looking.
	if err := xproto.UnmapWindowChecked(c.conn, win).Check(); err != nil {
		return err
	}
	if err := xproto.ChangePropertyChecked(c.conn, xproto.PropModeReplace, win, hints, hints, 32, 5, data).Check(); err != nil {
		return err
	}
	return xproto.MapWindowChecked(c.conn, win).Check()
}

// Undecorate takes the frame off and makes sure it stayed off.
//
// The browser writes this property for its own window too, so the manager can
// read the browser's value instead of ours and put the frame back - which
// happens on a cold start, where the browser is still settling the window as
// we ask. The result is therefore read back, and the dance repeated a few
// times before giving up; a window that keeps its frame is a worse look, never
// a broken one.
func (c *x11Control) Undecorate() error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if err = c.setMotif(false); err != nil {
			return err
		}
		if !c.framed() {
			return nil
		}
		time.Sleep(time.Duration(300+attempt*300) * time.Millisecond)
	}
	if c.framed() {
		return errors.New("desktop: the window manager kept the frame on this window")
	}
	return err
}

func (c *x11Control) Decorate() error { return c.setMotif(true) }

// states answers the window's _NET_WM_STATE as the manager publishes it.
func (c *x11Control) states() []xproto.Atom {
	win, err := c.window()
	if err != nil {
		return nil
	}
	state, err := c.atom("_NET_WM_STATE")
	if err != nil {
		return nil
	}
	p, err := xproto.GetProperty(c.conn, false, win, state, xproto.GetPropertyTypeAny, 0, 64).Reply()
	if err != nil {
		return nil
	}
	out := make([]xproto.Atom, 0, len(p.Value)/4)
	for i := 0; i+4 <= len(p.Value); i += 4 {
		out = append(out, xproto.Atom(xgb.Get32(p.Value[i:])))
	}
	return out
}

func (c *x11Control) hasState(name string) bool {
	want, err := c.atom(name)
	if err != nil {
		return false
	}
	for _, s := range c.states() {
		if s == want {
			return true
		}
	}
	return false
}

// maximisedAxes reports which axes the window manager has the window filling.
//
// A quick-tiled window - half the screen, Meta+Left on this desktop - is not a
// state of its own in EWMH: KWin gives it _NET_WM_STATE_MAXIMIZED_VERT and
// half the width, which is why a drag asks about these two and not about
// tiling.
func (c *x11Control) maximisedAxes() (vert, horz bool) {
	v, err := c.atom("_NET_WM_STATE_MAXIMIZED_VERT")
	if err != nil {
		return false, false
	}
	h, err := c.atom("_NET_WM_STATE_MAXIMIZED_HORZ")
	if err != nil {
		return false, false
	}
	for _, s := range c.states() {
		switch s {
		case v:
			vert = true
		case h:
			horz = true
		}
	}
	return vert, horz
}

// framed reports whether the manager is drawing a frame right now: a window
// with no decoration has no frame extents, or extents of nothing.
func (c *x11Control) framed() bool {
	win, err := c.window()
	if err != nil {
		return false
	}
	extents, err := c.atom("_NET_FRAME_EXTENTS")
	if err != nil {
		return false
	}
	p, err := xproto.GetProperty(c.conn, false, win, extents, xproto.GetPropertyTypeAny, 0, 4).Reply()
	if err != nil || len(p.Value) < 16 {
		return false
	}
	for i := 0; i+4 <= len(p.Value); i += 4 {
		if xgb.Get32(p.Value[i:]) != 0 {
			return true
		}
	}
	return false
}

// Minimise is ICCCM's, not EWMH's: _NET_WM_STATE_HIDDEN is a state a manager
// reports, never one a client sets, and WM_CHANGE_STATE to IconicState (3) is
// how a window asks to be iconified.
func (c *x11Control) Minimise() error { return c.message("WM_CHANGE_STATE", 3) }

// ToggleMaximise asks for both axes at once with the toggle action (2), so the
// strip's button needs to know nothing about what the window is doing now.
func (c *x11Control) ToggleMaximise() error {
	vert, err := c.atom("_NET_WM_STATE_MAXIMIZED_VERT")
	if err != nil {
		return err
	}
	horz, err := c.atom("_NET_WM_STATE_MAXIMIZED_HORZ")
	if err != nil {
		return err
	}
	// 2 = toggle, and the last word is the source indication: 1, a normal
	// application, which is what the window manager's focus-stealing rules
	// expect from a window asking about itself.
	return c.message("_NET_WM_STATE", 2, uint32(vert), uint32(horz), 1)
}

// Close is the polite close: the manager asks the window, the browser shuts
// down, the launcher's Wait returns and the run ends - the same path as the
// system close button, so nothing of the session is lost.
func (c *x11Control) Close() error { return c.message("_NET_CLOSE_WINDOW", 0, 1) }

// BeginMove and MoveBy move the window from the page, rather than handing it
// to the window manager's own move loop.
//
// The obvious implementation is _NET_WM_MOVERESIZE, which is what a title bar
// sends, and it is wrong here: the move loop ends when the window manager sees
// the mouse button released, and the button was pressed inside the browser's
// window, so the release goes to the browser and never to the manager. The
// window then follows the pointer after the button is up, until something else
// takes the grab - exactly as if a drag had got stuck. The page holds the
// pointer for the whole gesture (it captured it), so the page is what has to
// drive the move and end it.
//
// What is lost is the manager's own snapping and edge tiling during the drag.
// What is gained is a drag that stops when the button does.
// BeginMove takes note of where the window stands, and lets it loose when the
// window manager has it pinned - maximised, or quick-tiled against an edge.
// pointerX and pointerY are where the pointer took hold, in root coordinates,
// so a window that gets its own size back can be put back under it
// (planDragStart, drag.go).
func (c *x11Control) BeginMove(pointerX, pointerY int) error {
	cur, err := c.rect()
	if err != nil {
		return err
	}
	vert, horz := c.maximisedAxes()
	plan := planDragStart(vert || horz, cur, cur, pointerX)
	if plan.Restore {
		if err := c.unpin(); err != nil {
			return err
		}
		// The manager answers a state change by resizing the window, and the
		// size it ends at is what the new position is measured from.
		restored := c.settled(cur)
		plan = planDragStart(true, cur, restored, pointerX)
		if err := c.place(plan.X, plan.Y); err != nil {
			return err
		}
	}
	c.mu.Lock()
	c.originX, c.originY = plan.X, plan.Y
	c.mu.Unlock()
	return nil
}

func (c *x11Control) MoveBy(dx, dy int) error {
	c.mu.Lock()
	x, y := c.originX+dx, c.originY+dy
	c.mu.Unlock()
	x, y = clampMove(x, y)
	if err := c.place(x, y); err != nil {
		return err
	}
	// The browser clears the window's shape whenever its bounds change, so the
	// corners are laid again in the same frame the window moved, rather than
	// by the watcher a tenth of a second later.
	c.reapplyShape()
	return nil
}

// place puts the window's top left corner at x, y.
//
// _NET_MOVERESIZE_WINDOW and not ConfigureWindow: a managed window's geometry
// belongs to the window manager, which redirects a configure request and
// answers it with its own constraints - here it moved the window sideways and
// refused to move it down at all. This message is the one a pager uses to place
// a window, and the manager honours it, as long as the window is not pinned.
func (c *x11Control) place(x, y int) error {
	// The first word carries the gravity (0, the window's own) and the flags:
	// which of x, y, width and height are given (bits 8 and 9), and who is
	// asking (bits 12 and 13, 1 for an ordinary application).
	const flags = 1<<8 | 1<<9 | 1<<12
	// G115: x and y have been through clampMove, which keeps them inside the
	// screen the window is on, and the message carries them as the signed
	// 32-bit numbers EWMH specifies.
	return c.message("_NET_MOVERESIZE_WINDOW", flags, uint32(int32(x)), uint32(int32(y)), 0, 0) //nolint:gosec // G115: see above.
}

// rect answers where the window is, in root coordinates. Its own x and y are
// relative to whatever the manager reparented it into, so they are translated.
func (c *x11Control) rect() (windowRect, error) {
	win, err := c.window()
	if err != nil {
		return windowRect{}, err
	}
	geom, err := xproto.GetGeometry(c.conn, xproto.Drawable(win)).Reply()
	if err != nil {
		return windowRect{}, err
	}
	at, err := xproto.TranslateCoordinates(c.conn, win, c.root, 0, 0).Reply()
	if err != nil {
		return windowRect{}, err
	}
	return windowRect{X: int(at.DstX), Y: int(at.DstY), W: int(geom.Width), H: int(geom.Height)}, nil
}

// unpin clears both maximised states, which is what a quick-tiled window
// carries as well. 0 is the remove action, and the last word says an ordinary
// application is asking.
func (c *x11Control) unpin() error {
	vert, err := c.atom("_NET_WM_STATE_MAXIMIZED_VERT")
	if err != nil {
		return err
	}
	horz, err := c.atom("_NET_WM_STATE_MAXIMIZED_HORZ")
	if err != nil {
		return err
	}
	return c.message("_NET_WM_STATE", 0, uint32(vert), uint32(horz), 1)
}

// settled waits for the window manager to finish giving the window its own
// size back, and answers where it ended up. A manager that answers instantly
// costs one read; one that does not is given a tenth of a second, which is the
// first frame of the drag and not a pause anybody sees.
func (c *x11Control) settled(before windowRect) windowRect {
	deadline := time.Now().Add(100 * time.Millisecond)
	last := before
	for time.Now().Before(deadline) {
		now, err := c.rect()
		if err != nil {
			return last
		}
		if now.W != before.W || now.H != before.H {
			return now
		}
		last = now
		time.Sleep(8 * time.Millisecond)
	}
	return last
}
