//go:build desktop && linux && !android

package desktop

import (
	"context"

	"github.com/jezek/xgb/xproto"
)

// Taking a chord away from the browser is a passive key grab on its window.
//
// A grab is how a window manager owns Meta+Tab while an application is
// focused: the X server delivers the matching key press to the grabbing client
// instead of to the window's own one, so the browser never sees the key and
// never gets to act on it. The grab is laid on the browser's window rather
// than on the root, so Ctrl+T belongs to Coddy only while Coddy's window has
// the focus and to every other program the rest of the time.
//
// What it cannot do is reach a key that is not on the layout: the grab is by
// keycode, and the keycode is found by asking the server which key produces
// 't'. A keyboard map with no Latin letters at all - a Russian-only layout
// with no second group - answers nothing, and the two keycodes of the Linux
// evdev map are used instead, which is where 't' and 'w' sit on every
// ordinary keyboard.

// evdevChordKeycodes are the keycodes of T and W on the Linux evdev keyboard
// map (KEY_T and KEY_W, offset by the eight X reserves), used only when the
// layout names no Latin letter at all.
var evdevChordKeycodes = map[uint32]xproto.Keycode{keysymT: 28, keysymW: 25}

// NextChord answers the next chord taken from the window.
func (c *x11Control) NextChord(ctx context.Context) string {
	if c == nil || c.chords == nil {
		<-ctx.Done()
		return ""
	}
	return c.chords.next(ctx)
}

// pushChord puts a chord into the same queue a grabbed key goes down, for a
// row of the tray's menu to ask the strip for what that key does.
func (c *x11Control) pushChord(name string) {
	if c == nil || c.chords == nil {
		return
	}
	c.chords.push(name)
}

// grabChords takes the chords the browser answers itself and starts the loop
// that turns them into what the strip is told.
//
// A chord it could not take is a debug line and nothing more: the window still
// works, the browser answers that key the way it always did, and `Alt` stands
// beside every chord for exactly this case.
func (c *x11Control) grabChords() {
	win, err := c.window()
	if err != nil {
		logger(c.log).Debug("no window to take the chords of", "error", err)
		return
	}
	codes := c.chordKeycodes()
	if len(codes) == 0 {
		logger(c.log).Debug("this keyboard map names no key the chords could be taken by")
		return
	}
	for key, chord := range codes {
		base := uint16(xproto.ModMaskControl)
		if key.shift {
			base |= uint16(xproto.ModMaskShift)
		}
		for _, mask := range grabMasks(base, c.lockMasks()...) {
			err := xproto.GrabKeyChecked(c.conn, false, win, mask, key.code,
				xproto.GrabModeAsync, xproto.GrabModeAsync).Check()
			if err != nil {
				logger(c.log).Debug("a chord could not be taken from the browser", "keycode", key.code, "modifiers", mask, "chord", chord, "error", err)
			}
		}
	}
	go c.readChords(codes)
}

// chordCode is one taken key as the event loop sees it: which key, and whether
// Shift was down with Control. Ctrl+Tab and Ctrl+Shift+Tab are the same
// keycode and two different chords, so the shift is part of the key here.
type chordCode struct {
	code  xproto.Keycode
	shift bool
}

// chordKeycodes answers what every taken key means, by keycode and shift, so
// the event loop is one map read per press.
func (c *x11Control) chordKeycodes() map[chordCode]string {
	out := make(map[chordCode]string, len(chordKeys()))
	syms := c.keyboardMap()
	for _, key := range chordKeys() {
		codes := syms[key.keysym]
		if len(codes) == 0 {
			if code, ok := evdevChordKeycodes[key.keysym]; ok {
				codes = []xproto.Keycode{code}
			}
		}
		for _, code := range codes {
			out[chordCode{code: code, shift: key.shift}] = key.chord
		}
	}
	return out
}

// keyboardMap answers which keycodes produce the chord keysyms, in any group
// or shift level: a second layout is as good as the first, since the key is
// the same key under the finger.
func (c *x11Control) keyboardMap() map[uint32][]xproto.Keycode {
	out := make(map[uint32][]xproto.Keycode, 2)
	setup := xproto.Setup(c.conn)
	if setup == nil {
		return out
	}
	first := setup.MinKeycode
	count := byte(setup.MaxKeycode - setup.MinKeycode + 1)
	reply, err := xproto.GetKeyboardMapping(c.conn, first, count).Reply()
	if err != nil || reply.KeysymsPerKeycode == 0 {
		return out
	}
	per := int(reply.KeysymsPerKeycode)
	wanted := make(map[uint32]bool, 2)
	for _, sym := range chordKeysyms() {
		wanted[sym] = true
	}
	for i, sym := range reply.Keysyms {
		if !wanted[uint32(sym)] {
			continue
		}
		// G115: the keyboard map was asked for from `first` to the server's
		// last keycode, and a keycode is one byte in the protocol, so
		// first + i/per cannot leave that byte.
		code := xproto.Keycode(int(first) + i/per) //nolint:gosec // G115: see above.
		if !hasKeycode(out[uint32(sym)], code) {
			out[uint32(sym)] = append(out[uint32(sym)], code)
		}
	}
	return out
}

func hasKeycode(codes []xproto.Keycode, want xproto.Keycode) bool {
	for _, code := range codes {
		if code == want {
			return true
		}
	}
	return false
}

// lockMasks answers the modifiers a grab has to ignore: Caps Lock, which is
// always the Lock modifier, and Num Lock, which is wherever the keyboard map
// put it and is read rather than assumed.
func (c *x11Control) lockMasks() []uint16 {
	masks := []uint16{uint16(xproto.ModMaskLock)}
	if num := c.numLockMask(); num != 0 {
		masks = append(masks, num)
	}
	return masks
}

// keysymNumLock is XK_Num_Lock.
const keysymNumLock uint32 = 0xff7f

func (c *x11Control) numLockMask() uint16 {
	reply, err := xproto.GetModifierMapping(c.conn).Reply()
	if err != nil || reply.KeycodesPerModifier == 0 {
		return 0
	}
	syms := c.keyboardMapOf(keysymNumLock)
	per := int(reply.KeycodesPerModifier)
	for i, code := range reply.Keycodes {
		if code == 0 || !hasKeycode(syms, code) {
			continue
		}
		return uint16(1) << uint(i/per)
	}
	return 0
}

// keyboardMapOf answers the keycodes of one keysym.
func (c *x11Control) keyboardMapOf(sym uint32) []xproto.Keycode {
	setup := xproto.Setup(c.conn)
	if setup == nil {
		return nil
	}
	first := setup.MinKeycode
	count := byte(setup.MaxKeycode - setup.MinKeycode + 1)
	reply, err := xproto.GetKeyboardMapping(c.conn, first, count).Reply()
	if err != nil || reply.KeysymsPerKeycode == 0 {
		return nil
	}
	per := int(reply.KeysymsPerKeycode)
	var out []xproto.Keycode
	for i, have := range reply.Keysyms {
		if uint32(have) != sym {
			continue
		}
		// G115: the keyboard map was asked for from `first` to the server's
		// last keycode, and a keycode is one byte in the protocol, so
		// first + i/per cannot leave that byte.
		code := xproto.Keycode(int(first) + i/per) //nolint:gosec // G115: see above.
		if !hasKeycode(out, code) {
			out = append(out, code)
		}
	}
	return out
}

// readChords turns the key presses the grab delivers into what the strip is
// told. It is the only reader of this connection's events, and it ends when
// the connection does, which is when the run does.
func (c *x11Control) readChords(codes map[chordCode]string) {
	for {
		ev, err := c.conn.WaitForEvent()
		if ev == nil && err == nil {
			return
		}
		press, ok := ev.(xproto.KeyPressEvent)
		if !ok {
			continue
		}
		shift := press.State&uint16(xproto.ModMaskShift) != 0
		if chord, ok := codes[chordCode{code: press.Detail, shift: shift}]; ok {
			c.chords.push(chord)
		}
	}
}

// activate brings the window back: a minimised window is restored, a buried
// one is raised, and the keyboard goes to it. 2 is the source indication of a
// pager, which is what the tray icon is here, and is how a window manager
// tells a person's own click from an application raising itself; 0 is
// CurrentTime, since a click on a tray icon carries no X timestamp.
func (c *x11Control) activate() error {
	return c.message("_NET_ACTIVE_WINDOW", 2, 0, 0)
}
