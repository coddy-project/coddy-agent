//go:build cli

package tui

import (
	"strconv"
	"strings"
)

// Key is a decoded keyboard event.
type Key struct {
	// Name is the canonical key name: a letter/digit/symbol, or one of
	// enter, escape, tab, space, backspace, delete, insert, home, end,
	// pageup, pagedown, up, down, left, right, f1..f12.
	Name  string
	Shift bool
	Alt   bool
	Ctrl  bool
	Super bool
}

// String renders the key back to "ctrl+shift+x" notation.
func (k Key) String() string {
	var parts []string
	if k.Ctrl {
		parts = append(parts, "ctrl")
	}
	if k.Shift {
		parts = append(parts, "shift")
	}
	if k.Alt {
		parts = append(parts, "alt")
	}
	if k.Super {
		parts = append(parts, "super")
	}
	parts = append(parts, k.Name)
	return strings.Join(parts, "+")
}

// ParseKey decodes one complete input sequence into a Key. Returns false for
// sequences that are not key presses (mouse, focus, paste bodies) or key
// release events (kitty ":3" suffix).
func ParseKey(data []byte) (Key, bool) {
	s := string(data)
	if s == "" {
		return Key{}, false
	}

	// Kitty / modifyOtherKeys CSI forms first.
	if strings.HasPrefix(s, "\x1b[") {
		return parseCSIKey(s)
	}
	if strings.HasPrefix(s, "\x1bO") && len(s) == 3 {
		return parseSS3Key(s[2])
	}
	// Alt+key: ESC prefix followed by a printable or control byte.
	if len(s) >= 2 && s[0] == 0x1b {
		inner, ok := ParseKey([]byte(s[1:]))
		if !ok {
			return Key{}, false
		}
		inner.Alt = true
		return inner, true
	}
	if s == "\x1b" {
		return Key{Name: "escape"}, true
	}

	// Single byte forms.
	if len(s) == 1 {
		b := s[0]
		switch b {
		case '\r':
			return Key{Name: "enter"}, true
		case '\n':
			return Key{Name: "j", Ctrl: true}, true
		case '\t':
			return Key{Name: "tab"}, true
		case ' ':
			return Key{Name: "space"}, true
		case 0x7f:
			return Key{Name: "backspace"}, true
		case 0x08:
			return Key{Name: "backspace", Ctrl: true}, true
		case 0:
			return Key{Name: "space", Ctrl: true}, true
		}
		if b >= 1 && b <= 26 {
			return Key{Name: string(rune('a' + b - 1)), Ctrl: true}, true
		}
		if b >= 0x20 && b < 0x7f {
			r := rune(b)
			k := Key{Name: strings.ToLower(string(r))}
			if r >= 'A' && r <= 'Z' {
				k.Shift = true
			}
			return k, true
		}
		return Key{}, false
	}

	// Multi-byte UTF-8 printable (IME input etc.) - not a named key.
	return Key{}, false
}

func parseSS3Key(final byte) (Key, bool) {
	switch final {
	case 'A':
		return Key{Name: "up"}, true
	case 'B':
		return Key{Name: "down"}, true
	case 'C':
		return Key{Name: "right"}, true
	case 'D':
		return Key{Name: "left"}, true
	case 'H':
		return Key{Name: "home"}, true
	case 'F':
		return Key{Name: "end"}, true
	case 'P':
		return Key{Name: "f1"}, true
	case 'Q':
		return Key{Name: "f2"}, true
	case 'R':
		return Key{Name: "f3"}, true
	case 'S':
		return Key{Name: "f4"}, true
	}
	return Key{}, false
}

var tildeKeys = map[int]string{
	1: "home", 2: "insert", 3: "delete", 4: "end", 5: "pageup", 6: "pagedown",
	7: "home", 8: "end",
	11: "f1", 12: "f2", 13: "f3", 14: "f4", 15: "f5",
	17: "f6", 18: "f7", 19: "f8", 20: "f9", 21: "f10", 23: "f11", 24: "f12",
}

var letterFinals = map[byte]string{
	'A': "up", 'B': "down", 'C': "right", 'D': "left",
	'H': "home", 'F': "end", 'Z': "tab",
}

func parseCSIKey(s string) (Key, bool) {
	body := s[2:]
	if body == "" {
		return Key{}, false
	}
	final := body[len(body)-1]
	params := body[:len(body)-1]

	// Ignore mouse, focus, paste, and query replies.
	switch final {
	case 'M', 'm', 'I', 'O', 't', 'c', 'n', 'R', 'y':
		if final != 'R' {
			return Key{}, false
		}
	}

	switch final {
	case '~':
		fields := splitParams(params)
		if len(fields) == 0 {
			return Key{}, false
		}
		code, err := strconv.Atoi(fields[0])
		if err != nil {
			return Key{}, false
		}
		// modifyOtherKeys: CSI 27;<mod>;<codepoint>~
		if code == 27 && len(fields) >= 3 {
			mod, err1 := strconv.Atoi(fields[1])
			cp, err2 := strconv.Atoi(fields[2])
			if err1 != nil || err2 != nil {
				return Key{}, false
			}
			return keyFromCodepoint(cp, mod)
		}
		name, ok := tildeKeys[code]
		if !ok {
			return Key{}, false
		}
		k := Key{Name: name}
		if len(fields) >= 2 {
			applyMod(&k, fields[1])
		}
		return k, true
	case 'u':
		// Kitty: CSI <codepoint>[;<mod>[:event]]u
		fields := splitParams(params)
		if len(fields) == 0 {
			return Key{}, false
		}
		cp, err := strconv.Atoi(strings.SplitN(fields[0], ":", 2)[0])
		if err != nil {
			return Key{}, false
		}
		mod := 1
		if len(fields) >= 2 {
			modField := strings.SplitN(fields[1], ":", 2)
			if len(modField) == 2 && modField[1] == "3" {
				return Key{}, false // key release event
			}
			if m, err := strconv.Atoi(modField[0]); err == nil {
				mod = m
			}
		}
		return keyFromCodepoint(cp, mod)
	default:
		if name, ok := letterFinals[final]; ok {
			k := Key{Name: name}
			if final == 'Z' {
				k.Shift = true
			}
			fields := splitParams(params)
			if len(fields) >= 2 {
				applyMod(&k, fields[1])
			}
			return k, true
		}
	}
	return Key{}, false
}

func splitParams(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ";")
}

func applyMod(k *Key, field string) {
	mod, err := strconv.Atoi(strings.SplitN(field, ":", 2)[0])
	if err != nil || mod < 1 {
		return
	}
	bits := mod - 1
	k.Shift = k.Shift || bits&1 != 0
	k.Alt = k.Alt || bits&2 != 0
	k.Ctrl = k.Ctrl || bits&4 != 0
	k.Super = k.Super || bits&8 != 0
}

func keyFromCodepoint(cp, mod int) (Key, bool) {
	k := Key{}
	bits := mod - 1
	if bits > 0 {
		k.Shift = bits&1 != 0
		k.Alt = bits&2 != 0
		k.Ctrl = bits&4 != 0
		k.Super = bits&8 != 0
	}
	switch cp {
	case 13:
		k.Name = "enter"
	case 27:
		k.Name = "escape"
	case 9:
		k.Name = "tab"
	case 32:
		k.Name = "space"
	case 127, 8:
		k.Name = "backspace"
	default:
		if cp >= 0x21 && cp < 0x7f {
			r := rune(cp)
			k.Name = strings.ToLower(string(r))
			if r >= 'A' && r <= 'Z' {
				k.Shift = true
			}
		} else {
			return Key{}, false
		}
	}
	return k, true
}

// MatchesKey reports whether data decodes to the key spec ("ctrl+c",
// "shift+enter", "up", "alt+backspace", ...).
func MatchesKey(data []byte, spec string) bool {
	got, ok := ParseKey(data)
	if !ok {
		return false
	}
	want, ok := parseSpec(spec)
	if !ok {
		return false
	}
	return got == want
}

func parseSpec(spec string) (Key, bool) {
	parts := strings.Split(strings.ToLower(strings.TrimSpace(spec)), "+")
	if len(parts) == 0 {
		return Key{}, false
	}
	k := Key{}
	for _, p := range parts[:len(parts)-1] {
		switch p {
		case "ctrl":
			k.Ctrl = true
		case "shift":
			k.Shift = true
		case "alt", "option":
			k.Alt = true
		case "super", "cmd":
			k.Super = true
		default:
			return Key{}, false
		}
	}
	name := parts[len(parts)-1]
	switch name {
	case "esc":
		name = "escape"
	case "return":
		name = "enter"
	case "pgup", "pageUp":
		name = "pageup"
	case "pgdn", "pagedown", "pageDown":
		name = "pagedown"
	}
	k.Name = name
	return k, true
}
