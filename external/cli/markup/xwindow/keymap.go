//go:build cli

package xwindow

import (
	"unicode"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"

	"github.com/EvilFreelancer/coddy-agent/external/cli/markup"
	"github.com/EvilFreelancer/coddy-agent/external/cli/xutil"
)

// keymap turns a key press into what the editor needs. The core keyboard
// mapping lists, for every keycode, the keysyms of the first two groups at
// two levels each; the group in effect comes in bits 13 and 14 of the
// event's state once the client has said it speaks XKB (useXKB), which is
// how a "us,ru" layout types Cyrillic in the second group.
type keymap struct {
	min  xproto.Keycode
	per  int
	syms []xproto.Keysym
}

func loadKeymap(c *xutil.Conn) (*keymap, error) {
	minK, maxK := c.Setup.MinKeycode, c.Setup.MaxKeycode
	r, err := xproto.GetKeyboardMapping(c.Conn, minK, byte(maxK-minK+1)).Reply()
	if err != nil {
		return nil, err
	}
	return &keymap{min: minK, per: int(r.KeysymsPerKeycode), syms: r.Keysyms}, nil
}

// useXKB tells the server this client understands XKB state, so core key
// events carry the keyboard group. Without the extension the first group
// is all there is.
func useXKB(c *xutil.Conn) {
	ext, err := xproto.QueryExtension(c.Conn, uint16(len("XKEYBOARD")), "XKEYBOARD").Reply()
	if err != nil || !ext.Present {
		return
	}
	// XkbUseExtension: major opcode, minor 0, length 2, wanted version 1.0.
	buf := make([]byte, 8)
	buf[0] = ext.MajorOpcode
	buf[1] = 0
	xgb.Put16(buf[2:], 2)
	xgb.Put16(buf[4:], 1)
	xgb.Put16(buf[6:], 0)
	cookie := c.NewCookie(true, true)
	c.NewRequest(buf, cookie)
	_, _ = cookie.Reply()
}

const (
	keysymReturn    = 0xff0d
	keysymKPEnter   = 0xff8d
	keysymEscape    = 0xff1b
	keysymBackSpace = 0xff08
)

// key turns a press of code with the given state into an editor key; ok is
// false for a key that types nothing the editor uses (a modifier alone).
func (k *keymap) key(code xproto.Keycode, state uint16) (markup.Key, bool) {
	if k == nil || code < k.min || k.per == 0 {
		return markup.Key{}, false
	}
	base := int(code-k.min) * k.per
	at := func(group, level int) xproto.Keysym {
		i := group*2 + level
		if i >= k.per || base+i >= len(k.syms) {
			return 0
		}
		return k.syms[base+i]
	}
	group := int(state>>13) & 3
	if at(group, 0) == 0 && at(group, 1) == 0 {
		// The core mapping holds two groups; a key without symbols in the
		// group in effect types what it types in the first one.
		group = 0
	}
	lower, upper := at(group, 0), at(group, 1)
	if upper == 0 {
		upper = lower
	}
	shift := state&xproto.ModMaskShift != 0
	ctrl := state&xproto.ModMaskControl != 0
	sym := lower
	if shift {
		sym = upper
	}
	switch sym {
	case keysymReturn, keysymKPEnter:
		return markup.Key{Name: "enter", Ctrl: ctrl}, true
	case keysymEscape:
		return markup.Key{Name: "escape", Ctrl: ctrl}, true
	case keysymBackSpace:
		return markup.Key{Name: "backspace", Ctrl: ctrl}, true
	}
	r := keysymRune(sym)
	if r == 0 {
		return markup.Key{}, false
	}
	plain := shortcutRune(at)
	if unicode.IsLetter(r) {
		// A letter's case is shift, inverted by caps lock; a keymap that
		// lists only one case for a key still types both.
		if shift != (state&xproto.ModMaskLock != 0) {
			r = unicode.ToUpper(r)
		} else {
			r = unicode.ToLower(r)
		}
	}
	if ctrl {
		r = unicode.ToLower(r)
	}
	return markup.Key{Rune: r, Ctrl: ctrl, Base: plain}, true
}

// shortcutRune is the letter a key stands for as a shortcut: the first Latin
// letter its groups type unshifted, so a layout list that starts with Russian
// still finds p, r or z; else whatever the first group types.
func shortcutRune(at func(group, level int) xproto.Keysym) rune {
	for g := 0; g < 2; g++ {
		if r := keysymRune(at(g, 0)); r < unicode.MaxASCII && unicode.IsLetter(r) {
			return r
		}
	}
	return keysymRune(at(0, 0))
}

// keysymRune is the character a keysym types: Latin-1 keysyms are their
// code points, Unicode keysyms carry theirs above 0x01000000, and the
// legacy Cyrillic block is looked up.
func keysymRune(sym xproto.Keysym) rune {
	switch {
	case sym >= 0x20 && sym <= 0x7e, sym >= 0xa0 && sym <= 0xff:
		return rune(sym)
	case sym >= 0x01000100 && sym <= 0x0110ffff:
		return rune(sym - 0x01000000)
	case sym >= 0x6a1 && sym <= 0x6ff:
		return cyrillic[sym-0x6a1]
	}
	return 0
}

// cyrillic maps the keysyms 0x6a1 to 0x6ff (X11 keysymdef.h) to Unicode.
var cyrillic = [...]rune{
	// 0x6a1 - 0x6af
	'ђ', 'ѓ', 'ё', 'є', 'ѕ', 'і', 'ї', 'ј', 'љ', 'њ', 'ћ', 'ќ', 'ґ', 'ў', 'џ',
	// 0x6b0 - 0x6bf
	'№', 'Ђ', 'Ѓ', 'Ё', 'Є', 'Ѕ', 'І', 'Ї', 'Ј', 'Љ', 'Њ', 'Ћ', 'Ќ', 'Ґ', 'Ў', 'Џ',
	// 0x6c0 - 0x6df
	'ю', 'а', 'б', 'ц', 'д', 'е', 'ф', 'г', 'х', 'и', 'й', 'к', 'л', 'м', 'н', 'о',
	'п', 'я', 'р', 'с', 'т', 'у', 'ж', 'в', 'ь', 'ы', 'з', 'ш', 'э', 'щ', 'ч', 'ъ',
	// 0x6e0 - 0x6ff
	'Ю', 'А', 'Б', 'Ц', 'Д', 'Е', 'Ф', 'Г', 'Х', 'И', 'Й', 'К', 'Л', 'М', 'Н', 'О',
	'П', 'Я', 'Р', 'С', 'Т', 'У', 'Ж', 'В', 'Ь', 'Ы', 'З', 'Ш', 'Э', 'Щ', 'Ч', 'Ъ',
}
