//go:build cli

package tui

import "testing"

func TestEncodedKeyTextContract(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"modifyOtherKeys carries the final character", "\x1b[27;2;97~", "a"},
		{"Kitty does not guess the layout of digits", "\x1b[49;2u", "1"},
		{"Kitty explicit shifted symbol", "\x1b[49:33;2u", "!"},
		{"Kitty associated text takes precedence", "\x1b[49:33;2;64u", "@"},
		{"Kitty empty associated text uses the key", "\x1b[97;2;u", "A"},
		{"Kitty meta is a shortcut", "\x1b[97;33u", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, ok := ParseKey([]byte(tc.input))
			if !ok || k.Text != tc.want {
				t.Fatalf("ParseKey = %+v, %t; want text %q", k, ok, tc.want)
			}
		})
	}
	k, ok := ParseKey([]byte("\x1b[0;;101:769u"))
	if !ok || k.Name != "" || k.Text != "e\u0301" {
		t.Fatalf("text-only event must have a payload and no shortcut name: %+v, %t", k, ok)
	}
}

func TestModifyOtherKeysRejectsInvalidModifiers(t *testing.T) {
	for _, input := range []string{"\x1b[27;0;13~", "\x1b[27;-1;97~", "\x1b[27;bad;97~"} {
		if key, ok := ParseKey([]byte(input)); ok {
			t.Errorf("invalid modifier in %q became %+v", input, key)
		}
	}
}
