//go:build desktop

package desktop

import "context"

// The chords a Chromium window opened with --app answers itself, and what the
// strip does with them once the launcher has taken them away from it.
//
// Two keys need taking. `Ctrl+T` in an application window opens an ordinary
// browser window, and `Ctrl+W` closes the application - neither reaches the
// page, and neither can be cancelled from one, because a browser decides its
// own accelerators before the document sees the key. `Ctrl+1`..`Ctrl+9` are
// not among them: an application window has no tabs of its own to switch to,
// so it hands those to the page, which is where they stay.
//
// A measurement, not a guess: synthetic input (Playwright's `press`, a key
// sent with XSendEvent) bypasses the accelerator table and reaches the page
// either way, so only a real key on a real X server shows which of them the
// browser keeps. The browser check of the SPA cannot see this; the probe is
// `xdotool`-style XTEST input against a window on a display of its own.
const (
	chordNewTab   = "new-tab"
	chordCloseTab = "close-tab"
	chordNextTab  = "next-tab"
	chordPrevTab  = "prev-tab"
)

// The X keysyms of the keys. A Latin-1 character is its own keysym, so the
// first two are simply 't' and 'w'; Tab has a keysym of its own.
const (
	keysymT   uint32 = 0x0074
	keysymW   uint32 = 0x0077
	keysymTab uint32 = 0xff09
)

// chordKey is one key worth taking, under the modifiers it is taken with.
//
// Shift is part of the grab rather than read off the event because a passive
// grab matches the modifier state exactly: Ctrl+Tab and Ctrl+Shift+Tab are two
// grabs, not one, and a grab on Ctrl alone would never see the second.
type chordKey struct {
	keysym uint32
	shift  bool
	chord  string
}

// chordKeys are the keys worth taking from the browser.
func chordKeys() []chordKey {
	return []chordKey{
		{keysym: keysymT, chord: chordNewTab},
		{keysym: keysymW, chord: chordCloseTab},
		{keysym: keysymTab, chord: chordNextTab},
		{keysym: keysymTab, shift: true, chord: chordPrevTab},
	}
}

// chordKeysyms are the distinct keys a keyboard map has to be searched for.
func chordKeysyms() []uint32 {
	var out []uint32
	for _, key := range chordKeys() {
		if !containsKeysym(out, key.keysym) {
			out = append(out, key.keysym)
		}
	}
	return out
}

func containsKeysym(syms []uint32, want uint32) bool {
	for _, sym := range syms {
		if sym == want {
			return true
		}
	}
	return false
}

// chordOf answers what the strip should do with a key that was taken, under
// the modifiers it was taken with.
func chordOf(sym uint32, shift bool) (string, bool) {
	for _, key := range chordKeys() {
		if key.keysym == sym && key.shift == shift {
			return key.chord, true
		}
	}
	return "", false
}

// grabMasks answers every modifier combination a grab has to be laid under.
//
// A passive grab matches the modifier state exactly, so a grab on Ctrl alone
// stops working the moment Caps Lock or Num Lock is on - which is why every
// window manager lays the same grab once per combination of the lock
// modifiers. There are two of them in practice, so this is four grabs per key.
func grabMasks(base uint16, locks ...uint16) []uint16 {
	masks := []uint16{base}
	for _, lock := range locks {
		if lock == 0 {
			continue
		}
		for _, have := range append([]uint16(nil), masks...) {
			next := have | lock
			if !containsMask(masks, next) {
				masks = append(masks, next)
			}
		}
	}
	return masks
}

func containsMask(masks []uint16, want uint16) bool {
	for _, m := range masks {
		if m == want {
			return true
		}
	}
	return false
}

// chordQueue carries a chord from the X event loop to the page's long poll.
//
// It is a queue and not a signal because the page is not always waiting: it
// asks again as soon as an answer comes back, and a key pressed in between
// those two requests still has to arrive. It is bounded because a page that
// has gone away must not make the event loop grow memory for keys nobody will
// read.
type chordQueue struct{ ch chan string }

func newChordQueue() *chordQueue { return &chordQueue{ch: make(chan string, 16)} }

func (q *chordQueue) push(name string) {
	select {
	case q.ch <- name:
	default:
	}
}

func (q *chordQueue) next(ctx context.Context) string {
	select {
	case name := <-q.ch:
		return name
	case <-ctx.Done():
		return ""
	}
}
