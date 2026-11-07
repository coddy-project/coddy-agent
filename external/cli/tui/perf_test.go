//go:build cli

package tui

import "testing"

func BenchmarkConsoleKeyDecoding(b *testing.B) {
	for _, tc := range []struct{ name, input string }{
		{"plain", "A"},
		{"modifyOtherKeys letter", "\x1b[27;2;65~"},
		{"modifyOtherKeys Cyrillic", "\x1b[27;2;1046~"},
		{"Kitty shifted symbol", "\x1b[49:33;2u"},
		{"Kitty associated text", "\x1b[0;;101:769u"},
	} {
		b.Run(tc.name, func(b *testing.B) {
			data := []byte(tc.input)
			b.ReportAllocs()
			for b.Loop() {
				ParseKey(data)
			}
		})
	}
}
