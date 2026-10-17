//go:build cli

package clipimage

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"

	"github.com/EvilFreelancer/coddy-agent/external/cli/xutil"
	"github.com/EvilFreelancer/coddy-agent/external/cli/xutil/xvfbtest"
)

// clipboardOwner is an X client that owns CLIPBOARD and answers for the
// offers it was given, in pieces of chunk bytes (the INCR protocol) when an
// answer is longer than that.
type clipboardOwner struct {
	c      *xutil.Conn
	win    xproto.Window
	atoms  map[string]xproto.Atom
	offers map[xproto.Atom][]byte
	chunk  int
	// the INCR transfer in progress
	to   xproto.Window
	prop xproto.Atom
	typ  xproto.Atom
	rest []byte
	busy bool
}

func ownClipboard(t *testing.T, display string, offers map[string][]byte, chunk int) {
	t.Helper()
	c, err := xutil.Open(display)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	atoms, err := c.Atoms("CLIPBOARD", "TARGETS", "INCR")
	if err != nil {
		t.Fatal(err)
	}
	o := &clipboardOwner{c: c, atoms: atoms, offers: map[xproto.Atom][]byte{}, chunk: chunk}
	for name, data := range offers {
		a, err := c.Atom(name)
		if err != nil {
			t.Fatal(err)
		}
		o.offers[a] = data
	}
	o.win, _ = xproto.NewWindowId(c.Conn)
	if err := xproto.CreateWindowChecked(c.Conn, 0, o.win, c.Screen.Root, 0, 0, 1, 1, 0,
		xproto.WindowClassInputOnly, 0, 0, nil).Check(); err != nil {
		t.Fatal(err)
	}
	if err := xproto.SetSelectionOwnerChecked(c.Conn, o.win, atoms["CLIPBOARD"], xproto.TimeCurrentTime).Check(); err != nil {
		t.Fatal(err)
	}
	go o.serve()
}

func (o *clipboardOwner) serve() {
	for {
		ev, err := o.c.WaitForEvent()
		if ev == nil && err == nil {
			return
		}
		switch e := ev.(type) {
		case xproto.SelectionRequestEvent:
			o.answer(e)
		case xproto.PropertyNotifyEvent:
			if o.busy && e.Window == o.to && e.Atom == o.prop && e.State == xproto.PropertyDelete {
				n := min(o.chunk, len(o.rest))
				xproto.ChangeProperty(o.c.Conn, xproto.PropModeReplace, o.to, o.prop, o.typ, 8, uint32(n), o.rest[:n])
				o.rest = o.rest[n:]
				if n == 0 {
					o.busy = false
				}
			}
		}
	}
}

func (o *clipboardOwner) answer(e xproto.SelectionRequestEvent) {
	prop := e.Property
	switch data, ok := o.offers[e.Target]; {
	case e.Target == o.atoms["TARGETS"]:
		list := []xproto.Atom{o.atoms["TARGETS"]}
		for a := range o.offers {
			list = append(list, a)
		}
		o.c.SetAtoms(e.Requestor, prop, list...)
	case ok && len(data) > o.chunk:
		xproto.ChangeWindowAttributes(o.c.Conn, e.Requestor, xproto.CwEventMask, []uint32{xproto.EventMaskPropertyChange})
		size := make([]byte, 4)
		xgb.Put32(size, uint32(len(data)))
		xproto.ChangeProperty(o.c.Conn, xproto.PropModeReplace, e.Requestor, prop, o.atoms["INCR"], 32, 1, size)
		o.to, o.prop, o.typ, o.rest, o.busy = e.Requestor, prop, e.Target, data, true
	case ok:
		xproto.ChangeProperty(o.c.Conn, xproto.PropModeReplace, e.Requestor, prop, e.Target, 8, uint32(len(data)), data)
	default:
		prop = xproto.AtomNone
	}
	n := xproto.SelectionNotifyEvent{Time: e.Time, Requestor: e.Requestor, Selection: e.Selection, Target: e.Target, Property: prop}
	xproto.SendEvent(o.c.Conn, false, e.Requestor, 0, string(n.Bytes()))
}

func TestReadX11TakesThePictureTheOwnerOffers(t *testing.T) {
	display := xvfbtest.Start(t)
	data := pngBytes(t, 6, 4)
	ownClipboard(t, display, map[string][]byte{"text/plain": []byte("hello"), "image/png": data}, 1<<20)
	img, err := ReadX11(context.Background(), display)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(img.Data, data) || img.MIME != "image/png" {
		t.Fatalf("got %s of %d bytes, want the offered PNG", img.MIME, len(img.Data))
	}
}

func TestReadX11TakesALargePictureInPieces(t *testing.T) {
	display := xvfbtest.Start(t)
	data := pngBytes(t, 300, 300) // a few hundred KB of noise
	ownClipboard(t, display, map[string][]byte{"image/png": data}, 16<<10)
	img, err := ReadX11(context.Background(), display)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(img.Data, data) {
		t.Fatalf("INCR transfer gave %d bytes, want %d", len(img.Data), len(data))
	}
}

func TestReadX11WithoutAPicture(t *testing.T) {
	display := xvfbtest.Start(t)
	if _, err := ReadX11(context.Background(), display); !errors.Is(err, ErrNoImage) {
		t.Fatalf("no owner: %v", err)
	}
	ownClipboard(t, display, map[string][]byte{"UTF8_STRING": []byte("just text")}, 1<<20)
	if _, err := ReadX11(context.Background(), display); !errors.Is(err, ErrNoImage) {
		t.Fatalf("text only: %v", err)
	}
}
