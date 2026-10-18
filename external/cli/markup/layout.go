//go:build cli

package markup

import (
	"image"
	"image/color"
	"math"
)

// Tool is what a drag on the picture does.
type Tool int

const (
	ToolPen Tool = iota
	ToolEllipse
	ToolRect
	ToolArrow
	ToolText
	ToolCrop
)

// Palette is the choice of colours, the macOS markup set: red, orange,
// yellow, green, blue, white and black.
var Palette = []color.NRGBA{
	{0xff, 0x3b, 0x30, 0xff},
	{0xff, 0x95, 0x00, 0xff},
	{0xff, 0xcc, 0x00, 0xff},
	{0x34, 0xc7, 0x59, 0xff},
	{0x0a, 0x84, 0xff, 0xff},
	{0xff, 0xff, 0xff, 0xff},
	{0x00, 0x00, 0x00, 0xff},
}

// Control names a button of the toolbar.
type Control int

const (
	ControlNone Control = iota
	ControlTool
	ControlRotate
	ControlUndo
	ControlColor
	ControlCancel
	ControlDone
)

// Button is one control of the toolbar and where it sits in the window.
type Button struct {
	Control Control
	Tool    Tool // for ControlTool
	Color   int  // for ControlColor
	Rect    image.Rectangle
}

// Layout is where everything goes in a window of a given size: the toolbar
// along the bottom - Cancel on the left, the tools and the palette in a
// group in the middle, Done on the right - and the picture fitted into the
// rest, never enlarged past the display scale.
type Layout struct {
	Size    image.Point
	Scale   float64
	Bar     image.Rectangle
	Group   image.Rectangle
	Buttons []Button
	// Area is where the picture may go, View is where it goes, and K is how
	// many window pixels one picture pixel takes.
	Area image.Rectangle
	View image.Rectangle
	K    float64
}

// metrics of the toolbar at scale 1, in window pixels.
const (
	barHeight    = 64
	pad          = 16
	buttonSize   = 36
	buttonGap    = 4
	groupGap     = 14
	swatchSize   = 22
	swatchGap    = 8
	groupPadding = 10
	actionHeight = 34
	actionPadX   = 16
	labelSize    = 15
)

// toolOrder is the left-to-right order of the drawing tools; crop opens
// the second group, followed by rotate and undo.
var toolOrder = []Tool{ToolPen, ToolEllipse, ToolRect, ToolArrow, ToolText}

func px(v, scale float64) int { return int(math.Round(v * scale)) }

func actionWidth(label string, scale float64) int {
	return int(math.Ceil(measureText(label, true, labelSize*scale).width)) + 2*px(actionPadX, scale)
}

func groupWidth(scale float64) int {
	b, g := px(buttonSize, scale), px(buttonGap, scale)
	return 2*px(groupPadding, scale) + 8*b + 6*g + 2*px(groupGap, scale) +
		len(Palette)*px(swatchSize, scale) + (len(Palette)-1)*px(swatchGap, scale)
}

// MinSize is the smallest window the toolbar fits in.
func MinSize(scale float64) image.Point {
	w := groupWidth(scale) + actionWidth(labelCancel, scale) + actionWidth(labelDone, scale) + 4*px(pad, scale)
	return image.Pt(w, px(barHeight, scale)+px(160, scale))
}

// PreferredSize is the window a picture of size img opens in on a screen of
// the given size: the picture at its natural size where it fits in most of
// the screen, smaller where it does not, and never narrower than the
// toolbar.
func PreferredSize(img, screen image.Point, scale float64) image.Point {
	minSz := MinSize(scale)
	maxW, maxH := float64(screen.X)*0.85, float64(screen.Y)*0.85
	p, bar := float64(px(pad, scale)), float64(px(barHeight, scale))
	k := fitScale(img, maxW-2*p, maxH-bar-2*p, scale)
	w := int(math.Ceil(float64(img.X)*k + 2*p))
	h := int(math.Ceil(float64(img.Y)*k + 2*p + bar))
	w = max(w, minSz.X)
	h = max(h, minSz.Y)
	if screen.X > 0 {
		w = min(w, screen.X)
	}
	if screen.Y > 0 {
		h = min(h, screen.Y)
	}
	return image.Pt(w, h)
}

func fitScale(img image.Point, w, h, limit float64) float64 {
	if img.X <= 0 || img.Y <= 0 || w <= 0 || h <= 0 {
		return 1
	}
	return math.Min(limit, math.Min(w/float64(img.X), h/float64(img.Y)))
}

// NewLayout places the toolbar and the picture in a window of size sz.
func NewLayout(sz, img image.Point, scale float64) Layout {
	if scale <= 0 {
		scale = 1
	}
	l := Layout{Size: sz, Scale: scale}
	barH := px(barHeight, scale)
	l.Bar = image.Rect(0, sz.Y-barH, sz.X, sz.Y)
	p := px(pad, scale)
	l.Area = image.Rect(p, p, sz.X-p, sz.Y-barH)
	if l.Area.Dx() < 1 || l.Area.Dy() < 1 {
		l.Area = image.Rect(0, 0, max(sz.X, 1), max(sz.Y-barH, 1))
	}
	l.K = fitScale(img, float64(l.Area.Dx()), float64(l.Area.Dy()), scale)
	vw, vh := int(math.Round(float64(img.X)*l.K)), int(math.Round(float64(img.Y)*l.K))
	vx := l.Area.Min.X + (l.Area.Dx()-vw)/2
	vy := l.Area.Min.Y + (l.Area.Dy()-vh)/2
	l.View = image.Rect(vx, vy, vx+vw, vy+vh)

	midY := l.Bar.Min.Y + barH/2
	ah := px(actionHeight, scale)
	cw := actionWidth(labelCancel, scale)
	dw := actionWidth(labelDone, scale)
	l.Buttons = append(l.Buttons,
		Button{Control: ControlCancel, Rect: image.Rect(p, midY-ah/2, p+cw, midY-ah/2+ah)},
		Button{Control: ControlDone, Rect: image.Rect(sz.X-p-dw, midY-ah/2, sz.X-p, midY-ah/2+ah)},
	)

	gw := groupWidth(scale)
	b := px(buttonSize, scale)
	gh := b + 2*px(4, scale)
	gx := (sz.X - gw) / 2
	l.Group = image.Rect(gx, midY-gh/2, gx+gw, midY-gh/2+gh)
	x := gx + px(groupPadding, scale)
	top := midY - b/2
	add := func(btn Button, w int) {
		btn.Rect = image.Rect(x, top, x+w, top+b)
		l.Buttons = append(l.Buttons, btn)
		x += w
	}
	for i, t := range toolOrder {
		if i > 0 {
			x += px(buttonGap, scale)
		}
		add(Button{Control: ControlTool, Tool: t}, b)
	}
	x += px(groupGap, scale)
	add(Button{Control: ControlTool, Tool: ToolCrop}, b)
	x += px(buttonGap, scale)
	add(Button{Control: ControlRotate}, b)
	x += px(buttonGap, scale)
	add(Button{Control: ControlUndo}, b)
	x += px(groupGap, scale)
	sw := px(swatchSize, scale)
	for i := range Palette {
		if i > 0 {
			x += px(swatchGap, scale)
		}
		// A swatch is hit in the full height of the bar's buttons, wider
		// than the dot it shows.
		add(Button{Control: ControlColor, Color: i}, sw)
	}
	return l
}

// ButtonAt is the control under p, if any.
func (l Layout) ButtonAt(p image.Point) (Button, bool) {
	for _, b := range l.Buttons {
		if p.In(b.Rect) {
			return b, true
		}
	}
	return Button{}, false
}

// ToPicture maps a window point to picture coordinates.
func (l Layout) ToPicture(p image.Point) Pt {
	return Pt{(float64(p.X) - float64(l.View.Min.X)) / l.K, (float64(p.Y) - float64(l.View.Min.Y)) / l.K}
}

// ToWindow maps a picture point to window coordinates.
func (l Layout) ToWindow(p Pt) Pt {
	return Pt{float64(l.View.Min.X) + p.X*l.K, float64(l.View.Min.Y) + p.Y*l.K}
}

// Button finds the first control of a kind (and tool or colour).
func (l Layout) Button(c Control, tool Tool, colorIndex int) (Button, bool) {
	for _, b := range l.Buttons {
		if b.Control != c {
			continue
		}
		if c == ControlTool && b.Tool != tool {
			continue
		}
		if c == ControlColor && b.Color != colorIndex {
			continue
		}
		return b, true
	}
	return Button{}, false
}

const (
	labelCancel = "Cancel"
	labelDone   = "Done"
)
