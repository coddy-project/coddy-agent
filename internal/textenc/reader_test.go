package textenc

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
)

func TestReaderStreamsPastSampleAndPreservesSplitRune(t *testing.T) {
	want := strings.Repeat("a", 8191) + "Привет\n" + strings.Repeat("tail\n", 200000)
	src := strings.NewReader(want)
	r, charset, err := NewReader(src)
	if err != nil {
		t.Fatal(err)
	}
	if src.Len() != len(want)-8192 {
		t.Fatalf("eagerly read %d bytes", len(want)-src.Len())
	}
	got, err := io.ReadAll(r)
	if err != nil || string(got) != want || charset != CharsetUTF8 {
		t.Fatalf("stream changed: charset=%s length=%d err=%v", charset, len(got), err)
	}
}

func TestReaderUsesExistingEncodingPolicy(t *testing.T) {
	want := strings.Repeat("Привет, мир!\r\n", 1000)
	utf16, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes([]byte(want))
	if err != nil {
		t.Fatal(err)
	}
	legacy, err := charmap.Windows1251.NewEncoder().Bytes([]byte(want))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"utf16": utf16, "legacy": legacy, "bom": append([]byte{0xef, 0xbb, 0xbf}, []byte(want)...)} {
		t.Run(name, func(t *testing.T) {
			r, _, err := NewReader(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(r)
			if err != nil || string(got) != want {
				t.Fatalf("wrong decoding: %.40q %v", got, err)
			}
		})
	}
}
