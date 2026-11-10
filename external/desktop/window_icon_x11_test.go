//go:build desktop && linux && !android

package desktop

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// What goes into _NET_WM_ICON. The property write itself needs an X server
// with a window manager on it; the arithmetic is held here.

func pngOf(t *testing.T, w, h int, c color.NRGBA) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestTheWindowIconCarriesEverySizeSmallestFirst(t *testing.T) {
	small := pngOf(t, 2, 2, color.NRGBA{R: 1, G: 2, B: 3, A: 4})
	large := pngOf(t, 4, 4, color.NRGBA{R: 5, G: 6, B: 7, A: 8})
	data, err := netWMIcon(small, large)
	if err != nil {
		t.Fatal(err)
	}
	// Two headers of two words each, plus 2x2 and 4x4 pixels.
	if want := (2 + 4 + 2 + 16) * 4; len(data) != want {
		t.Fatalf("the icon is %d bytes, wanted %d", len(data), want)
	}
	read := func(i int) uint32 { return binary.NativeEndian.Uint32(data[i*4:]) }
	if read(0) != 2 || read(1) != 2 {
		t.Fatalf("the first icon says it is %dx%d", read(0), read(1))
	}
	// Each pixel is one ARGB word, alpha in the top byte.
	if got := read(2); got != 0x04010203 {
		t.Fatalf("the first pixel is %#08x, wanted ARGB", got)
	}
	if read(6) != 4 || read(7) != 4 {
		t.Fatalf("the second icon says it is %dx%d", read(6), read(7))
	}
}

// The property travels in one X request, and a request has a maximum length.
// A 256-pixel icon is 262 152 bytes, which the server answers with BadLength -
// and the connection it answered on is no use afterwards, so the frame stays
// on the window and the chords are never grabbed. The sizes the window is
// actually given have to leave room for the request around them.
func TestTheWindowIconFitsInOneRequestOfASmallServer(t *testing.T) {
	data, err := netWMIcon(iconSmallPNG, iconPNG)
	if err != nil {
		t.Fatal(err)
	}
	// The smallest maximum an X server may report, in bytes: 4 096 four-byte
	// words is what the protocol guarantees, and every server in practice
	// allows 256 KiB. The icon is held to the practical one with room for the
	// request's own header.
	const practicalMax = 262140
	if len(data)+32 > practicalMax {
		t.Fatalf("the window icon is %d bytes, which does not fit one request", len(data))
	}
}

func TestAnIconThatIsNotAPNGIsRefused(t *testing.T) {
	if _, err := netWMIcon([]byte("not a png")); err == nil {
		t.Fatal("a window was given something that is not an icon")
	}
	if _, err := netWMIcon(); err == nil {
		t.Fatal("a window was given no icon at all and nothing said so")
	}
}
