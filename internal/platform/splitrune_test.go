package platform

import (
	"bytes"
	"testing"
)

// A read that ends inside a character must not be decoded on its own: the
// bytes already shown would otherwise change meaning when the rest arrives.
func TestSplitPartialRuneHoldsBackOnlyACutUTF8Sequence(t *testing.T) {
	whole := []byte("привет")
	for cut := 1; cut < 4 && cut < len(whole); cut++ {
		head, carry := SplitPartialRune(whole[:len(whole)-cut])
		joined := append(append([]byte(nil), head...), carry...)
		if !bytes.Equal(joined, whole[:len(whole)-cut]) {
			t.Fatalf("cut %d loses bytes: %q + %q", cut, head, carry)
		}
		if len(carry) == 0 && cut%2 == 1 {
			t.Fatalf("cut %d left a half character in the head: %q", cut, head)
		}
	}
}

func TestSplitPartialRuneKeepsCompleteInputWhole(t *testing.T) {
	for _, in := range []string{"", "plain ascii\n", "привет\n", "日本語"} {
		head, carry := SplitPartialRune([]byte(in))
		if string(head) != in || len(carry) != 0 {
			t.Fatalf("%q was split into %q + %q", in, head, carry)
		}
	}
}

// Bytes in a legacy code page are complete characters already. Holding one
// back because it looks like a UTF-8 lead byte would keep the last letter of
// a Windows prompt off the screen until the next command printed something.
func TestSplitPartialRuneKeepsCodePageBytesWhole(t *testing.T) {
	// "Привет" in code page 866: every byte is its own character.
	cp866 := []byte{0x8f, 0xe0, 0xa8, 0xa2, 0xa5, 0xe2}
	head, carry := SplitPartialRune(cp866)
	if !bytes.Equal(head, cp866) || len(carry) != 0 {
		t.Fatalf("code page bytes were split: %q + %q", head, carry)
	}
}
