//go:build windows

package platform

import (
	"testing"
	"unicode/utf8"
)

// cp866Russian is "Русский" in code page 866, the encoding a Russian Windows
// console uses. These are the kind of bytes a PowerShell parser error arrives as
// (the script never runs, so it cannot switch its own output encoding first).
var cp866Russian = []byte{0x90, 0xE3, 0xE1, 0xE1, 0xAA, 0xA8, 0xA9}

func TestDecodeCodePage866(t *testing.T) {
	got, err := decodeCodePage(866, cp866Russian)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Русский" {
		t.Fatalf("decodeCodePage(866, % x) = %q, want %q", cp866Russian, got, "Русский")
	}
}

// Whatever the console code page of the machine running the tests, output that
// is not UTF-8 must not be handed on as mojibake.
func TestDecodeOutputRepairsNonUTF8(t *testing.T) {
	got := DecodeOutput(cp866Russian)
	if !utf8.ValidString(got) {
		t.Fatalf("DecodeOutput(% x) = %q, which is still not valid UTF-8", cp866Russian, got)
	}
}
