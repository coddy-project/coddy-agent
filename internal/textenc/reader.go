package textenc

import (
	"bytes"
	"io"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/encoding/ianaindex"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/encoding/unicode/utf32"
)

// NewReader detects a leading sample with the same policy as DecodeToUTF8 and
// decodes the rest incrementally. It never buffers the entire source file.
func NewReader(src io.Reader) (io.Reader, string, error) {
	sample := make([]byte, 8192)
	n, err := io.ReadFull(src, sample)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, "", err
	}
	sample = sample[:n]
	probe := sample
	// A sample may end halfway through a UTF-8 rune. Keep those bytes in the
	// input stream, but do not let the incomplete rune change its detection.
	for cut := 0; cut < 4 && len(probe) > 0 && !utf8.Valid(probe); cut++ {
		probe = sample[:len(sample)-cut-1]
	}
	if !utf8.Valid(probe) {
		probe = sample
	}
	_, charset, err := DecodeToUTF8(probe)
	if err != nil {
		return nil, "", err
	}
	input := io.MultiReader(bytes.NewReader(sample), src)
	switch strings.ToUpper(charset) {
	case "UTF-8":
		return unicode.UTF8BOM.NewDecoder().Reader(input), charset, nil
	case "UTF-16LE":
		return unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder().Reader(input), charset, nil
	case "UTF-16BE":
		return unicode.UTF16(unicode.BigEndian, unicode.UseBOM).NewDecoder().Reader(input), charset, nil
	case "UTF-32LE":
		return utf32.UTF32(utf32.LittleEndian, utf32.UseBOM).NewDecoder().Reader(input), charset, nil
	case "UTF-32BE":
		return utf32.UTF32(utf32.BigEndian, utf32.UseBOM).NewDecoder().Reader(input), charset, nil
	}
	enc, err := ianaindex.MIME.Encoding(charset)
	if err != nil || enc == nil {
		return nil, "", ErrUndecodable
	}
	return enc.NewDecoder().Reader(input), charset, nil
}
