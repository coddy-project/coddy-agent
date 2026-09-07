//go:build windows

package textenc_test

import (
	"testing"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/textenc"
)

// requireCyrillicANSI skips a test unless the machine's ANSI code page reads
// single-byte Cyrillic. These cases are about the code page standing in for
// statistical detection, so on a Western Windows install there is nothing to
// stand in with and the expectation would not hold.
func requireCyrillicANSI(t *testing.T) {
	t.Helper()
	text, _, ok := platform.DecodeANSI([]byte{0xCF, 0xF0, 0xE8})
	if !ok || text != "При" {
		t.Skip("system ANSI code page is not Cyrillic")
	}
}

// TestDecodeToUTF8ShortMixedFilesUseSystemANSI covers the files chardet cannot
// call: mostly ASCII with a minority of Cyrillic, too little evidence for the
// statistical model either over the whole input or over its non-ASCII lines.
// ISO-8859-1 fits any byte sequence and wins on confidence, so without the ANSI
// code page these decode to mojibake with no error raised.
func TestDecodeToUTF8ShortMixedFilesUseSystemANSI(t *testing.T) {
	requireCyrillicANSI(t)
	tests := []struct {
		name string
		want string
	}{
		{name: "go comment", want: "package main\n\nfunc main() {\n\t// Готово\n}\n"},
		{name: "ini comment", want: "name=test\nport=80\n# Имя\n"},
		{name: "markdown heading", want: "# Report\n\nsee table below\n\n## Итоги\n\n- one\n- two\n"},
		{name: "json value", want: "{\n  \"id\": 1,\n  \"title\": \"Отчёт\"\n}\n"},
		{name: "log line", want: "2026-08-02 12:00:01 INFO  started\n2026-08-02 12:00:02 ERROR ошибка\n"},
		{name: "csv cell", want: "id,name,city\n1,alpha,Москва\n2,beta,london\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, charset, err := textenc.DecodeToUTF8(mustEncode(t, charmap.Windows1251, tt.want))
			if err != nil {
				t.Fatalf("DecodeToUTF8: %v", err)
			}
			if got != tt.want {
				t.Fatalf("text = %q, want %q (charset %q)", got, tt.want, charset)
			}
		})
	}
}

// TestDecodeToUTF8KeepsWesternReadings pins the other side of the choice: a
// Western file on a Cyrillic Windows install must keep the detected reading
// rather than being re-read through the ANSI code page.
func TestDecodeToUTF8KeepsWesternReadings(t *testing.T) {
	requireCyrillicANSI(t)
	tests := []struct {
		name string
		enc  encoding.Encoding
		want string
	}{
		{name: "french", enc: charmap.ISO8859_1, want: "Café au lait\nprice: 5 EUR\nthanks\n"},
		{name: "german", enc: charmap.ISO8859_1, want: "Müller GmbH\naddress: Berlin\nphone: 123\n"},
		{name: "french accents", enc: charmap.ISO8859_1, want: "naïve résumé\nfrom the café\n"},
		{name: "spanish", enc: charmap.ISO8859_1, want: "El niño come mañana\nen la playa\n"},
		{name: "standalone accented word", enc: charmap.ISO8859_1, want: "il va à Paris\ndemain matin\n"},
		{name: "nordic", enc: charmap.ISO8859_1, want: "Blåbær og rødgrød\nsmager godt\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, charset, err := textenc.DecodeToUTF8(mustEncode(t, tt.enc, tt.want))
			if err != nil {
				t.Fatalf("DecodeToUTF8: %v", err)
			}
			if got != tt.want {
				t.Fatalf("text = %q, want %q (charset %q)", got, tt.want, charset)
			}
		})
	}
}
