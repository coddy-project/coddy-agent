//go:build desktop && linux && !android

package desktop

import "testing"

// The keyboard-map fallback of the grab, which only an X11 session has.

// Every chord on a Latin letter needs a keycode of the evdev map behind it:
// that fallback is what a Russian-only layout, which names no Latin letter at
// all, is grabbed by. A letter missing from the table is a chord the browser
// keeps on exactly the keyboards the table was written for.
func TestEveryLetterChordHasAnEvdevKeycode(t *testing.T) {
	for _, key := range chordKeys() {
		if key.keysym == keysymTab {
			continue // Every keyboard map names XK_Tab.
		}
		if _, ok := evdevChordKeycodes[key.keysym]; !ok {
			t.Errorf("the chord on keysym %#x has no evdev keycode to fall back to", key.keysym)
		}
	}
}
