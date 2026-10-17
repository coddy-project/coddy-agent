//go:build cli

package markup

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
)

// Kind is what a shape draws.
type Kind int

const (
	KindPen Kind = iota
	KindEllipse
	KindRect
	KindArrow
	KindText
)

// Shape is one mark on the picture, in the picture's pixel coordinates at
// the moment it was drawn. Pen keeps its whole path in Points; ellipse,
// rectangle and arrow keep the two corners (or the tail and the tip); text
// keeps its top-left corner.
type Shape struct {
	Kind   Kind
	Color  color.NRGBA
	Width  float64
	Points []Pt
	Text   string
	Size   float64
}

// draw paints the shape onto dst.
func (s Shape) draw(dst draw.Image) {
	switch s.Kind {
	case KindPen:
		strokeLine(dst, s.Points, s.Width, s.Color)
	case KindEllipse:
		if len(s.Points) == 2 {
			strokeEllipse(dst, s.Points[0], s.Points[1], s.Width, s.Color)
		}
	case KindRect:
		if len(s.Points) == 2 {
			strokeRect(dst, s.Points[0], s.Points[1], s.Width, s.Color)
		}
	case KindArrow:
		if len(s.Points) == 2 {
			drawArrow(dst, s.Points[0], s.Points[1], s.Width, s.Color)
		}
	case KindText:
		if len(s.Points) == 1 && s.Text != "" {
			drawText(dst, s.Points[0], s.Text, true, s.Size, s.Color)
		}
	}
}

// transformed maps every point of the shape through f and scales its
// widths by k.
func (s Shape) transformed(f func(Pt) Pt, k float64) Shape {
	out := s
	out.Points = make([]Pt, len(s.Points))
	for i, p := range s.Points {
		out.Points[i] = f(p)
	}
	out.Width *= k
	out.Size *= k
	return out
}

type opKind int

const (
	opShape opKind = iota
	opCrop
	opRotate
)

// op is one step of the history: a shape, a crop or a quarter turn.
type op struct {
	kind  opKind
	shape Shape
	crop  image.Rectangle
}

// Document is the picture being marked up and everything done to it, in
// order. Undo drops the last step; the picture is the original replayed
// through what is left, so nothing is ever lost to a crop that is undone.
type Document struct {
	base *image.RGBA
	ops  []op

	rendered   *image.RGBA
	renderedOf int // len(ops) the render reflects; -1 when stale
	rev        int // bumps on every change, for the caches of a view
}

// NewDocument copies img into a document whose origin is (0, 0).
func NewDocument(img image.Image) *Document {
	b := img.Bounds()
	base := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(base, base.Bounds(), img, b.Min, draw.Src)
	return &Document{base: base, renderedOf: -1}
}

// Size is the size of the picture as it stands, after crops and turns.
func (d *Document) Size() image.Point {
	sz := d.base.Bounds().Size()
	for _, o := range d.ops {
		switch o.kind {
		case opCrop:
			sz = o.crop.Size()
		case opRotate:
			sz = image.Pt(sz.Y, sz.X)
		}
	}
	return sz
}

// Edited reports whether anything was done to the picture.
func (d *Document) Edited() bool { return len(d.ops) > 0 }

// CanUndo reports whether there is a step to take back.
func (d *Document) CanUndo() bool { return len(d.ops) > 0 }

// Revision changes whenever the picture does.
func (d *Document) Revision() int { return d.rev }

// AddShape puts a mark on the picture.
func (d *Document) AddShape(s Shape) {
	d.push(op{kind: opShape, shape: s})
}

// Crop keeps the part of the picture inside r. A rectangle that does not
// overlap the picture by at least 2x2 pixels, or keeps all of it, does
// nothing and reports false.
func (d *Document) Crop(r image.Rectangle) bool {
	sz := d.Size()
	r = r.Canon().Intersect(image.Rect(0, 0, sz.X, sz.Y))
	if r.Dx() < 2 || r.Dy() < 2 || r == image.Rect(0, 0, sz.X, sz.Y) {
		return false
	}
	d.push(op{kind: opCrop, crop: r})
	return true
}

// Rotate turns the picture a quarter clockwise.
func (d *Document) Rotate() {
	d.push(op{kind: opRotate})
}

// Undo takes back the last step and reports whether there was one.
func (d *Document) Undo() bool {
	if len(d.ops) == 0 {
		return false
	}
	d.ops = d.ops[:len(d.ops)-1]
	d.renderedOf = -1
	d.rev++
	return true
}

func (d *Document) push(o op) {
	d.ops = append(d.ops, o)
	d.rev++
	if d.rendered != nil && d.renderedOf == len(d.ops)-1 && o.kind == opShape {
		// A shape on top of an up-to-date render is drawn in place instead of
		// replaying the history.
		o.shape.draw(d.rendered)
		d.renderedOf = len(d.ops)
		return
	}
	d.renderedOf = -1
}

// Image is the picture with every step applied. The result is owned by the
// document and changes with it; copy it to keep it.
func (d *Document) Image() *image.RGBA {
	if d.rendered != nil && d.renderedOf == len(d.ops) {
		return d.rendered
	}
	img := image.NewRGBA(d.base.Bounds())
	copy(img.Pix, d.base.Pix)
	for _, o := range d.ops {
		switch o.kind {
		case opShape:
			o.shape.draw(img)
		case opCrop:
			img = cropRGBA(img, o.crop)
		case opRotate:
			img = rotateRGBA(img)
		}
	}
	d.rendered, d.renderedOf = img, len(d.ops)
	return img
}

// PNG encodes the picture as it stands.
func (d *Document) PNG() ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, d.Image()); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func cropRGBA(src *image.RGBA, r image.Rectangle) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	draw.Draw(out, out.Bounds(), src, r.Min, draw.Src)
	return out
}

// rotateRGBA turns src a quarter clockwise: the pixel at (x, y) moves to
// (h-1-y, x).
func rotateRGBA(src *image.RGBA) *image.RGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	out := image.NewRGBA(image.Rect(0, 0, h, w))
	for y := 0; y < h; y++ {
		row := src.Pix[y*src.Stride : y*src.Stride+w*4]
		nx := h - 1 - y
		for x := 0; x < w; x++ {
			o := x*out.Stride + nx*4
			copy(out.Pix[o:o+4], row[x*4:x*4+4])
		}
	}
	return out
}

// strokeWidth is the line width that reads well on a picture of this size.
func strokeWidth(sz image.Point) float64 {
	return clamp(float64(max(sz.X, sz.Y))/240, 3, 14)
}

// textSize is the font size of a label on a picture of this size.
func textSize(sz image.Point) float64 {
	return clamp(float64(max(sz.X, sz.Y))/36, 16, 96)
}

func clamp(v, lo, hi float64) float64 {
	return math.Max(lo, math.Min(hi, v))
}
