//go:build cli

package markup

import (
	"image"
	"image/color"
	"image/draw"
	"math"

	xdraw "golang.org/x/image/draw"
)

// The window's colours: a dark frame like the macOS editor's whatever the
// terminal's theme, since the picture is what has to stand out.
var (
	colWindow   = color.NRGBA{0x1f, 0x1f, 0x22, 0xff}
	colGroup    = color.NRGBA{0x2e, 0x2e, 0x33, 0xff}
	colSelected = color.NRGBA{0x50, 0x50, 0x58, 0xff}
	colIcon     = color.NRGBA{0xe8, 0xe8, 0xed, 0xff}
	colDisabled = color.NRGBA{0x70, 0x70, 0x76, 0xff}
	colBorder   = color.NRGBA{0x3c, 0x3c, 0x42, 0xff}
	colDone     = color.NRGBA{0x0a, 0x84, 0xff, 0xff}
	colOnDone   = color.NRGBA{0xff, 0xff, 0xff, 0xff}
	colRing     = color.NRGBA{0xff, 0xff, 0xff, 0xff}
	colShade    = color.NRGBA{0x00, 0x00, 0x00, 0x88}
	colCaret    = color.NRGBA{0x0a, 0x84, 0xff, 0xff}
)

// Frame paints the whole window: the picture with the gesture in progress
// on top of it and the toolbar. The image is reused between calls.
func (e *Editor) Frame() *image.RGBA {
	sz := e.lay.Size
	if e.frame == nil || e.frame.Bounds().Size() != sz {
		e.frame = image.NewRGBA(image.Rect(0, 0, sz.X, sz.Y))
	}
	f := e.frame
	draw.Draw(f, f.Bounds(), image.NewUniform(colWindow), image.Point{}, draw.Src)

	v := e.lay.View
	if !v.Empty() {
		strokeRect(f, ptOf(v.Min).sub(Pt{1, 1}), ptOf(v.Max).add(Pt{1, 1}), 1, colBorder)
		draw.Draw(f, v, e.scaledView(), image.Point{}, draw.Src)
		pic := f.SubImage(v).(*image.RGBA)
		toWin := func(s Shape) Shape { return s.transformed(e.lay.ToWindow, e.lay.K) }
		if e.drawing {
			toWin(e.shape).draw(pic)
		}
		if e.typing {
			e.paintLabel(pic, toWin(e.label))
		}
		if e.cropping {
			e.paintCrop(f)
		}
	}
	e.paintToolbar(f)
	return f
}

// scaledView is the picture scaled into the view, cached until either
// changes.
func (e *Editor) scaledView() *image.RGBA {
	v := e.lay.View
	if e.view != nil && e.viewRev == e.doc.Revision() && e.viewFor == v {
		return e.view
	}
	src := e.doc.Image()
	dst := image.NewRGBA(image.Rect(0, 0, v.Dx(), v.Dy()))
	if src.Bounds().Size() == dst.Bounds().Size() {
		copy(dst.Pix, src.Pix)
	} else {
		xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	}
	e.view, e.viewRev, e.viewFor = dst, e.doc.Revision(), v
	return dst
}

func (e *Editor) paintLabel(dst draw.Image, s Shape) {
	at := s.Points[0]
	m := measureText(s.Text, true, s.Size)
	if s.Text != "" {
		s.draw(dst)
	}
	h := m.height
	if h == 0 {
		h = s.Size
	}
	// The caret says where typing goes; a frame around the label says it is
	// still being edited.
	cw := math.Max(2, e.scale*2)
	caretX := at.X + m.width + cw
	fill(dst, []polygon{rectPolyF(Pt{caretX, at.Y}, Pt{caretX + cw, at.Y + h})}, nil, colCaret)
	padding := 4 * e.scale
	strokeRect(dst, Pt{at.X - padding, at.Y - padding}, Pt{caretX + cw + padding, at.Y + h + padding}, math.Max(1, e.scale), colCaret)
}

func (e *Editor) paintCrop(dst *image.RGBA) {
	v := e.lay.View
	a, b := normRect(e.lay.ToWindow(e.cropA), e.lay.ToWindow(e.cropB))
	r := image.Rect(int(a.X), int(a.Y), int(math.Ceil(b.X)), int(math.Ceil(b.Y))).Intersect(v)
	shade := image.NewUniform(colShade)
	for _, part := range []image.Rectangle{
		image.Rect(v.Min.X, v.Min.Y, v.Max.X, r.Min.Y),
		image.Rect(v.Min.X, r.Max.Y, v.Max.X, v.Max.Y),
		image.Rect(v.Min.X, r.Min.Y, r.Min.X, r.Max.Y),
		image.Rect(r.Max.X, r.Min.Y, v.Max.X, r.Max.Y),
	} {
		if !part.Empty() {
			draw.Draw(dst, part, shade, image.Point{}, draw.Over)
		}
	}
	if !r.Empty() {
		strokeRect(dst, ptOf(r.Min), ptOf(r.Max), math.Max(2, 2*e.scale), colRing)
	}
}

func (e *Editor) paintToolbar(dst *image.RGBA) {
	s := e.scale
	fillRoundRect(dst, e.lay.Group, 12*s, colGroup)
	for _, b := range e.lay.Buttons {
		switch b.Control {
		case ControlCancel:
			e.paintAction(dst, b.Rect, labelCancel, false)
		case ControlDone:
			e.paintAction(dst, b.Rect, labelDone, true)
		case ControlColor:
			c := ptOf(b.Rect.Min).add(Pt{float64(b.Rect.Dx()) / 2, float64(b.Rect.Dy()) / 2})
			r := float64(b.Rect.Dx()) / 2
			fillCircle(dst, c, r-2*s, Palette[b.Color])
			if b.Color == len(Palette)-1 {
				// Black would vanish into the bar without an edge.
				strokeCircle(dst, c, r-2*s, math.Max(1, s), colDisabled)
			}
			if b.Color == e.color {
				strokeCircle(dst, c, r+1*s, 2*s, colRing)
			}
		default:
			selected := b.Control == ControlTool && b.Tool == e.tool
			if selected {
				fillRoundRect(dst, b.Rect, 8*s, colSelected)
			}
			ink := colIcon
			if b.Control == ControlUndo && !e.doc.CanUndo() && !e.typing {
				ink = colDisabled
			}
			paintIcon(dst, b, ink, s)
		}
	}
}

func (e *Editor) paintAction(dst *image.RGBA, r image.Rectangle, label string, primary bool) {
	s := e.scale
	ink := colIcon
	if primary {
		fillRoundRect(dst, r, 8*s, colDone)
		ink = colOnDone
	}
	m := measureText(label, true, labelSize*s)
	at := Pt{float64(r.Min.X) + (float64(r.Dx())-m.width)/2, float64(r.Min.Y) + (float64(r.Dy())-m.height)/2}
	drawText(dst, at, label, true, labelSize*s, ink)
}

// paintIcon draws a toolbar glyph centred in the button, on a 24-unit grid
// scaled to the button.
func paintIcon(dst draw.Image, b Button, ink color.Color, scale float64) {
	r := b.Rect
	c := Pt{float64(r.Min.X) + float64(r.Dx())/2, float64(r.Min.Y) + float64(r.Dy())/2}
	u := float64(r.Dx()) / 36
	w := 2 * u
	at := func(x, y float64) Pt { return Pt{c.X + x*u, c.Y + y*u} }
	switch {
	case b.Control == ControlTool && b.Tool == ToolPen:
		// A pencil lying from the bottom left: an outlined body and a solid
		// point.
		hw := 2.8
		body := func(x, y float64) Pt { return at(x, y) }
		n := Pt{hw * 0.7071, hw * 0.7071}
		p0, p1 := body(-4, 4), body(8, -8)
		outline := []Pt{p0.add(n.mul(u)), p1.add(n.mul(u)), p1.sub(n.mul(u)), p0.sub(n.mul(u)), p0.add(n.mul(u))}
		strokeLine(dst, outline, 1.6*u, ink)
		fill(dst, []polygon{{p0.add(n.mul(u)), p0.sub(n.mul(u)), at(-10, 10)}}, nil, ink)
	case b.Control == ControlTool && b.Tool == ToolEllipse:
		strokeEllipse(dst, at(-9, -9), at(9, 9), w, ink)
	case b.Control == ControlTool && b.Tool == ToolRect:
		strokeRect(dst, at(-9, -7), at(9, 7), w, ink)
	case b.Control == ControlTool && b.Tool == ToolArrow:
		drawArrow(dst, at(-8, 8), at(9, -9), w, ink)
	case b.Control == ControlTool && b.Tool == ToolText:
		size := 22 * u
		m := measureText("T", false, size)
		drawText(dst, Pt{c.X - m.width/2, c.Y - m.height/2}, "T", false, size, ink)
	case b.Control == ControlTool && b.Tool == ToolCrop:
		strokeLine(dst, []Pt{at(-5, -11), at(-5, 5), at(11, 5)}, w, ink)
		strokeLine(dst, []Pt{at(-11, -5), at(5, -5), at(5, 11)}, w, ink)
	case b.Control == ControlRotate:
		// A clockwise arc with its head at the top right.
		arc := arcPoints(c, 8*u, math.Pi*0.25, math.Pi*1.75)
		strokeLine(dst, arc, w, ink)
		paintArcHead(dst, arc, u, ink)
	case b.Control == ControlUndo:
		// A curve over the top, its head pointing back down on the left.
		arc := arcPoints(at(1, 3), 8*u, 0, -math.Pi)
		strokeLine(dst, arc, w, ink)
		paintArcHead(dst, arc, u, ink)
	}
}

// paintArcHead puts a filled head at the end of a sampled arc, pointing the
// way the arc runs.
func paintArcHead(dst draw.Image, arc []Pt, u float64, ink color.Color) {
	end := arc[len(arc)-1]
	prev := arc[len(arc)-2]
	d := end.sub(prev)
	l := d.len()
	if l == 0 {
		return
	}
	t := d.mul(1 / l)
	n := Pt{-t.Y, t.X}
	tip := end.add(t.mul(4 * u))
	fill(dst, []polygon{{tip, end.sub(t.mul(1 * u)).add(n.mul(4.5 * u)), end.sub(t.mul(1 * u)).sub(n.mul(4.5 * u))}}, nil, ink)
}
