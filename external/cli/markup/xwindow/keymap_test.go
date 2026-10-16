//go:build cli

package xwindow

import (
	"testing"

	"github.com/jezek/xgb/xproto"

	"github.com/EvilFreelancer/coddy-agent/external/cli/markup"
)

func TestKeymapTypesBothGroupsAndBothCases(t *testing.T) {
	const group2 = 1 << 13
	k := &keymap{min: 10, per: 4, syms: []xproto.Keysym{
		'a', 'A', 0x6c6, 0x6e6, // a A ф Ф
		keysymReturn, 0, 0, 0,
		'2', '@', 0, 0, // no second group: types the first
		0xff1b, 0, 0, 0,
		0x6a3, 0x6b3, 0, 0, // ё Ё
	}}
	cases := []struct {
		code  xproto.Keycode
		state uint16
		want  markup.Key
	}{
		{10, 0, markup.Key{Rune: 'a', Base: 'a'}},
		{10, xproto.ModMaskShift, markup.Key{Rune: 'A', Base: 'a'}},
		{10, xproto.ModMaskLock, markup.Key{Rune: 'A', Base: 'a'}},
		{10, xproto.ModMaskLock | xproto.ModMaskShift, markup.Key{Rune: 'a', Base: 'a'}},
		{10, group2, markup.Key{Rune: 'ф', Base: 'a'}},
		{10, group2 | xproto.ModMaskShift, markup.Key{Rune: 'Ф', Base: 'a'}},
		{10, xproto.ModMaskControl, markup.Key{Rune: 'a', Ctrl: true, Base: 'a'}},
		{10, xproto.ModMaskControl | xproto.ModMaskShift, markup.Key{Rune: 'a', Ctrl: true, Base: 'a'}},
		{11, 0, markup.Key{Name: "enter"}},
		{12, group2, markup.Key{Rune: '2', Base: '2'}},
		{12, group2 | xproto.ModMaskShift, markup.Key{Rune: '@', Base: '2'}},
		{13, 0, markup.Key{Name: "escape"}},
		{14, 0, markup.Key{Rune: 'ё', Base: 'ё'}},
		{14, xproto.ModMaskShift, markup.Key{Rune: 'Ё', Base: 'ё'}},
	}
	for _, c := range cases {
		got, ok := k.key(c.code, c.state)
		if !ok || got != c.want {
			t.Errorf("key %d state %#x = %+v %v, want %+v", c.code, c.state, got, ok, c.want)
		}
	}
	if _, ok := k.key(9, 0); ok {
		t.Error("a keycode below the mapping typed something")
	}
}

func TestKeysymRune(t *testing.T) {
	for sym, want := range map[xproto.Keysym]rune{
		'q':        'q',
		0xe9:       'é',
		0x6c1:      'а',
		0x6ff:      'Ъ',
		0x6b0:      '№',
		0x01000416: 'Ж',
		0x0100263a: '☺',
		0xffe1:     0, // Shift_L types nothing
	} {
		if got := keysymRune(sym); got != want {
			t.Errorf("keysym %#x = %q, want %q", sym, got, want)
		}
	}
}
