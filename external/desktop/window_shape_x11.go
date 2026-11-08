//go:build desktop && linux && !android

package desktop

import (
	"math"
	"time"

	"github.com/jezek/xgb/shape"
	"github.com/jezek/xgb/xproto"
)

// A window with no frame is a bare rectangle, and a bare rectangle with square
// corners looks like a bug next to every other window on the desktop. The
// corners are therefore cut out of the window itself with the X Shape
// extension - the same way a browser rounds its own frame - and only while the
// window floats: a maximised window fills the screen, where a rounded corner
// would be a hole onto the wallpaper.
//
// The shape is in window coordinates, so it has to be laid again after every
// resize. That is what watchShape is for; it shares the control's connection,
// which xgb serialises, and ends with the run.

// cornerRadius is the radius of the window's corners, in pixels. It matches
// the radius the page rounds its own top corners by.
const cornerRadius = 10

// roundCorners cuts the corners out of the window, or takes the shape off
// again when the window should have none.
//
// A window with the system frame back on is the manager's to round, and it
// draws its own corners over ours; a maximised one fills the screen, where a
// cut corner is a hole onto the wallpaper. Neither takes a whole-rectangle
// shape, which is what this used to set: a shape set is a shape the browser
// clears again on the next change of the window's bounds, so the watcher laid
// it again, and the manager redrew the frame around it every time - a window
// that blinked for as long as the frame was on. A window with no corners to
// cut is left with no shape at all.
func (c *x11Control) roundCorners() {
	win, err := c.window()
	if err != nil {
		return
	}
	if c.maximised() || c.framed() {
		c.clearShape(win)
		return
	}
	geom, err := xproto.GetGeometry(c.conn, xproto.Drawable(win)).Reply()
	if err != nil {
		return
	}
	c.applyShape(win, roundedRectangles(geom.Width, geom.Height, cornerRadius))
}

// clearShape takes any shape off the window, which is what a framed or
// maximised one wants.
func (c *x11Control) clearShape(win xproto.Window) {
	if !c.shapeReady() {
		return
	}
	if err := shape.MaskChecked(c.conn, shape.SoSet, shape.SkBounding, win, 0, 0, xproto.PixmapNone).Check(); err != nil {
		logger(c.log).Debug("the window's shape could not be taken off", "error", err)
	}
	c.mu.Lock()
	// A window with no corners to keep has nothing for a drag to lay again.
	c.shapeRects = nil
	c.mu.Unlock()
}

func (c *x11Control) applyShape(win xproto.Window, rects []xproto.Rectangle) {
	if !c.shapeReady() {
		return
	}
	err := shape.RectanglesChecked(c.conn, shape.SoSet, shape.SkBounding, xproto.ClipOrderingUnsorted,
		win, 0, 0, rects).Check()
	if err != nil {
		logger(c.log).Debug("the window's corners could not be cut", "error", err)
		return
	}
	// Kept for the drag below, which lays the same shape again without asking
	// the server anything about the window first.
	c.mu.Lock()
	c.shapeRects = rects
	c.mu.Unlock()
}

// reapplyShape lays the shape the window already had, again.
//
// It is for a drag: the browser sets the region of its own window whenever the
// window's bounds change, which clears ours, and a window dragged across the
// screen changes its bounds sixty times a second. The watcher below would put
// the corners back a tenth of a second later each time, which is exactly long
// enough to see - the corners went square for the whole drag and round again
// when it stopped. The size does not change while a window is moved, so the
// rectangles are the ones already computed, and the request is sent without
// waiting for an answer: a round trip per frame is what a drag cannot afford.
func (c *x11Control) reapplyShape() {
	c.mu.Lock()
	rects := c.shapeRects
	c.mu.Unlock()
	if len(rects) == 0 || !c.shapeReady() {
		return
	}
	win, err := c.window()
	if err != nil {
		return
	}
	shape.Rectangles(c.conn, shape.SoSet, shape.SkBounding, xproto.ClipOrderingUnsorted, win, 0, 0, rects)
}

// shapeReady loads the extension once per connection. A server without it
// leaves the corners square, which is a look and never a failure.
func (c *x11Control) shapeReady() bool {
	c.shapeOnce.Do(func() {
		if err := shape.Init(c.conn); err != nil {
			return
		}
		if _, err := shape.QueryVersion(c.conn).Reply(); err != nil {
			return
		}
		c.shaped = true
	})
	return c.shaped
}

// maximised reports whether the window manager has the window filling the
// screen on either axis, fullscreen included: a corner cut out of such a window
// is a hole onto the wallpaper.
func (c *x11Control) maximised() bool {
	vert, horz := c.maximisedAxes()
	return vert || horz || c.hasState("_NET_WM_STATE_FULLSCREEN")
}

// roundedRectangles describes a rounded rectangle as the scanlines it is made
// of: one rectangle per row inside a corner, and one for everything between
// them. A shape is a region, and a region of rectangles is what the extension
// takes; a radius of ten costs twenty of them.
func roundedRectangles(w, h uint16, radius int) []xproto.Rectangle {
	if radius <= 0 || int(w) < radius*2 || int(h) < radius*2 {
		return []xproto.Rectangle{{X: 0, Y: 0, Width: w, Height: h}}
	}
	r := float64(radius)
	out := make([]xproto.Rectangle, 0, radius*2+1)
	// Every number below is a coordinate inside a window that is at least
	// twice the radius wide and high, and the radius is cornerRadius: nothing
	// here can leave the 16 bits an X rectangle carries, which is what the
	// G115 exemptions say.
	for y := 0; y < radius; y++ {
		// How far the circle has come in by this row: the horizontal half-chord
		// of the corner's arc.
		dy := r - float64(y) - 0.5
		dx := r - math.Sqrt(r*r-dy*dy)
		inset := int(dx + 0.5)
		out = append(out, xproto.Rectangle{
			X: int16(inset), Y: int16(y), //nolint:gosec // G115: inset <= radius and y < radius, both small.
			Width: w - uint16(inset*2), Height: 1, //nolint:gosec // G115: inset*2 <= w, checked above.
		})
	}
	out = append(out, xproto.Rectangle{
		X: 0, Y: int16(radius), //nolint:gosec // G115: the radius is cornerRadius, ten pixels.
		Width: w, Height: h - uint16(radius*2), //nolint:gosec // G115: radius*2 <= h, checked above.
	})
	for y := 0; y < radius; y++ {
		dy := r - float64(y) - 0.5
		dx := r - math.Sqrt(r*r-dy*dy)
		inset := int(dx + 0.5)
		out = append(out, xproto.Rectangle{
			X: int16(inset), Y: int16(int(h) - 1 - y), //nolint:gosec // G115: a row inside a window, which is uint16 itself.
			Width: w - uint16(inset*2), Height: 1, //nolint:gosec // G115: inset*2 <= w, checked above.
		})
	}
	return out
}

// boundingShaped reports whether the window has any shape on it at all.
//
// It is asked every tick because the shape is not ours alone: the browser sets
// the region of its own window whenever the window's bounds change, and where
// it wants no region it clears the shape outright - which takes our corners
// with it. That is why a window could come up rounded and go square again a
// moment later, with nothing in this process having asked for it.
func (c *x11Control) boundingShaped() bool {
	win, err := c.window()
	if err != nil || !c.shapeReady() {
		return false
	}
	ext, err := shape.QueryExtents(c.conn, win).Reply()
	if err != nil {
		return false
	}
	return ext.BoundingShaped
}

// watchShape keeps the corners cut as the window is moved, resized, maximised
// and restored, and lays them again when the browser has cleared them. It
// samples rather than subscribes: the window belongs to the browser, so
// selecting for its events would fight whatever the browser has selected, and
// a shape laid a tenth of a second late is a shape nobody sees arrive.
func (c *x11Control) watchShape(stop <-chan struct{}) {
	shaped := c.shapeReady()
	last := struct {
		w, h   uint16
		max    bool
		framed bool
	}{}
	tick := time.NewTicker(120 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-stop:
			return
		case <-tick.C:
		}
		// The browser writes its own icon into the window whenever the page
		// reloads or a frame of it navigates, and its own icon is the page's
		// favicon at sixteen pixels - which a task bar then blurs up to its
		// own size. One small property read a tick puts Coddy's mark back.
		c.keepIcon()
		if !shaped {
			continue
		}
		win, err := c.window()
		if err != nil {
			continue
		}
		geom, err := xproto.GetGeometry(c.conn, xproto.Drawable(win)).Reply()
		if err != nil {
			continue
		}
		max, framed := c.maximised(), c.framed()
		settled := geom.Width == last.w && geom.Height == last.h &&
			max == last.max && framed == last.framed
		if settled {
			// A window that wants corners is checked for having them, because
			// the browser clears the shape whenever the window's bounds move.
			// One that wants none is left alone entirely: it has a frame, and
			// the frame is the manager's, which redraws it for every shape
			// this process writes.
			if max || framed || c.boundingShaped() {
				continue
			}
		}
		last.w, last.h, last.max, last.framed = geom.Width, geom.Height, max, framed
		c.roundCorners()
	}
}
