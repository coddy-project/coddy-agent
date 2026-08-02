package textenc_test

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/encoding/unicode/utf32"

	"github.com/EvilFreelancer/coddy-agent/internal/textenc"
)

// russian is long enough for statistical charset detection to be confident,
// which is what a real note or README looks like.
const russian = "Привет, мир!\n" +
	"Это обычный текстовый файл на русском языке, сохранённый в устаревшей кодировке.\n" +
	"Он содержит несколько строк, чтобы определение кодировки было надёжным.\n" +
	"Проверяем, что содержимое читается без потерь и без замен символов.\n"

func mustEncode(t *testing.T, enc encoding.Encoding, s string) []byte {
	t.Helper()
	b, err := enc.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return b
}

func TestDecodeToUTF8(t *testing.T) {
	german := "Grüße aus München. Die Straße war heute völlig überfüllt und größtenteils gesperrt.\n" +
		"Wir mussten früher zurück, weil das Wetter schlechter wurde als angekündigt.\n"

	cases := []struct {
		name string
		data []byte
		want string
	}{
		{name: "ascii", data: []byte("plain ascii text"), want: "plain ascii text"},
		{name: "utf8 multibyte", data: []byte(russian), want: russian},
		{name: "utf8 with bom", data: append([]byte{0xEF, 0xBB, 0xBF}, russian...), want: russian},
		{name: "windows-1251", data: mustEncode(t, charmap.Windows1251, russian), want: russian},
		{name: "koi8-r", data: mustEncode(t, charmap.KOI8R, russian), want: russian},
		{name: "iso-8859-1", data: mustEncode(t, charmap.ISO8859_1, german), want: german},
		{
			name: "utf16le with bom",
			data: mustEncode(t, unicode.UTF16(unicode.LittleEndian, unicode.UseBOM), russian),
			want: russian,
		},
		{
			name: "utf16be with bom",
			data: mustEncode(t, unicode.UTF16(unicode.BigEndian, unicode.UseBOM), russian),
			want: russian,
		},
		{
			name: "utf32le with bom",
			data: mustEncode(t, utf32.UTF32(utf32.LittleEndian, utf32.UseBOM), russian),
			want: russian,
		},
		{name: "empty", data: nil, want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, charset, err := textenc.DecodeToUTF8(tc.data)
			if err != nil {
				t.Fatalf("DecodeToUTF8: %v", err)
			}
			if got != tc.want {
				t.Fatalf("decoded %q, want %q (charset %q)", got, tc.want, charset)
			}
			if charset == "" {
				t.Fatal("charset is empty")
			}
		})
	}
}

func TestDecodeToUTF8RejectsNonText(t *testing.T) {
	pngHeader := []byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 'I', 'H', 'D', 'R'}

	cases := []struct {
		name string
		data []byte
	}{
		{name: "png header", data: pngHeader},
		{name: "utf16le without bom", data: mustEncode(t, unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM), "hello")},
		{name: "utf16be without bom", data: mustEncode(t, unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM), russian)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := textenc.DecodeToUTF8(tc.data)
			if !errors.Is(err, textenc.ErrUndecodable) {
				t.Fatalf("err = %v, want ErrUndecodable", err)
			}
		})
	}
}

// TestDecodeToUTF8DetectsWindows1251WithoutPlatformFallback pins the portable
// path: the Windows ANSI fallback is a no-op on Linux CI, so detection alone has
// to name the charset. A regression here would only show up off Windows.
func TestDecodeToUTF8DetectsWindows1251(t *testing.T) {
	got, charset, err := textenc.DecodeToUTF8(mustEncode(t, charmap.Windows1251, russian))
	if err != nil {
		t.Fatalf("DecodeToUTF8: %v", err)
	}
	if got != russian {
		t.Fatalf("decoded %q, want %q", got, russian)
	}
	if !strings.EqualFold(charset, "windows-1251") {
		t.Fatalf("charset = %q, want windows-1251", charset)
	}
}

// TestDecodeToUTF8KeepsUTF8WithEmbeddedNUL pins that a stray NUL does not turn
// valid UTF-8 into "binary". Source files do carry the odd NUL literal - this
// repository's own external/ui/src/ui/settings/SkillsSection.tsx has one inside
// a string constant - and refusing them would hide real text from the model.
func TestDecodeToUTF8KeepsUTF8WithEmbeddedNUL(t *testing.T) {
	in := []byte("const SYNC_ALL_KEY = \"\x00all\";\n" + russian)
	got, charset, err := textenc.DecodeToUTF8(in)
	if err != nil {
		t.Fatalf("DecodeToUTF8: %v", err)
	}
	if got != string(in) {
		t.Fatalf("decoded %q, want the input unchanged", got)
	}
	if !strings.EqualFold(charset, "utf-8") {
		t.Fatalf("charset = %q, want utf-8", charset)
	}
}

// TestDecodeToUTF8DetectsLegacyCharsetInMostlyASCIIFile covers the shape a
// developer actually commits: source code where the ASCII bulk outweighs the
// legacy-encoded comments. Detection over the whole file loses to ISO-8859-1
// there, which fits any byte sequence, and the file decodes to mojibake.
func TestDecodeToUTF8DetectsLegacyCharsetInMostlyASCIIFile(t *testing.T) {
	const source = "package main\n" +
		"\n" +
		"import \"fmt\"\n" +
		"\n" +
		"// Точка входа в программу.\n" +
		"func main() {\n" +
		"\tfmt.Println(\"hello\")\n" +
		"\t// Здесь считаем сумму значений.\n" +
		"\ttotal := 0\n" +
		"\tfor i := 0; i < 10; i++ {\n" +
		"\t\ttotal += i\n" +
		"\t}\n" +
		"\tfmt.Println(total)\n" +
		"}\n"

	cases := []struct {
		name string
		enc  encoding.Encoding
		want string
	}{
		{name: "windows-1251", enc: charmap.Windows1251, want: "windows-1251"},
		{name: "koi8-r", enc: charmap.KOI8R, want: "KOI8-R"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, charset, err := textenc.DecodeToUTF8(mustEncode(t, tc.enc, source))
			if err != nil {
				t.Fatalf("DecodeToUTF8: %v", err)
			}
			if got != source {
				t.Fatalf("decoded %q,\nwant %q", got, source)
			}
			if !strings.EqualFold(charset, tc.want) {
				t.Fatalf("charset = %q, want %q", charset, tc.want)
			}
		})
	}
}

// TestDecodeToUTF8KeepsLatin1ForWesternText guards the other side of that
// change: a Western file must not be dragged onto a Cyrillic charset by the
// concentrated detection pass.
func TestDecodeToUTF8KeepsLatin1ForWesternText(t *testing.T) {
	const source = "#!/bin/sh\n" +
		"# Copyright (C) 2003,04,07 Guido Günther <agx@example.org>\n" +
		"# Distributed under the terms of the GNU General Public License.\n" +
		"set -e\n" +
		"echo \"starting daemon\"\n" +
		"exit 0\n"
	got, _, err := textenc.DecodeToUTF8(mustEncode(t, charmap.ISO8859_1, source))
	if err != nil {
		t.Fatalf("DecodeToUTF8: %v", err)
	}
	if got != source {
		t.Fatalf("decoded %q,\nwant %q", got, source)
	}
}

// TestDecodeToUTF8PassesUTF8Through checks the fast path returns the exact input
// bytes, since most attachments are already UTF-8.
func TestDecodeToUTF8PassesUTF8Through(t *testing.T) {
	in := []byte("# Заголовок\nmixed текст and English\n")
	got, charset, err := textenc.DecodeToUTF8(in)
	if err != nil {
		t.Fatalf("DecodeToUTF8: %v", err)
	}
	if got != string(in) {
		t.Fatalf("decoded %q, want %q", got, in)
	}
	if !strings.EqualFold(charset, "utf-8") {
		t.Fatalf("charset = %q, want utf-8", charset)
	}
}
