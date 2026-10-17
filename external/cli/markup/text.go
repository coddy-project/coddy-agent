//go:build cli

package markup

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// The Go fonts cover WGL4, Cyrillic included, so a label typed in Russian
// renders as typed.
var (
	fontsOnce sync.Once
	boldFont  *opentype.Font
	plainFont *opentype.Font

	facesMu sync.Mutex
	faces   = map[faceKey]font.Face{}
)

type faceKey struct {
	bold bool
	size int // tenths of a pixel
}

func loadFonts() {
	fontsOnce.Do(func() {
		// Both are embedded byte slices of known-good TrueType files: a parse
		// error would be a broken build, not a runtime condition.
		boldFont, _ = opentype.Parse(gobold.TTF)
		plainFont, _ = opentype.Parse(goregular.TTF)
	})
}

// withFace runs fn with a cached face of the given pixel size. A face keeps
// scratch buffers and is not safe for concurrent use, so fn runs under the
// cache lock.
func withFace(bold bool, size float64, fn func(font.Face)) {
	loadFonts()
	key := faceKey{bold: bold, size: int(math.Round(size * 10))}
	facesMu.Lock()
	defer facesMu.Unlock()
	f, ok := faces[key]
	if !ok {
		src := plainFont
		if bold {
			src = boldFont
		}
		var err error
		f, err = opentype.NewFace(src, &opentype.FaceOptions{Size: float64(key.size) / 10, DPI: 72, Hinting: font.HintingNone})
		if err != nil {
			return
		}
		faces[key] = f
	}
	fn(f)
}

// textMetrics is the box a line of text occupies when its top-left corner is
// the origin.
type textMetrics struct {
	width, ascent, height float64
}

func metricsOf(f font.Face, s string) textMetrics {
	m := f.Metrics()
	return textMetrics{
		width:  fixedToFloat(font.MeasureString(f, s)),
		ascent: fixedToFloat(m.Ascent),
		height: fixedToFloat(m.Ascent + m.Descent),
	}
}

func measureText(s string, bold bool, size float64) (m textMetrics) {
	withFace(bold, size, func(f font.Face) { m = metricsOf(f, s) })
	return m
}

// drawText paints s with its top-left corner at origin.
func drawText(dst draw.Image, origin Pt, s string, bold bool, size float64, c color.Color) (m textMetrics) {
	withFace(bold, size, func(f font.Face) {
		m = metricsOf(f, s)
		d := &font.Drawer{
			Dst:  dst,
			Src:  image.NewUniform(c),
			Face: f,
			Dot:  fixed.Point26_6{X: floatToFixed(origin.X), Y: floatToFixed(origin.Y + m.ascent)},
		}
		d.DrawString(s)
	})
	return m
}

func fixedToFloat(v fixed.Int26_6) float64 { return float64(v) / 64 }
func floatToFixed(v float64) fixed.Int26_6 { return fixed.Int26_6(math.Round(v * 64)) }
