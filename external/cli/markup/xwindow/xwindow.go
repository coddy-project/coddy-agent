//go:build cli

// Package xwindow shows the markup editor (external/cli/markup) in a window
// on an X display and returns the picture when the person clicks Done.
package xwindow

import (
	"context"
	"errors"
	"fmt"
	"image"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"

	"github.com/EvilFreelancer/coddy-agent/external/cli/markup"
	"github.com/EvilFreelancer/coddy-agent/external/cli/xutil"
)

// Title is the window's title.
const Title = "Coddy - mark up the picture"

// ErrUnsupportedVisual says the display's default visual is not a 24-bit
// true-colour one, which is the only kind the window paints on.
var ErrUnsupportedVisual = errors.New("the X display has no 24-bit true-colour visual")

// mapped, when set by a test, hears where the window is and how it is laid
// out once it is on screen.
var mapped func(win xproto.Window, l markup.Layout)

// Edit shows img in the markup window on the display and waits. It returns
// the picture as PNG when the person clicks Done (or presses enter), and
// ok false when they click Cancel, press escape, close the window or ctx
// ends. An error means no window could be shown.
func Edit(ctx context.Context, display string, img image.Image) (png []byte, ok bool, err error) {
	c, err := xutil.Open(display)
	if err != nil {
		return nil, false, err
	}
	defer c.Close()
	w, err := newWindow(c, img)
	if err != nil {
		return nil, false, err
	}
	defer w.destroy()
	return w.run(ctx)
}

type window struct {
	c      *xutil.Conn
	id     xproto.Window
	gc     xproto.Gcontext
	depth  byte
	lsb    bool
	maxReq int
	atoms  map[string]xproto.Atom
	keys   *keymap
	ed     *markup.Editor
	buf    []byte
}

func newWindow(c *xutil.Conn, img image.Image) (*window, error) {
	s := c.Screen
	if !trueColor24(c) {
		return nil, ErrUnsupportedVisual
	}
	useXKB(c)
	keys, err := loadKeymap(c)
	if err != nil {
		return nil, fmt.Errorf("read the keyboard mapping: %w", err)
	}
	atoms, err := c.Atoms("WM_PROTOCOLS", "WM_DELETE_WINDOW", "_NET_WM_NAME", "UTF8_STRING",
		"_NET_WM_WINDOW_TYPE", "_NET_WM_WINDOW_TYPE_DIALOG")
	if err != nil {
		return nil, err
	}
	scale := c.DPIScale()
	ed := markup.NewEditor(img, scale)
	screen := image.Pt(int(s.WidthInPixels), int(s.HeightInPixels))
	size := markup.PreferredSize(ed.Document().Size(), screen, scale)
	ed.Resize(size)

	id, err := xproto.NewWindowId(c.Conn)
	if err != nil {
		return nil, err
	}
	mask := uint32(xproto.EventMaskExposure | xproto.EventMaskKeyPress | xproto.EventMaskButtonPress |
		xproto.EventMaskButtonRelease | xproto.EventMaskPointerMotion | xproto.EventMaskStructureNotify)
	x, y := (screen.X-size.X)/2, (screen.Y-size.Y)/2
	if err := xproto.CreateWindowChecked(c.Conn, s.RootDepth, id, s.Root, int16(max(x, 0)), int16(max(y, 0)),
		uint16(size.X), uint16(size.Y), 0, xproto.WindowClassInputOutput, s.RootVisual,
		xproto.CwBackPixel|xproto.CwEventMask, []uint32{0x1f1f22, mask}).Check(); err != nil {
		return nil, fmt.Errorf("create the window: %w", err)
	}
	w := &window{c: c, id: id, depth: s.RootDepth, lsb: c.Setup.ImageByteOrder == xproto.ImageOrderLSBFirst,
		maxReq: int(c.Setup.MaximumRequestLength) * 4, atoms: atoms, keys: keys, ed: ed}

	c.SetString(id, xproto.AtomWmName, xproto.AtomString, Title)
	c.SetString(id, atoms["_NET_WM_NAME"], atoms["UTF8_STRING"], Title)
	c.SetString(id, xproto.AtomWmClass, xproto.AtomString, "coddy-markup\x00Coddy\x00")
	c.SetAtoms(id, atoms["WM_PROTOCOLS"], atoms["WM_DELETE_WINDOW"])
	c.SetAtoms(id, atoms["_NET_WM_WINDOW_TYPE"], atoms["_NET_WM_WINDOW_TYPE_DIALOG"])
	w.setSizeHints(image.Pt(x, y), size, markup.MinSize(scale))

	gc, err := xproto.NewGcontextId(c.Conn)
	if err != nil {
		return nil, err
	}
	xproto.CreateGC(c.Conn, gc, xproto.Drawable(id), 0, nil)
	w.gc = gc
	xproto.MapWindow(c.Conn, id)
	return w, nil
}

// trueColor24 reports whether the root visual is 24-bit true colour with
// the usual masks, stored in 32 bits a pixel.
func trueColor24(c *xutil.Conn) bool {
	s := c.Screen
	if s.RootDepth != 24 && s.RootDepth != 32 {
		return false
	}
	bpp := 0
	for _, f := range c.Setup.PixmapFormats {
		if f.Depth == s.RootDepth {
			bpp = int(f.BitsPerPixel)
		}
	}
	if bpp != 32 {
		return false
	}
	for _, d := range s.AllowedDepths {
		for _, v := range d.Visuals {
			if v.VisualId == s.RootVisual {
				return v.Class == xproto.VisualClassTrueColor && v.RedMask == 0xff0000 && v.GreenMask == 0xff00 && v.BlueMask == 0xff
			}
		}
	}
	return false
}

// setSizeHints asks the window manager for the position and size the window
// was created with and for the toolbar's minimum (ICCCM WM_NORMAL_HINTS).
func (w *window) setSizeHints(at, size, minSize image.Point) {
	const (
		pPosition = 1 << 2
		pSize     = 1 << 3
		pMinSize  = 1 << 4
	)
	hints := make([]byte, 18*4)
	put := func(i int, v int) { xgb.Put32(hints[i*4:], uint32(int32(v))) }
	put(0, pPosition|pSize|pMinSize)
	put(1, at.X)
	put(2, at.Y)
	put(3, size.X)
	put(4, size.Y)
	put(5, minSize.X)
	put(6, minSize.Y)
	xproto.ChangeProperty(w.c.Conn, xproto.PropModeReplace, w.id, xproto.AtomWmNormalHints, xproto.AtomWmSizeHints, 32, 18, hints)
}

func (w *window) destroy() {
	xproto.DestroyWindow(w.c.Conn, w.id)
	xproto.FreeGC(w.c.Conn, w.gc)
	// A round trip makes sure the window is gone before the connection
	// closes under it.
	_, _ = xproto.GetInputFocus(w.c.Conn).Reply()
}

type xevent struct {
	ev  xgb.Event
	err xgb.Error
}

func (w *window) run(ctx context.Context) ([]byte, bool, error) {
	events := make(chan xevent, 256)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		defer close(events)
		for {
			ev, err := w.c.WaitForEvent()
			if ev == nil && err == nil {
				return
			}
			select {
			case events <- xevent{ev, err}:
			case <-stop:
				return
			}
		}
	}()
	dirty := false
	for {
		select {
		case <-ctx.Done():
			return nil, false, ctx.Err()
		case e, open := <-events:
			if !open {
				return nil, false, errors.New("the X connection closed")
			}
			if e.ev == nil {
				// A protocol error from a request nobody waits for (a focus
				// request the window manager beat us to) changes nothing.
				continue
			}
			res, redraw := w.handle(e.ev)
			dirty = dirty || redraw
			switch res {
			case markup.Done:
				png, err := w.ed.PNG()
				if err != nil {
					return nil, false, err
				}
				return png, true, nil
			case markup.Cancel:
				return nil, false, nil
			}
			// Paint once the queue is drained, so a burst of motion is one
			// frame.
			if dirty && len(events) == 0 {
				w.paint()
				dirty = false
			}
		}
	}
}

// handle applies one event and says whether the window should close and
// whether the frame changed.
func (w *window) handle(ev xgb.Event) (markup.Result, bool) {
	switch e := ev.(type) {
	case xproto.ExposeEvent:
		return markup.Continue, e.Count == 0
	case xproto.MapNotifyEvent:
		xproto.SetInputFocus(w.c.Conn, xproto.InputFocusParent, w.id, xproto.TimeCurrentTime)
		if mapped != nil {
			mapped(w.id, w.ed.Layout())
		}
		return markup.Continue, true
	case xproto.ConfigureNotifyEvent:
		sz := image.Pt(int(e.Width), int(e.Height))
		if sz != w.ed.Layout().Size && sz.X > 0 && sz.Y > 0 {
			w.ed.Resize(sz)
			return markup.Continue, true
		}
	case xproto.ButtonPressEvent:
		if e.Detail == xproto.ButtonIndex1 {
			return w.ed.PointerDown(image.Pt(int(e.EventX), int(e.EventY))), true
		}
	case xproto.ButtonReleaseEvent:
		if e.Detail == xproto.ButtonIndex1 {
			return w.ed.PointerUp(image.Pt(int(e.EventX), int(e.EventY))), true
		}
	case xproto.MotionNotifyEvent:
		return markup.Continue, w.ed.PointerMove(image.Pt(int(e.EventX), int(e.EventY)))
	case xproto.KeyPressEvent:
		if k, ok := w.keys.key(e.Detail, e.State); ok {
			return w.ed.KeyPress(k), true
		}
	case xproto.MappingNotifyEvent:
		if keys, err := loadKeymap(w.c); err == nil {
			w.keys = keys
		}
	case xproto.ClientMessageEvent:
		if e.Type == w.atoms["WM_PROTOCOLS"] && len(e.Data.Data32) > 0 && xproto.Atom(e.Data.Data32[0]) == w.atoms["WM_DELETE_WINDOW"] {
			return markup.Cancel, false
		}
	}
	return markup.Continue, false
}

// paint sends the frame to the window in horizontal strips that each fit
// one request.
func (w *window) paint() {
	f := w.ed.Frame()
	sz := f.Bounds().Size()
	stride := sz.X * 4
	if cap(w.buf) < stride*sz.Y {
		w.buf = make([]byte, stride*sz.Y)
	}
	buf := w.buf[:stride*sz.Y]
	for i := 0; i < len(buf); i += 4 {
		r, g, b := f.Pix[i], f.Pix[i+1], f.Pix[i+2]
		if w.lsb {
			buf[i], buf[i+1], buf[i+2], buf[i+3] = b, g, r, 0
		} else {
			buf[i], buf[i+1], buf[i+2], buf[i+3] = 0, r, g, b
		}
	}
	rows := max(1, (w.maxReq-64)/stride)
	for y := 0; y < sz.Y; y += rows {
		n := min(rows, sz.Y-y)
		xproto.PutImage(w.c.Conn, xproto.ImageFormatZPixmap, xproto.Drawable(w.id), w.gc,
			uint16(sz.X), uint16(n), 0, int16(y), 0, w.depth, buf[y*stride:(y+n)*stride])
	}
}
