package fs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/gif"
	_ "image/jpeg" // image.DecodeConfig reads the size of a JPEG
	"image/png"
	"io"
	"os"
	"path/filepath"

	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// readImageFormats are the pictures read hands the model, by the type their
// content sniffs as, never by their name: the formats every provider Coddy
// talks to takes as an image.
var readImageFormats = map[string]string{
	"image/png":  "PNG",
	"image/jpeg": "JPEG",
	"image/gif":  "GIF",
	"image/webp": "WebP",
}

const (
	// readImageMaxBytes bounds one picture: 3.75 MiB is 5 MiB once base64
	// encoded, the most the Anthropic API takes per image. The picture stays
	// in the history the session replays to whichever provider it switches to,
	// so the strictest provider's limit is the one that holds.
	readImageMaxBytes = 3*1024*1024 + 768*1024
	// readImageMaxSide is the widest and tallest picture a provider takes:
	// the Anthropic API refuses one over 8000 pixels a side.
	readImageMaxSide = 8000
	// gifFrameMaxPixels bounds the first frame of a GIF read decodes to send
	// it as a PNG: 16 megapixels, 16 MB as a palette image.
	gifFrameMaxPixels = 16_000_000
)

// PrepareImageForModel validates a tool-owned picture against the same limits
// as read, then converts a GIF to its first frame as PNG. A rejected picture
// never enters the session history, where it would break later model calls.
func PrepareImageForModel(data []byte, mimeType string) ([]byte, string, error) {
	if readImageFormats[mimeType] == "" || sniffKind(data) != mimeType {
		return nil, "", fmt.Errorf("unsupported or mislabeled image type %q", mimeType)
	}
	if len(data) > readImageMaxBytes {
		return nil, "", fmt.Errorf("image exceeds the %d byte model limit", readImageMaxBytes)
	}
	w, h, err := imageSize(data, mimeType)
	if err == nil {
		err = imageComplete(data, mimeType)
	}
	if err != nil {
		return nil, "", fmt.Errorf("image cannot be decoded: %w", err)
	}
	if w > readImageMaxSide || h > readImageMaxSide {
		return nil, "", fmt.Errorf("image exceeds %d pixels on a side", readImageMaxSide)
	}
	if mimeType == "image/gif" {
		if int64(w)*int64(h) > gifFrameMaxPixels {
			return nil, "", fmt.Errorf("GIF frame exceeds %d pixels", gifFrameMaxPixels)
		}
		frame, err := gifFirstFrame(data)
		if err != nil {
			return nil, "", fmt.Errorf("GIF frame cannot be decoded: %w", err)
		}
		if len(frame) > readImageMaxBytes {
			return nil, "", fmt.Errorf("GIF frame exceeds the %d byte model limit", readImageMaxBytes)
		}
		return frame, "image/png", nil
	}
	return data, mimeType, nil
}

// readImage answers read for a file whose content sniffs as a picture: it
// hands the picture to the model through env.AttachImage and says what it is.
// A picture no provider would take is refused before it reaches the history,
// where a rejected image would fail every later request of the session. With
// no agent to take the picture the file is refused as binary, as read always
// refused it.
func readImage(argPath, path string, data []byte, kind string, env *tooling.Env) (string, error) {
	if env == nil || env.AttachImage == nil {
		return "", fmt.Errorf("read: %s %w (%s, %d bytes); read shows text files only", argPath, errBinaryFile, kind, len(data))
	}
	if err := imageRefused(argPath, env); err != nil {
		return "", err
	}
	format := readImageFormats[kind]
	if len(data) > readImageMaxBytes {
		return "", oversizedImage(argPath, kind, int64(len(data)), env)
	}
	w, h, err := imageSize(data, kind)
	if err == nil {
		err = imageComplete(data, kind)
	}
	if isStructureFault(err) {
		return "", fmt.Errorf("read: %s %w", argPath, err)
	}
	if err != nil {
		return "", fmt.Errorf("read: %s looks like a %s image but cannot be decoded: %v", argPath, format, err)
	}
	if w > readImageMaxSide || h > readImageMaxSide {
		return "", fmt.Errorf("read: %s is a %s image of %dx%d, larger than the %d pixels a side a model takes; save a scaled-down copy and read that",
			argPath, format, w, h, readImageMaxSide)
	}

	sent, sentType, note := data, kind, ""
	if kind == "image/gif" && int64(w)*int64(h) > gifFrameMaxPixels {
		// A GIF is small on disk for any canvas, and its first frame is
		// decoded to be sent: the size decides before that decode.
		return "", fmt.Errorf("read: %s is a GIF image of %dx%d, more than the %d megapixels read decodes a frame of; save a smaller copy and read that",
			argPath, w, h, gifFrameMaxPixels/1_000_000)
	}
	if kind == "image/gif" {
		// Not every provider takes an animated GIF, and telling an animated one
		// from a still one means decoding every frame, which a small file of
		// many large frames turns into gigabytes. The first frame alone, as a
		// PNG, is a picture every provider takes.
		frame, err := gifFirstFrame(data)
		if err != nil {
			return "", fmt.Errorf("read: %s looks like a GIF image but cannot be decoded: %v", argPath, err)
		}
		if len(frame) > readImageMaxBytes {
			return "", fmt.Errorf("read: the first frame of %s is %s as a PNG, more than the %s one picture may take; save a scaled-down copy and read that",
				argPath, formatBytes(len(frame)), formatBytes(readImageMaxBytes))
		}
		sent, sentType, note = frame, "image/png", "; a GIF is shown to you as its first frame"
	}
	if err := env.AttachImage(filepath.Base(path), sentType, sent); err != nil {
		return "", fmt.Errorf("read: %s: %w", argPath, err)
	}
	return fmt.Sprintf("%s: %s image, %dx%d, %s%s. The picture is attached for you to look at.",
		argPath, format, w, h, formatBytes(len(data)), note), nil
}

// imageRefused is the session's refusal of any picture (Env.ImageRefusal),
// naming the file, or nil.
func imageRefused(argPath string, env *tooling.Env) error {
	if env == nil || env.ImageRefusal == nil {
		return nil
	}
	if err := env.ImageRefusal(); err != nil {
		return fmt.Errorf("read: %s: %w", argPath, err)
	}
	return nil
}

// oversizedImage is the refusal of a picture larger than readImageMaxBytes,
// or the old binary refusal where no agent takes pictures at all, or the
// session's refusal of any picture, which comes first.
func oversizedImage(argPath, kind string, size int64, env *tooling.Env) error {
	if env == nil || env.AttachImage == nil {
		return fmt.Errorf("read: %s %w (%s, %d bytes); read shows text files only", argPath, errBinaryFile, kind, size)
	}
	if err := imageRefused(argPath, env); err != nil {
		return err
	}
	return fmt.Errorf("read: %s is a %s image of %s, more than the %s one picture may take; save a scaled-down copy and read that",
		argPath, readImageFormats[kind], formatBytes(int(size)), formatBytes(readImageMaxBytes))
}

// sniffFileHead is sniffKind over the first bytes of a file, all the content
// sniffer looks at.
func sniffFileHead(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	return sniffKind(head[:n])
}

// A picture that is not all there - a screenshot still being written, a
// download cut short - would go into the history and fail every later request
// of the session, so its structure is walked to the end before it is taken.
var (
	errImageCut     = errors.New("ends before its image does: the file may still be written, read it again once it is complete")
	errPNGNoImage   = errors.New("is a PNG file with no image data")
	errPNGDamaged   = errors.New("is a damaged PNG file: a chunk of it does not match its checksum")
	errJPEGNoImage  = errors.New("is a JPEG file with no image data")
	errWebPNoImage  = errors.New("is a WebP file with no image data")
	errWebPAnimated = errors.New("is an animated WebP, which not every provider takes; save a frame of it as a PNG and read that")
)

// isStructureFault reports whether err is one of the refusals above, which
// read words itself, rather than a decoder's error.
func isStructureFault(err error) bool {
	for _, fault := range []error{errImageCut, errPNGNoImage, errPNGDamaged, errJPEGNoImage, errWebPNoImage, errWebPAnimated} {
		if errors.Is(err, fault) {
			return true
		}
	}
	return false
}

// imageSize reads a picture's width and height from its header, without
// decoding its pixels.
func imageSize(data []byte, kind string) (int, int, error) {
	if kind == "image/webp" {
		return webpInfo(data)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

// imageComplete walks a PNG to its IEND chunk and a JPEG to its end-of-image
// marker; webpInfo walks a WebP, and a GIF goes as its first frame, which
// decodes whole or not at all.
func imageComplete(data []byte, kind string) error {
	switch kind {
	case "image/png":
		return pngComplete(data)
	case "image/jpeg":
		return jpegComplete(data)
	}
	return nil
}

// pngComplete follows the chunks after the signature to IEND, checking each
// chunk against its checksum and that image data came before the end. A
// damaged chunk, image data or not, is refused: Go's decoder refuses any,
// libpng any critical one, Pillow any other than the image data.
func pngComplete(data []byte) error {
	sawImage := false
	for off := 8; off+8 <= len(data); {
		n := int(binary.BigEndian.Uint32(data[off : off+4]))
		if n < 0 || n > len(data)-off-12 {
			return errImageCut
		}
		end := off + 12 + n
		if crc32.ChecksumIEEE(data[off+4:end-4]) != binary.BigEndian.Uint32(data[end-4:end]) {
			return errPNGDamaged
		}
		switch string(data[off+4 : off+8]) {
		case "IDAT":
			sawImage = true
		case "IEND":
			if !sawImage {
				return errPNGNoImage
			}
			return nil
		}
		off = end
	}
	return errImageCut
}

// jpegComplete follows the segments of a JPEG to its end-of-image marker,
// skipping the entropy-coded data after each start of scan and any stray byte
// before a marker, and wants at least one scan before the end: a JPEG with
// none holds no picture.
func jpegComplete(data []byte) error {
	sawScan := false
	for i := 2; i+1 < len(data); {
		if data[i] != 0xFF {
			// A stray byte between segments: libjpeg skips to the next
			// marker with a warning, and Go's decoder after it.
			i++
			continue
		}
		m := data[i+1]
		switch {
		case m == 0xFF: // fill byte
			i++
			continue
		case m == 0xD9: // EOI
			if !sawScan {
				return errJPEGNoImage
			}
			return nil
		case m == 0x01 || (m >= 0xD0 && m <= 0xD7): // TEM, RSTn: no length
			i += 2
			continue
		}
		if i+4 > len(data) {
			return errImageCut
		}
		i += 2 + (int(data[i+2])<<8 | int(data[i+3]))
		if m != 0xDA { // not SOS
			continue
		}
		sawScan = true
		for i+1 < len(data) && (data[i] != 0xFF || data[i+1] == 0x00 || (data[i+1] >= 0xD0 && data[i+1] <= 0xD7)) {
			i++
		}
	}
	return errImageCut
}

// webpInfo walks the chunks of a WebP, which the standard library has no
// decoder for: it reads the canvas size (from VP8X, else from the VP8 or VP8L
// image chunk), requires that image chunk to be there, and refuses an
// animation and a chunk that runs past the end of the file. A RIFF size larger
// than what follows the header is refused too, the whole file's length or a
// zero written there included: libwebp does not decode such a file.
func webpInfo(data []byte) (int, int, error) {
	if len(data) < 12 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WEBP" {
		return 0, 0, errors.New("no WebP header")
	}
	riff := int(binary.LittleEndian.Uint32(data[4:8]))
	if riff < 4 || riff > len(data)-8 {
		return 0, 0, errImageCut
	}
	data = data[:8+riff]
	w, h, hasImage := 0, 0, false
	for off := 12; off < len(data); {
		if off+8 > len(data) {
			return 0, 0, errImageCut
		}
		size := int(binary.LittleEndian.Uint32(data[off+4 : off+8]))
		body := off + 8
		if size < 0 || size > len(data)-body {
			return 0, 0, errImageCut
		}
		payload := data[body : body+size]
		switch string(data[off : off+4]) {
		case "VP8X":
			if len(payload) < 10 {
				return 0, 0, errors.New("a VP8X chunk too short")
			}
			if payload[0]&0x02 != 0 {
				return 0, 0, errWebPAnimated
			}
			w = 1 + (int(payload[4]) | int(payload[5])<<8 | int(payload[6])<<16)
			h = 1 + (int(payload[7]) | int(payload[8])<<8 | int(payload[9])<<16)
		case "ANIM", "ANMF":
			return 0, 0, errWebPAnimated
		case "VP8 ":
			if len(payload) < 10 || payload[3] != 0x9d || payload[4] != 0x01 || payload[5] != 0x2a {
				return 0, 0, errors.New("no VP8 key frame")
			}
			if w == 0 {
				w = int(binary.LittleEndian.Uint16(payload[6:8]) & 0x3fff)
				h = int(binary.LittleEndian.Uint16(payload[8:10]) & 0x3fff)
			}
			hasImage = true
		case "VP8L":
			if len(payload) < 5 || payload[0] != 0x2f {
				return 0, 0, errors.New("no VP8L signature")
			}
			if w == 0 {
				bits := binary.LittleEndian.Uint32(payload[1:5])
				w, h = int(bits&0x3fff)+1, int(bits>>14&0x3fff)+1
			}
			hasImage = true
		}
		off = body + size + size%2
	}
	if !hasImage {
		return 0, 0, errWebPNoImage
	}
	return w, h, nil
}

// gifFirstFrame returns the first frame of a GIF as a PNG. gif.Decode stops
// after that frame, so the rest of an animation is never decoded, and the
// frame is encoded as the palette image it decodes to, one byte a pixel, with
// no RGBA copy of it; gifFrameMaxPixels bounds it before the decode.
func gifFirstFrame(data []byte) ([]byte, error) {
	frame, err := gif.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, frame); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// formatBytes spells a file size the way a person reads it.
func formatBytes(n int) string {
	switch {
	case n >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	case n >= 1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%d bytes", n)
}
