//go:build cli

// Package xutil is what the console's X11 clients share: a connection to
// the display the environment names, quiet (the protocol library would
// otherwise log to stderr, which is the console's screen), atoms by name,
// and the property reads both the clipboard and the markup window need.
//
// Everything speaks the X11 protocol in Go (github.com/jezek/xgb): no cgo
// and no libX11, so the console binary stays static and still opens a
// window wherever an X server - Xorg, XWayland, XQuartz, a forwarded
// display over ssh -X - is reachable.
package xutil

import (
	"errors"
	"io"
	"log"
	"strconv"
	"strings"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// ErrNoDisplay says the environment names no X display.
var ErrNoDisplay = errors.New("no X display (DISPLAY is not set)")

var quietOnce sync.Once

// quiet sends the protocol library's log to nowhere: it prints connection
// trouble to stderr, and stderr is the console's screen.
func quiet() {
	quietOnce.Do(func() { xgb.Logger = log.New(io.Discard, "", 0) })
}

// DisplayName is the X display the environment names, or "" when there is
// none. A Wayland session that runs XWayland sets DISPLAY too.
func DisplayName(getenv func(string) string) string {
	return strings.TrimSpace(getenv("DISPLAY"))
}

// Conn is an open connection with its default screen.
type Conn struct {
	*xgb.Conn
	Setup  *xproto.SetupInfo
	Screen *xproto.ScreenInfo
}

// Open connects to the display. An empty name is ErrNoDisplay.
func Open(display string) (*Conn, error) {
	if display == "" {
		return nil, ErrNoDisplay
	}
	quiet()
	c, err := xgb.NewConnDisplay(display)
	if err != nil {
		return nil, err
	}
	setup := xproto.Setup(c)
	if setup == nil || len(setup.Roots) == 0 {
		c.Close()
		return nil, errors.New("the X server reported no screen")
	}
	return &Conn{Conn: c, Setup: setup, Screen: setup.DefaultScreen(c)}, nil
}

// Atom interns name.
func (c *Conn) Atom(name string) (xproto.Atom, error) {
	r, err := xproto.InternAtom(c.Conn, false, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, err
	}
	return r.Atom, nil
}

// Atoms interns every name at once and fails on the first that does not
// come back.
func (c *Conn) Atoms(names ...string) (map[string]xproto.Atom, error) {
	cookies := make([]xproto.InternAtomCookie, len(names))
	for i, n := range names {
		cookies[i] = xproto.InternAtom(c.Conn, false, uint16(len(n)), n)
	}
	out := make(map[string]xproto.Atom, len(names))
	for i, ck := range cookies {
		r, err := ck.Reply()
		if err != nil {
			return nil, err
		}
		out[names[i]] = r.Atom
	}
	return out, nil
}

// AtomName is the name of an atom, or "" when the server does not know it.
func (c *Conn) AtomName(a xproto.Atom) string {
	r, err := xproto.GetAtomName(c.Conn, a).Reply()
	if err != nil {
		return ""
	}
	return r.Name
}

// ReadProperty reads a whole property of w, however long, and reports its
// type and format. A missing property is a nil value with type 0.
func (c *Conn) ReadProperty(w xproto.Window, prop xproto.Atom, del bool) (value []byte, typ xproto.Atom, format byte, err error) {
	var offset uint32
	for {
		// The length is in 32-bit units; a megabyte per round trip.
		r, err := xproto.GetProperty(c.Conn, false, w, prop, xproto.GetPropertyTypeAny, offset, 1<<18).Reply()
		if err != nil {
			return nil, 0, 0, err
		}
		if r.Type == xproto.AtomNone {
			return nil, 0, 0, nil
		}
		value = append(value, r.Value...)
		typ, format = r.Type, r.Format
		if r.BytesAfter == 0 {
			break
		}
		offset += uint32(len(r.Value)) / 4
	}
	if del {
		xproto.DeleteProperty(c.Conn, w, prop)
	}
	return value, typ, format, nil
}

// SetString sets a string property of w.
func (c *Conn) SetString(w xproto.Window, prop, typ xproto.Atom, s string) {
	xproto.ChangeProperty(c.Conn, xproto.PropModeReplace, w, prop, typ, 8, uint32(len(s)), []byte(s))
}

// SetAtoms sets a property of w to a list of atoms.
func (c *Conn) SetAtoms(w xproto.Window, prop xproto.Atom, atoms ...xproto.Atom) {
	buf := make([]byte, 4*len(atoms))
	for i, a := range atoms {
		xgb.Put32(buf[4*i:], uint32(a))
	}
	xproto.ChangeProperty(c.Conn, xproto.PropModeReplace, w, prop, xproto.AtomAtom, 32, uint32(len(atoms)), buf)
}

// DPIScale is the display scale the desktop asks for through the Xft.dpi
// resource (96 dpi is 1), in quarter steps between 1 and 4; 1 when the
// resource is not set.
func (c *Conn) DPIScale() float64 {
	v, _, _, err := c.ReadProperty(c.Screen.Root, xproto.AtomResourceManager, false)
	if err != nil {
		return 1
	}
	return ScaleFromResources(string(v))
}

// ScaleFromResources reads Xft.dpi out of the RESOURCE_MANAGER text.
func ScaleFromResources(res string) float64 {
	for _, line := range strings.Split(res, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(k) != "Xft.dpi" {
			continue
		}
		dpi, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil || dpi <= 0 {
			return 1
		}
		s := dpi / 96
		s = float64(int(s*4+0.5)) / 4
		if s < 1 {
			return 1
		}
		if s > 4 {
			return 4
		}
		return s
	}
	return 1
}
