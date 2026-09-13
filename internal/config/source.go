package config

// What an editor leaves in a config file besides the configuration.
//
// A config.yaml comes off disk in whatever shape the editor that wrote it chose. On
// Windows that means every line ends with a carriage return and a line feed, and
// Notepad and its relatives may write a byte order mark in front of the first one.
// Neither is visible to the operator and neither says anything about the settings,
// but both travel through the file: the mark hides the "# yaml-language-server:"
// modeline from the check that looks for it, and a carriage return stays inside every
// comment the parser hands back, so the comment-preserving save wrote it out again
// and pushed a fresh blank line under each comment on every save.
//
// So the bytes are normalized on the way in - mark dropped, every line ending turned
// into a line feed, the line count untouched so a finding still points at the line the
// editor shows - and the file's own ending is put back on the way out, so a config
// written on Windows stays a Windows file.
//
// UTF-16 is the one shape that is not normalized but named: a file saved as "Unicode"
// rather than UTF-8 is not a text file Coddy can read, and saying so beats letting the
// parser call it a syntax error.

import "bytes"

const (
	utf8BOM   = "\xef\xbb\xbf"
	lineFeed  = "\n"
	crLineEnd = "\r\n"
)

// normalizeConfigSource makes config bytes read the same whatever editor wrote them:
// a UTF-8 byte order mark is dropped and every line ending becomes a line feed. The
// number of lines never changes, so positions reported against the result are the
// positions an editor shows.
func normalizeConfigSource(data []byte) []byte {
	return toLineFeeds(bytes.TrimPrefix(data, []byte(utf8BOM)))
}

// toLineFeeds turns Windows (CR LF) and classic Mac (CR) line endings into line feeds.
func toLineFeeds(data []byte) []byte {
	if !bytes.ContainsRune(data, '\r') {
		return data
	}
	out := bytes.ReplaceAll(data, []byte(crLineEnd), []byte(lineFeed))
	return bytes.ReplaceAll(out, []byte("\r"), []byte(lineFeed))
}

// configLineEnding reports the line ending a config file uses, so a save can write the
// file back the way its operator's editor expects it. A file whose lines mostly end the
// Windows way is a Windows file; everything else, an empty file included, is a line feed.
func configLineEnding(data []byte) string {
	total := bytes.Count(data, []byte(lineFeed))
	if total > 0 && bytes.Count(data, []byte(crLineEnd))*2 > total {
		return crLineEnd
	}
	return lineFeed
}

// applyLineEnding rewrites rendered config bytes with the given line ending.
func applyLineEnding(data []byte, ending string) []byte {
	data = toLineFeeds(data)
	if ending == crLineEnd {
		return bytes.ReplaceAll(data, []byte(lineFeed), []byte(crLineEnd))
	}
	return data
}

// utf16Encoding names the UTF-16 flavour a file was saved in, or the empty string when
// it is not one. Only the byte order mark is read: a file that starts with one is not
// UTF-8 whatever follows.
func utf16Encoding(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		return "UTF-16 LE"
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		return "UTF-16 BE"
	default:
		return ""
	}
}

// utf16Fix is what to do about such a file, in the words of the editor that wrote it.
const utf16Fix = `save the file as UTF-8 ("UTF-8" in the encoding list of Notepad's Save As dialog, "UTF-8 without BOM" in most editors)`
