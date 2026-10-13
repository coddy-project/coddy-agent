package llm

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// pictureTypes are the picture types the Anthropic Messages API and the
// OpenAI Responses API take as an image.
var pictureTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// What an attached part is to a provider that takes only pictureTypes.
const (
	attachedPicture    = iota // sent as a picture
	attachedText              // written out as a labelled text block
	attachedUnsendable        // a picture it cannot take, named instead
)

// IsPicture reports whether an attached part reaches a provider as a picture
// rather than as text: an image of any type but SVG, which is text, whether
// the part is a data URL or the file it is kept as, or an https address. A
// provider that takes fewer types names the others instead of sending them,
// so a picture never travels as text.
func IsPicture(ip ImagePart) bool {
	if ip.DataURL == "" {
		return isPictureType(ip.MIMEType)
	}
	if !strings.HasPrefix(ip.DataURL, "data:") {
		return strings.HasPrefix(ip.DataURL, "https://")
	}
	mime, _, _ := parseDataURL(ip.DataURL)
	return isPictureType(mime)
}

// isPictureType reports whether a media type is a picture, SVG not counted.
func isPictureType(mime string) bool {
	return strings.HasPrefix(mime, "image/") && mime != "image/svg+xml"
}

// parseDataURL splits a data URL (RFC 2397) into its media type, lower case
// and without parameters, whether its payload is base64 - the flag is the
// last parameter before the comma, spelled in any case - and the payload.
// Anything that is not a data URL has no type and is its own payload.
func parseDataURL(dataURL string) (mime string, isBase64 bool, payload string) {
	rest, ok := strings.CutPrefix(dataURL, "data:")
	if !ok {
		return "", false, dataURL
	}
	header, payload, found := strings.Cut(rest, ",")
	if !found {
		return "", false, dataURL
	}
	params := strings.Split(header, ";")
	mime = strings.ToLower(strings.TrimSpace(params[0]))
	isBase64 = len(params) > 1 && strings.EqualFold(strings.TrimSpace(params[len(params)-1]), "base64")
	return mime, isBase64, payload
}

// sortAttachment sorts a part for a provider that takes only pictureTypes: a
// base64 data URL of one of them, or an https address, is a picture (with its
// type and base64 payload for a data URL); a data URL of anything else but a
// picture is text to write out, an SVG included, since it is text; any other
// picture - another type, a data URL that is not base64 - cannot be sent.
func sortAttachment(ip ImagePart) (kind int, mime, payload string) {
	if !strings.HasPrefix(ip.DataURL, "data:") {
		if strings.HasPrefix(ip.DataURL, "https://") {
			return attachedPicture, "", ""
		}
		return attachedUnsendable, "", ""
	}
	mime, isBase64, payload := parseDataURL(ip.DataURL)
	if !isPictureType(mime) {
		return attachedText, mime, ""
	}
	if !isBase64 || !pictureTypes[mime] {
		return attachedUnsendable, mime, ""
	}
	return attachedPicture, mime, payload
}

// attachmentText is the text a part that is not sent as a picture adds to its
// message: the file written out, or a line naming a picture the provider
// cannot take, so the model is never left to guess what came with a prompt.
func attachmentText(ip ImagePart, kind int, mime string) string {
	label := ip.Name
	if label == "" {
		label = "file"
	}
	if kind == attachedUnsendable {
		if mime == "" {
			mime = "picture"
		}
		return fmt.Sprintf("\n\n[File: %s: a %s this provider cannot be sent as a picture]", label, mime)
	}
	return "\n\n" + fileText(ip, mime)
}

// maxInlinedFileBytes is how much of an attached text file a message writes
// out: about 16K tokens. A longer file - a log a person sent the Telegram
// bot, a dump - is cut there, since one message larger than the context
// window would stay in the history and fail every request after it; the
// session keeps the whole file with its assets, and the prompt says where.
const maxInlinedFileBytes = 64 << 10

// fileText is how an attached file that is not a picture reads in a message:
// its text under a label, its beginning when it is longer than
// maxInlinedFileBytes, or, for a file that is not text - a PDF, an archive -
// a line naming it. Bytes that are not text written out as text are noise
// that fills the context window. A PDF counts as one whatever its bytes,
// since one written in ASCII is still no text a model reads.
func fileText(ip ImagePart, mime string) string {
	label := ip.Name
	if label == "" {
		label = "file"
	}
	body := decodeDataURL(ip.DataURL)
	if mime == "application/pdf" || !looksLikeText(body) {
		if mime == "" {
			mime = "binary"
		}
		return fmt.Sprintf("[File: %s (%s), not text, so it is not written out here]", label, mime)
	}
	if len(body) > maxInlinedFileBytes {
		cut := body[:maxInlinedFileBytes]
		for !utf8.ValidString(cut) {
			cut = cut[:len(cut)-1]
		}
		return fmt.Sprintf("[File: %s, the first %d of its %d bytes]\n%s\n[The rest of %s is not written out here.]", label, len(cut), len(body), cut, label)
	}
	return fmt.Sprintf("[File: %s]\n%s", label, body)
}

// looksLikeText reports whether a file's bytes read as text: valid UTF-8
// with no NUL byte.
func looksLikeText(body string) bool {
	return utf8.ValidString(body) && !strings.ContainsRune(body, 0)
}
