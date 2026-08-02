// Package textenc converts the bytes of a text file to UTF-8.
//
// Workspace files are not always UTF-8: a .txt saved by Notepad on a Russian
// Windows install is Windows-1251, and legacy sources in a repository can be
// KOI8-R or ISO-8859-x. Rejecting those outright hides real text from the model,
// while decoding blindly would turn a PNG into pages of noise. DecodeToUTF8
// therefore decodes what it can identify and reports ErrUndecodable for the rest.
package textenc

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gogs/chardet"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/encoding/unicode/utf32"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// CharsetUTF8 is the charset reported for content that needed no conversion.
const CharsetUTF8 = "UTF-8"

// ErrUndecodable means the bytes are not text in any encoding we can identify.
var ErrUndecodable = errors.New("content is not decodable text")

const (
	// binarySniffLimit bounds the NUL scan; the same prefix-based heuristic git
	// uses to tell binary files from text.
	binarySniffLimit = 8 * 1024
	// minDetectConfidence is the chardet score (1..100) below which a guess is
	// treated as noise. Detection of a single-byte charset from a short file is
	// inherently weak, so the Windows ANSI fallback picks up what is rejected here.
	minDetectConfidence = 30
	// maxReplacementRatio rejects a decode that produced mostly U+FFFD, which is
	// what a wrong charset guess against binary input looks like.
	maxReplacementRatio = 0.1
)

var (
	bomUTF8    = []byte{0xEF, 0xBB, 0xBF}
	bomUTF16LE = []byte{0xFF, 0xFE}
	bomUTF16BE = []byte{0xFE, 0xFF}
	bomUTF32LE = []byte{0xFF, 0xFE, 0x00, 0x00}
	bomUTF32BE = []byte{0x00, 0x00, 0xFE, 0xFF}
)

// DecodeToUTF8 returns data as a UTF-8 string plus the charset it was read as.
// Content that already is UTF-8 is returned untouched, so the common case costs
// one validation pass. ErrUndecodable is returned for binary content and for
// text whose encoding could not be identified.
func DecodeToUTF8(data []byte) (text string, charset string, err error) {
	if len(data) == 0 {
		return "", CharsetUTF8, nil
	}
	if text, charset, ok := decodeBOM(data); ok {
		return text, charset, nil
	}
	// The binary check precedes the UTF-8 fast path on purpose: NUL is a valid
	// UTF-8 code point, so BOM-less UTF-16 and plenty of binary formats pass
	// utf8.Valid and would otherwise be inlined as noise.
	if isBinary(data) {
		return "", "", ErrUndecodable
	}
	if utf8.Valid(data) {
		return string(data), CharsetUTF8, nil
	}
	if text, charset, ok := decodeDetected(data); ok {
		return text, charset, nil
	}
	// Last resort on Windows: a file written by a local editor is usually in the
	// machine's ANSI code page (1251 on a Russian install), which is exactly the
	// case chardet is least sure about when the file is short.
	if text, charset, ok := decodeSystemANSI(data); ok {
		return text, charset, nil
	}
	return "", "", ErrUndecodable
}

// decodeBOM handles the byte-order marks that identify an encoding outright.
// UTF-32 is checked before UTF-16 because the UTF-32LE mark starts with the
// UTF-16LE one.
func decodeBOM(data []byte) (string, string, bool) {
	switch {
	case bytes.HasPrefix(data, bomUTF8):
		rest := data[len(bomUTF8):]
		if utf8.Valid(rest) {
			return string(rest), CharsetUTF8, true
		}
	case bytes.HasPrefix(data, bomUTF32LE):
		return decodeWith(utf32.UTF32(utf32.LittleEndian, utf32.ExpectBOM), data, "UTF-32LE")
	case bytes.HasPrefix(data, bomUTF32BE):
		return decodeWith(utf32.UTF32(utf32.BigEndian, utf32.ExpectBOM), data, "UTF-32BE")
	case bytes.HasPrefix(data, bomUTF16LE):
		return decodeWith(unicode.UTF16(unicode.LittleEndian, unicode.ExpectBOM), data, "UTF-16LE")
	case bytes.HasPrefix(data, bomUTF16BE):
		return decodeWith(unicode.UTF16(unicode.BigEndian, unicode.ExpectBOM), data, "UTF-16BE")
	}
	return "", "", false
}

// decodeDetected asks chardet what the bytes look like and decodes with the
// matching x/text encoding when the guess is both confident and supported.
func decodeDetected(data []byte) (string, string, bool) {
	res, err := chardet.NewTextDetector().DetectBest(data)
	if err != nil || res == nil || res.Confidence < minDetectConfidence {
		return "", "", false
	}
	enc, err := ianaindex.IANA.Encoding(res.Charset)
	if err != nil || enc == nil {
		return "", "", false
	}
	return decodeWith(enc, data, res.Charset)
}

// decodeWith runs one encoding over the bytes and rejects a result that is not
// plausible text, so a wrong guess falls through to the next strategy.
func decodeWith(enc encoding.Encoding, data []byte, charset string) (string, string, bool) {
	decoded, err := enc.NewDecoder().Bytes(data)
	if err != nil || !plausibleText(decoded) {
		return "", "", false
	}
	return string(decoded), charset, true
}

// decodeSystemANSI reuses the platform code-page decoder; outside Windows there
// is no system ANSI code page and this is always a miss.
func decodeSystemANSI(data []byte) (string, string, bool) {
	decoded, codePage, ok := platform.DecodeANSI(data)
	if !ok || !plausibleText([]byte(decoded)) {
		return "", "", false
	}
	return decoded, fmt.Sprintf("windows-%d", codePage), true
}

// isBinary reports whether the prefix contains a NUL byte. UTF-16 and UTF-32
// text trips this, which is why the BOM branch runs first.
func isBinary(data []byte) bool {
	if len(data) > binarySniffLimit {
		data = data[:binarySniffLimit]
	}
	return bytes.IndexByte(data, 0) >= 0
}

// plausibleText rejects decoder output that is mostly replacement characters or
// still carries NUL bytes - both signs the input was never text in that charset.
func plausibleText(decoded []byte) bool {
	if !utf8.Valid(decoded) || bytes.IndexByte(decoded, 0) >= 0 {
		return false
	}
	total := utf8.RuneCount(decoded)
	if total == 0 {
		return false
	}
	bad := strings.Count(string(decoded), string(utf8.RuneError))
	return float64(bad)/float64(total) <= maxReplacementRatio
}
