//go:build cli

package clipimage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"

	"github.com/EvilFreelancer/coddy-agent/external/cli/xutil"
)

// stepTimeout bounds every exchange with the clipboard's owner: an owner
// that hangs must not hang the console's paste.
const stepTimeout = 3 * time.Second

// ReadX11 reads the CLIPBOARD selection of an X display the way any X
// client pastes: it asks the owner for its TARGETS, then for the best
// picture among them (or for the file list of a copied file), and takes an
// answer of any size, the INCR transfer included.
func ReadX11(ctx context.Context, display string) (Image, error) {
	c, err := xutil.Open(display)
	if err != nil {
		return Image{}, err
	}
	defer c.Close()
	atoms, err := c.Atoms("CLIPBOARD", "TARGETS", "INCR", "CODDY_CLIPBOARD")
	if err != nil {
		return Image{}, err
	}
	win, err := xproto.NewWindowId(c.Conn)
	if err != nil {
		return Image{}, err
	}
	if err := xproto.CreateWindowChecked(c.Conn, 0, win, c.Screen.Root, 0, 0, 1, 1, 0,
		xproto.WindowClassInputOnly, 0, xproto.CwEventMask, []uint32{xproto.EventMaskPropertyChange}).Check(); err != nil {
		return Image{}, err
	}
	defer xproto.DestroyWindow(c.Conn, win)

	done := make(chan struct{})
	defer close(done)
	r := &selReader{c: c, win: win, prop: atoms["CODDY_CLIPBOARD"], clipboard: atoms["CLIPBOARD"], incr: atoms["INCR"], events: events(c, done)}
	owner, err := xproto.GetSelectionOwner(c.Conn, r.clipboard).Reply()
	if err != nil {
		return Image{}, err
	}
	if owner.Owner == xproto.WindowNone {
		return Image{}, ErrNoImage
	}
	raw, err := r.convert(ctx, atoms["TARGETS"])
	if err != nil {
		return Image{}, err
	}
	offered := make([]string, 0, len(raw)/4)
	byName := map[string]xproto.Atom{}
	for i := 0; i+4 <= len(raw); i += 4 {
		a := xproto.Atom(xgb.Get32(raw[i:]))
		name := strings.ToLower(c.AtomName(a))
		offered = append(offered, name)
		byName[name] = a
	}
	t := pickType(offered)
	if t == "" {
		return Image{}, ErrNoImage
	}
	data, err := r.convert(ctx, byName[t])
	if err != nil {
		return Image{}, err
	}
	if t == "text/uri-list" {
		return fromURIList(string(data))
	}
	return fromBytes(data, "")
}

type xevent struct {
	ev  xgb.Event
	err xgb.Error
}

// events pumps the connection's events into a channel until done closes or
// the connection does.
func events(c *xutil.Conn, done <-chan struct{}) <-chan xevent {
	ch := make(chan xevent, 64)
	go func() {
		defer close(ch)
		for {
			ev, err := c.WaitForEvent()
			if ev == nil && err == nil {
				return
			}
			select {
			case ch <- xevent{ev, err}:
			case <-done:
				return
			}
		}
	}()
	return ch
}

type selReader struct {
	c         *xutil.Conn
	win       xproto.Window
	prop      xproto.Atom
	clipboard xproto.Atom
	incr      xproto.Atom
	events    <-chan xevent
}

// convert asks the owner for the selection as target and reads the answer.
func (r *selReader) convert(ctx context.Context, target xproto.Atom) ([]byte, error) {
	xproto.ConvertSelection(r.c.Conn, r.win, r.clipboard, target, r.prop, xproto.TimeCurrentTime)
	ev, err := r.wait(ctx, func(ev xgb.Event) bool {
		n, ok := ev.(xproto.SelectionNotifyEvent)
		return ok && n.Requestor == r.win && n.Selection == r.clipboard
	})
	if err != nil {
		return nil, err
	}
	if ev.(xproto.SelectionNotifyEvent).Property == xproto.AtomNone {
		return nil, ErrNoImage
	}
	value, typ, _, err := r.c.ReadProperty(r.win, r.prop, true, MaxBytes)
	if err != nil {
		return nil, err
	}
	if typ != r.incr {
		return value, nil
	}
	// INCR: the owner writes the answer in pieces, each after we delete the
	// one before (the read above deleted the INCR marker); an empty piece
	// ends it.
	var out []byte
	for {
		if _, err := r.wait(ctx, func(ev xgb.Event) bool {
			p, ok := ev.(xproto.PropertyNotifyEvent)
			return ok && p.Window == r.win && p.Atom == r.prop && p.State == xproto.PropertyNewValue
		}); err != nil {
			return nil, err
		}
		piece, _, _, err := r.c.ReadProperty(r.win, r.prop, true, MaxBytes)
		if err != nil {
			return nil, err
		}
		if len(piece) == 0 {
			return out, nil
		}
		out = append(out, piece...)
		if len(out) > MaxBytes {
			return nil, fmt.Errorf("the clipboard image is larger than %d MB", MaxBytes>>20)
		}
	}
}

func (r *selReader) wait(ctx context.Context, match func(xgb.Event) bool) (xgb.Event, error) {
	timer := time.NewTimer(stepTimeout)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return nil, errors.New("the clipboard owner did not answer")
		case e, ok := <-r.events:
			if !ok {
				return nil, errors.New("the X connection closed")
			}
			if e.ev != nil && match(e.ev) {
				return e.ev, nil
			}
		}
	}
}
