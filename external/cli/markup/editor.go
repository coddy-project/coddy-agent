//go:build cli

package markup

import (
	"image"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Result is what an event asks of the window.
type Result int

const (
	// Continue keeps the window open.
	Continue Result = iota
	// Done closes the window and keeps the picture.
	Done
	// Cancel closes the window and drops it.
	Cancel
)

// Key is a key press as the editor needs it: a typed character, or one of
// the named keys "enter", "escape", "backspace", with ctrl held or not.
type Key struct {
	Rune rune
	Name string
	Ctrl bool
	// Base is what the key types in the first keyboard layout with neither
	// shift nor caps lock, 0 when the window cannot tell. The shortcuts read
	// it, so p, r or ctrl+z work in a Russian layout and with caps lock on.
	Base rune
}

// shortcut is the letter a key stands for as a shortcut.
func (k Key) shortcut() rune {
	if k.Base != 0 {
		return unicode.ToLower(k.Base)
	}
	return unicode.ToLower(k.Rune)
}

// Editor is the markup window's state and behaviour without the window: the
// document, the chosen tool and colour, the gesture in progress and the
// label being typed. A window feeds it pointer and key events in window
// coordinates and paints Frame whenever an event says the picture changed.
type Editor struct {
	doc   *Document
	tool  Tool
	color int
	scale float64
	lay   Layout

	pressed  *Button // toolbar control under the pointer since press
	drawing  bool
	shape    Shape // the shape being dragged, in picture coordinates
	cropping bool
	cropA    Pt
	cropB    Pt

	typing bool
	label  Shape // the label being typed

	view    *image.RGBA // the picture scaled into Layout.View
	viewRev int
	viewFor image.Rectangle
	frame   *image.RGBA
}

// NewEditor opens img for markup at the given display scale (1 for 96 dpi).
// The pen and red are chosen, as in the macOS editor.
func NewEditor(img image.Image, scale float64) *Editor {
	if scale <= 0 {
		scale = 1
	}
	e := &Editor{doc: NewDocument(img), scale: scale, viewRev: -1}
	e.Resize(PreferredSize(e.doc.Size(), image.Pt(1<<14, 1<<14), scale))
	return e
}

// Document is the picture being edited.
func (e *Editor) Document() *Document { return e.doc }

// Tool is the chosen tool.
func (e *Editor) Tool() Tool { return e.tool }

// Color is the index of the chosen colour in Palette.
func (e *Editor) Color() int { return e.color }

// Layout is where everything sits in the window now.
func (e *Editor) Layout() Layout { return e.lay }

// Typing reports whether a label is being typed.
func (e *Editor) Typing() bool { return e.typing }

// Resize lays the window out again for a new size.
func (e *Editor) Resize(sz image.Point) {
	e.lay = NewLayout(sz, e.doc.Size(), e.scale)
}

// Finish commits a label being typed, so the document holds everything the
// window showed. Done does it; a window that closes on its own calls it
// before reading the picture.
func (e *Editor) Finish() {
	e.commitLabel()
}

// PointerDown starts a gesture: a press on a control arms it, a press on the
// picture starts a shape, a crop or a label.
func (e *Editor) PointerDown(p image.Point) Result {
	if b, ok := e.lay.ButtonAt(p); ok {
		e.pressed = &b
		return Continue
	}
	e.pressed = nil
	if e.typing {
		e.commitLabel()
	}
	if !p.In(e.lay.Area) && !p.In(e.lay.View) {
		return Continue
	}
	q := e.lay.ToPicture(p)
	sz := e.doc.Size()
	switch e.tool {
	case ToolText:
		if !q.in(image.Rect(0, 0, sz.X, sz.Y)) {
			return Continue
		}
		e.typing = true
		e.label = Shape{Kind: KindText, Color: Palette[e.color], Points: []Pt{q}, Size: textSize(sz)}
	case ToolCrop:
		e.cropping = true
		e.cropA, e.cropB = q, q
	default:
		e.drawing = true
		e.shape = Shape{Kind: kindOf(e.tool), Color: Palette[e.color], Width: strokeWidth(sz), Points: []Pt{q}}
		if e.tool != ToolPen {
			e.shape.Points = append(e.shape.Points, q)
		}
	}
	return Continue
}

func kindOf(t Tool) Kind {
	switch t {
	case ToolEllipse:
		return KindEllipse
	case ToolRect:
		return KindRect
	case ToolArrow:
		return KindArrow
	}
	return KindPen
}

// PointerMove extends the gesture in progress and reports whether the frame
// changed.
func (e *Editor) PointerMove(p image.Point) bool {
	q := e.lay.ToPicture(p)
	switch {
	case e.drawing && e.shape.Kind == KindPen:
		last := e.shape.Points[len(e.shape.Points)-1]
		// A point closer than half a window pixel to the last one adds
		// nothing to the line.
		if last.dist(q)*e.lay.K < 0.5 {
			return false
		}
		e.shape.Points = append(e.shape.Points, q)
		return true
	case e.drawing:
		e.shape.Points[1] = q
		return true
	case e.cropping:
		e.cropB = q
		return true
	}
	return false
}

// PointerUp ends the gesture: a control fires when the pointer is released
// over the one it was pressed on, a shape joins the picture, a crop is
// applied.
func (e *Editor) PointerUp(p image.Point) Result {
	if e.pressed != nil {
		b := *e.pressed
		e.pressed = nil
		if p.In(b.Rect) {
			return e.activate(b)
		}
		return Continue
	}
	e.PointerMove(p)
	switch {
	case e.drawing:
		e.drawing = false
		s := e.shape
		e.shape = Shape{}
		if s.Kind != KindPen && s.Points[0].dist(s.Points[1])*e.lay.K < 3 {
			// A click with a shape tool draws nothing.
			return Continue
		}
		e.doc.AddShape(s)
	case e.cropping:
		e.cropping = false
		lo, hi := normRect(e.cropA, e.cropB)
		if e.doc.Crop(image.Rectangle{Min: lo.round(), Max: hi.round()}) {
			e.Resize(e.lay.Size)
		}
	}
	return Continue
}

// Activate fires a control as if it were clicked.
func (e *Editor) Activate(b Button) Result { return e.activate(b) }

func (e *Editor) activate(b Button) Result {
	switch b.Control {
	case ControlTool:
		e.commitLabel()
		e.tool = b.Tool
	case ControlColor:
		e.color = b.Color
		if e.typing {
			e.label.Color = Palette[b.Color]
		}
	case ControlRotate:
		e.commitLabel()
		e.doc.Rotate()
		e.Resize(e.lay.Size)
	case ControlUndo:
		e.Undo()
	case ControlCancel:
		return Cancel
	case ControlDone:
		e.commitLabel()
		return Done
	}
	return Continue
}

// Undo drops the label being typed, or else takes back the last step.
func (e *Editor) Undo() {
	if e.typing {
		e.typing = false
		e.label = Shape{}
		return
	}
	if e.doc.Undo() {
		e.Resize(e.lay.Size)
	}
}

// KeyPress types into a label, or works as a shortcut: enter is Done,
// escape is Cancel, ctrl+z is undo, and a tool's letter picks it.
func (e *Editor) KeyPress(k Key) Result {
	if e.typing {
		switch {
		case k.Name == "enter":
			e.commitLabel()
		case k.Name == "escape":
			e.typing = false
			e.label = Shape{}
		case k.Name == "backspace":
			if _, n := utf8.DecodeLastRuneInString(e.label.Text); n > 0 {
				e.label.Text = e.label.Text[:len(e.label.Text)-n]
			}
		case k.Ctrl && k.shortcut() == 'z':
			e.Undo()
		case k.Rune >= ' ' && !k.Ctrl:
			e.label.Text += string(k.Rune)
		}
		return Continue
	}
	switch {
	case k.Name == "enter":
		return Done
	case k.Name == "escape":
		return Cancel
	case k.Ctrl && k.shortcut() == 'z':
		e.Undo()
	case !k.Ctrl:
		if t, ok := toolKeys[k.shortcut()]; ok {
			e.tool = t
		}
	}
	return Continue
}

var toolKeys = map[rune]Tool{
	'p': ToolPen, 'o': ToolEllipse, 'r': ToolRect, 'a': ToolArrow, 't': ToolText, 'c': ToolCrop,
}

func (e *Editor) commitLabel() {
	if !e.typing {
		return
	}
	e.typing = false
	l := e.label
	e.label = Shape{}
	if strings.TrimSpace(l.Text) == "" {
		return
	}
	e.doc.AddShape(l)
}

// PNG is the picture as it stands, encoded.
func (e *Editor) PNG() ([]byte, error) {
	e.commitLabel()
	return e.doc.PNG()
}
