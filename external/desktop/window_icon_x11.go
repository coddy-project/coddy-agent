//go:build desktop && linux && !android

package desktop

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image/color"
	"image/png"

	"github.com/jezek/xgb/xproto"
)

// The icon a task bar draws under the window.
//
// Setting the window's class is half of it (WindowClass, browser.go): a task
// bar that finds the matching application entry takes the icon from there, so
// an installed Coddy is already right. A Coddy run from a checkout has no
// entry installed, and then the task bar falls back to what the window itself
// carries - which, for a Chromium window opened with --app, is the page's
// favicon rasterised at 16 pixels, measured on a display of its own. Blown up
// to the 32 or 48 pixels of a task bar it is a smudge, and larger icons are
// only taken from the manifest of an installed web app, which this is not.
//
// So the window is given its icon here, where the window is already being
// driven: _NET_WM_ICON is a property like any other, and the mark it carries
// is the one the Linux packages install for the menu entry.

// setIcon puts Coddy's mark on the window.
//
// Once: the browser writes this property itself whenever the page's favicon
// arrives, which is after the window is mapped and therefore after this, so
// what holds the mark is keepIcon on the watcher's beat rather than a loop
// here - a loop here would also keep the system frame on the window for as
// long as it ran.
func (c *x11Control) setIcon() {
	win, err := c.window()
	if err != nil {
		logger(c.log).Debug("no window to put an icon on", "error", err)
		return
	}
	data, err := c.iconData()
	if err != nil {
		logger(c.log).Debug("the window icon could not be read", "error", err)
		return
	}
	// A property travels in one request, and a request has a maximum length:
	// the first version of this wrote a 256-pixel icon, 262 152 bytes, which
	// the server answered with BadLength - and the connection it answered it
	// on was no use afterwards, so the frame stayed on the window and the
	// chords were never grabbed. The sizes above are 74 KB together; this is
	// what makes sure of it on a server that allows less.
	if max := int(xproto.Setup(c.conn).MaximumRequestLength) * 4; max > 0 && len(data)+32 > max {
		logger(c.log).Debug("the window icon is larger than one request of this server", "bytes", len(data), "max", max)
		return
	}
	prop, err := c.atom("_NET_WM_ICON")
	if err != nil {
		logger(c.log).Debug("this server names no _NET_WM_ICON", "error", err)
		return
	}
	// Format 32: the protocol counts the elements, not the bytes.
	// G115: the length is the icon embedded in this binary.
	if err := xproto.ChangePropertyChecked(c.conn, xproto.PropModeReplace, win, prop,
		xproto.AtomCardinal, 32, uint32(len(data)/4), data).Check(); err != nil { //nolint:gosec // G115: see above.
		logger(c.log).Debug("the window icon could not be set", "error", err)
	}
}

// keepIcon puts the mark back when something else has replaced it.
//
// The browser writes this property itself from the page's favicon, and not
// only at startup: a reload of the page, or a frame of it navigating, is
// enough. The icon then goes from Coddy's mark to a sixteen-pixel rendering
// of it that every task bar blurs up to its own size, which is what an
// operator sees as the icon going soft after Ctrl+R. One property read on the
// watcher's beat is what notices; the write only happens when it has to.
func (c *x11Control) keepIcon() {
	win, err := c.window()
	if err != nil {
		return
	}
	data, err := c.iconData()
	if err != nil {
		return
	}
	prop, err := c.atom("_NET_WM_ICON")
	if err != nil {
		return
	}
	if c.iconIsOurs(win, prop, data) {
		return
	}
	// G115: the length is the icon embedded in this binary.
	_ = xproto.ChangePropertyChecked(c.conn, xproto.PropModeReplace, win, prop,
		xproto.AtomCardinal, 32, uint32(len(data)/4), data).Check() //nolint:gosec // G115: see above.
}

// iconPropertyChanged is the event loop noticing a write to the window's
// icon. Anything else that changed is not this file's business.
func (c *x11Control) iconPropertyChanged(atom xproto.Atom) {
	want, err := c.atom("_NET_WM_ICON")
	if err != nil || atom != want {
		return
	}
	c.keepIcon()
}

// iconData is the property's bytes, built once for the life of the window.
func (c *x11Control) iconData() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.icon != nil {
		return c.icon, nil
	}
	data, err := netWMIcon(iconSmallPNG, iconPNG)
	if err != nil {
		return nil, err
	}
	c.icon = data
	return data, nil
}

// iconIsOurs reads the property back and answers whether it still starts with
// the size this icon was written at.
func (c *x11Control) iconIsOurs(win xproto.Window, prop xproto.Atom, want []byte) bool {
	reply, err := xproto.GetProperty(c.conn, false, win, prop, xproto.AtomCardinal, 0, 2).Reply()
	if err != nil || reply == nil || len(reply.Value) < 8 {
		return false
	}
	return bytes.Equal(reply.Value[:8], want[:8])
}

// netWMIcon is one or more PNGs as _NET_WM_ICON wants them: per icon the
// width, the height, and then one 32-bit ARGB value per pixel, row by row, in
// the machine's own byte order, which is what an X client writes a format-32
// property as. Several icons are simply laid one after another, smallest
// first, and the task bar picks the size it draws at.
func netWMIcon(pngs ...[]byte) ([]byte, error) {
	var out []byte
	for _, b := range pngs {
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			return nil, fmt.Errorf("desktop: the window icon is not a readable PNG: %w", err)
		}
		bounds := img.Bounds()
		w, h := bounds.Dx(), bounds.Dy()
		if w <= 0 || h <= 0 {
			return nil, fmt.Errorf("desktop: the window icon is %dx%d", w, h)
		}
		// G115: the size of an icon embedded in this binary.
		out = binary.NativeEndian.AppendUint32(out, uint32(w)) //nolint:gosec // G115: see above.
		out = binary.NativeEndian.AppendUint32(out, uint32(h)) //nolint:gosec // G115: see above.
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				c, _ := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
				argb := uint32(c.A)<<24 | uint32(c.R)<<16 | uint32(c.G)<<8 | uint32(c.B)
				out = binary.NativeEndian.AppendUint32(out, argb)
			}
		}
	}
	if len(out) == 0 {
		return nil, errors.New("desktop: no window icon to set")
	}
	return out, nil
}
