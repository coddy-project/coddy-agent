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
	"unicode"
	"unicode/utf8"

	"github.com/gogs/chardet"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/ianaindex"
	xunicode "golang.org/x/text/encoding/unicode"
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
	// minSampleConfidence is the same bar for the concentrated pass in
	// detectCharset. That sample is short by construction - only the lines that
	// carry non-ASCII bytes - so chardet scores it lower for the same evidence.
	minSampleConfidence = 20
	// maxReplacementRatio rejects a decode that produced mostly U+FFFD, which is
	// what a wrong charset guess against binary input looks like.
	maxReplacementRatio = 0.1
	// minUTF16SniffBytes is the shortest input worth testing for the interleaved
	// byte pattern of BOM-less UTF-16; below it the parity ratio is meaningless.
	minUTF16SniffBytes = 8
	// minUTF16FillerRatio is the share of one parity class that must be control
	// bytes for the input to read as UTF-16. Real UTF-16 puts the code point's
	// high byte there, which is 0x00 for Latin text and 0x04 for Cyrillic.
	minUTF16FillerRatio = 0.8
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
	// Valid UTF-8 is returned as it stands, ahead of the binary guard, because a
	// stray NUL does not make a text file binary: source files do carry the odd
	// NUL literal and refusing them would hide real text from the model. The one
	// shape that must not take this path is BOM-less UTF-16, which passes
	// utf8.Valid (NUL is a code point) and would be inlined as noise.
	if utf8.Valid(data) && !looksLikeUTF16(data) {
		return string(data), CharsetUTF8, nil
	}
	if isBinary(data) {
		return "", "", ErrUndecodable
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
		return decodeWith(xunicode.UTF16(xunicode.LittleEndian, xunicode.ExpectBOM), data, "UTF-16LE")
	case bytes.HasPrefix(data, bomUTF16BE):
		return decodeWith(xunicode.UTF16(xunicode.BigEndian, xunicode.ExpectBOM), data, "UTF-16BE")
	}
	return "", "", false
}

// candidate is one decoding of the input together with the charset behind it.
type candidate struct {
	text    string
	charset string
}

// decodeDetected asks chardet what the bytes look like and decodes with the
// matching x/text encoding when the guess is both confident and supported.
//
// Detection runs twice, over the whole input and over just the lines carrying
// non-ASCII bytes. A file that is mostly ASCII with a minority of legacy text -
// source code with Russian comments, the everyday case on a Russian Windows
// install - starves the statistical model: ISO-8859-1 fits any byte sequence, so
// it wins the whole-input pass and the file decodes to mojibake. The
// concentrated pass sees only the evidence and names the real charset.
func decodeDetected(data []byte) (string, string, bool) {
	full, fullOK := decodeGuess(data, detectCharset(data, minDetectConfidence))
	sample, sampleOK := decodeGuess(data, detectCharset(nonASCIILines(data), minSampleConfidence))
	// The concentrated pass only overrules the other one when it makes a
	// positive claim the whole-input pass did not, namely a non-Latin script.
	// Between Latin charsets chardet cannot really tell 8859-1 from 8859-2, and
	// the whole-input answer is the safer of two near-identical decodings.
	if sampleOK && hasNonLatinLetters(sample.text) && (!fullOK || !hasNonLatinLetters(full.text)) {
		return sample.text, sample.charset, true
	}
	if fullOK {
		return full.text, full.charset, true
	}
	return "", "", false
}

// detectCharset names the charset chardet is most confident about, or "" when
// there is nothing to go on.
func detectCharset(data []byte, minConfidence int) string {
	if len(data) == 0 {
		return ""
	}
	res, err := chardet.NewTextDetector().DetectBest(data)
	if err != nil || res == nil || res.Confidence < minConfidence {
		return ""
	}
	return res.Charset
}

// decodeGuess decodes the whole input with a named charset, reporting failure
// for a charset x/text does not know or a decoding that is not plausible text.
func decodeGuess(data []byte, charset string) (candidate, bool) {
	if charset == "" {
		return candidate{}, false
	}
	enc, err := ianaindex.IANA.Encoding(charset)
	if err != nil || enc == nil {
		return candidate{}, false
	}
	text, cs, ok := decodeWith(enc, data, charset)
	if !ok {
		return candidate{}, false
	}
	return candidate{text: text, charset: cs}, true
}

// nonASCIILines keeps the lines holding a byte outside ASCII, which is where the
// evidence for a single-byte charset lives. It returns nil when every line
// qualifies, since the concentrated pass would then just repeat the full one.
func nonASCIILines(data []byte) []byte {
	var out bytes.Buffer
	for _, line := range bytes.Split(data, []byte("\n")) {
		for _, b := range line {
			if b >= utf8.RuneSelf {
				out.Write(line)
				out.WriteByte('\n')
				break
			}
		}
	}
	if out.Len() == 0 || out.Len() >= len(data) {
		return nil
	}
	return out.Bytes()
}

// hasNonLatinLetters reports whether the text carries a letter outside the Latin
// script. Cyrillic text mis-decoded through a Latin code page reads as Latin
// letters, so this separates a real Cyrillic reading from a plausible-looking
// Western one.
func hasNonLatinLetters(text string) bool {
	for _, r := range text {
		if unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r) {
			return true
		}
	}
	return false
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

// looksLikeUTF16 reports whether the bytes carry the interleaved pattern of
// BOM-less UTF-16: one of the two byte positions holds the code point's high
// byte, which for ordinary text is a control byte - 0x00 for Latin, 0x04 for
// Cyrillic. Tabs, newlines and carriage returns are not counted, so a file of
// very short ASCII lines does not read as UTF-16 just for being newline-heavy.
func looksLikeUTF16(data []byte) bool {
	if len(data) > binarySniffLimit {
		data = data[:binarySniffLimit]
	}
	if len(data) < minUTF16SniffBytes {
		return false
	}
	var counts, fillers [2]int
	for i, b := range data {
		counts[i%2]++
		if b < 0x20 && b != '\t' && b != '\n' && b != '\r' {
			fillers[i%2]++
		}
	}
	for i := range counts {
		if counts[i] > 0 && float64(fillers[i])/float64(counts[i]) >= minUTF16FillerRatio {
			return true
		}
	}
	return false
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
