//go:build cli

package markup

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// solid is a w x h picture of one colour.
func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	return img
}

var grey = color.RGBA{0x80, 0x80, 0x80, 0xff}

func rgbaAt(img *image.RGBA, x, y int) color.RGBA { return img.RGBAAt(x, y) }

// countColor counts the pixels of img within tolerance of c.
func countColor(img *image.RGBA, c color.NRGBA) int {
	n := 0
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			p := img.RGBAAt(x, y)
			if near(p.R, c.R) && near(p.G, c.G) && near(p.B, c.B) {
				n++
			}
		}
	}
	return n
}

func near(a, b uint8) bool { return int(a)-int(b) < 6 && int(b)-int(a) < 6 }

func TestDocumentShapeUndoAndHistory(t *testing.T) {
	d := NewDocument(solid(200, 100, grey))
	if d.Edited() || d.CanUndo() {
		t.Fatal("a fresh document reports edits")
	}
	d.AddShape(Shape{Kind: KindRect, Color: Palette[0], Width: 4, Points: []Pt{{20, 20}, {120, 80}}})
	if got := countColor(d.Image(), Palette[0]); got < 400 {
		t.Fatalf("rectangle painted %d red pixels, want an outline", got)
	}
	if rgbaAt(d.Image(), 70, 50) != grey {
		t.Fatal("the rectangle is filled; it should be an outline")
	}
	if !d.Undo() || d.Edited() {
		t.Fatal("undo did not take the rectangle back")
	}
	if got := countColor(d.Image(), Palette[0]); got != 0 {
		t.Fatalf("after undo %d red pixels remain", got)
	}
	if d.Undo() {
		t.Fatal("undo on an empty history reported a step")
	}
}

func TestDocumentCropAndRotateAreUndoable(t *testing.T) {
	src := solid(300, 200, grey)
	src.SetRGBA(299, 0, color.RGBA{0xff, 0, 0, 0xff}) // top-right marker
	d := NewDocument(src)
	d.Rotate()
	if got := d.Size(); got != image.Pt(200, 300) {
		t.Fatalf("rotated size %v, want 200x300", got)
	}
	// A quarter turn clockwise moves the top-right corner to the bottom right.
	if p := rgbaAt(d.Image(), 199, 299); p.R != 0xff || p.G != 0 {
		t.Fatalf("top-right pixel did not land bottom-right after the turn: %v", p)
	}
	if !d.Crop(image.Rect(10, 20, 110, 70)) {
		t.Fatal("crop inside the picture was refused")
	}
	if got := d.Size(); got != image.Pt(100, 50) {
		t.Fatalf("cropped size %v, want 100x50", got)
	}
	d.Undo()
	d.Undo()
	if got := d.Size(); got != image.Pt(300, 200) {
		t.Fatalf("size after undoing both steps %v, want the original 300x200", got)
	}
	if p := rgbaAt(d.Image(), 299, 0); p.R != 0xff {
		t.Fatal("undo did not restore the original pixels")
	}
}

func TestDocumentCropRefusesNothingAndEverything(t *testing.T) {
	d := NewDocument(solid(100, 100, grey))
	for _, r := range []image.Rectangle{
		image.Rect(200, 200, 300, 300), // outside
		image.Rect(10, 10, 11, 50),     // one pixel wide
		image.Rect(-5, -5, 120, 120),   // the whole picture
	} {
		if d.Crop(r) {
			t.Errorf("crop %v was applied", r)
		}
	}
	if d.Edited() {
		t.Fatal("refused crops left steps in the history")
	}
	if !d.Crop(image.Rect(-10, -10, 50, 40)) || d.Size() != image.Pt(50, 40) {
		t.Fatalf("a crop hanging over the edge is clipped to the picture, got %v", d.Size())
	}
}

func TestDocumentPNGIsThePictureAsItStands(t *testing.T) {
	d := NewDocument(solid(64, 32, grey))
	d.Rotate()
	b, err := d.PNG()
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Size() != image.Pt(32, 64) {
		t.Fatalf("encoded size %v, want 32x64", img.Bounds().Size())
	}
}

// drag runs a press, a move and a release at picture coordinates.
func drag(e *Editor, from, to Pt) Result {
	l := e.Layout()
	e.PointerDown(l.ToWindow(from).round())
	e.PointerMove(l.ToWindow(to).round())
	return e.PointerUp(l.ToWindow(to).round())
}

// click presses and releases a toolbar control.
func click(t *testing.T, e *Editor, c Control, tool Tool, colorIndex int) Result {
	t.Helper()
	b, ok := e.Layout().Button(c, tool, colorIndex)
	if !ok {
		t.Fatalf("no button %v/%v/%v", c, tool, colorIndex)
	}
	mid := b.Rect.Min.Add(b.Rect.Size().Div(2))
	e.PointerDown(mid)
	return e.PointerUp(mid)
}

func TestEditorDrawsWithTheChosenToolAndColour(t *testing.T) {
	e := NewEditor(solid(400, 300, grey), 1)
	if e.Tool() != ToolPen || e.Color() != 0 {
		t.Fatal("the editor should open with the pen and red")
	}
	click(t, e, ControlTool, ToolEllipse, 0)
	click(t, e, ControlColor, 0, 4)
	if e.Tool() != ToolEllipse || e.Color() != 4 {
		t.Fatalf("toolbar clicks chose tool %v colour %v", e.Tool(), e.Color())
	}
	drag(e, Pt{50, 50}, Pt{250, 200})
	if !e.Document().Edited() {
		t.Fatal("the drag drew nothing")
	}
	if got := countColor(e.Document().Image(), Palette[4]); got < 300 {
		t.Fatalf("ellipse painted %d blue pixels", got)
	}
}

func TestEditorShapeClickWithoutDragDrawsNothing(t *testing.T) {
	e := NewEditor(solid(400, 300, grey), 1)
	click(t, e, ControlTool, ToolArrow, 0)
	drag(e, Pt{100, 100}, Pt{100, 100})
	if e.Document().Edited() {
		t.Fatal("a click with the arrow tool added a shape")
	}
}

func TestEditorButtonFiresOnlyWhenReleasedOverIt(t *testing.T) {
	e := NewEditor(solid(400, 300, grey), 1)
	b, _ := e.Layout().Button(ControlTool, ToolRect, 0)
	e.PointerDown(b.Rect.Min.Add(image.Pt(2, 2)))
	if r := e.PointerUp(image.Pt(1, 1)); r != Continue || e.Tool() != ToolPen {
		t.Fatal("a press dragged off the button still fired it")
	}
}

func TestEditorLabelTypingInAnyScript(t *testing.T) {
	e := NewEditor(solid(400, 300, grey), 1)
	click(t, e, ControlTool, ToolText, 0)
	click(t, e, ControlColor, 0, 5)
	l := e.Layout()
	e.PointerDown(l.ToWindow(Pt{40, 40}).round())
	e.PointerUp(l.ToWindow(Pt{40, 40}).round())
	if !e.Typing() {
		t.Fatal("a click with the text tool did not start a label")
	}
	for _, r := range "Тест x" {
		e.KeyPress(Key{Rune: r})
	}
	e.KeyPress(Key{Name: "backspace"})
	if r := e.KeyPress(Key{Name: "escape"}); r != Continue || e.Typing() {
		t.Fatal("escape while typing should drop the label and keep the window")
	}
	if e.Document().Edited() {
		t.Fatal("a dropped label reached the picture")
	}
	e.PointerDown(l.ToWindow(Pt{40, 40}).round())
	for _, r := range "Привет" {
		e.KeyPress(Key{Rune: r})
	}
	e.KeyPress(Key{Name: "enter"})
	if e.Typing() || !e.Document().Edited() {
		t.Fatal("enter did not commit the label")
	}
	if got := countColor(e.Document().Image(), Palette[5]); got < 50 {
		t.Fatalf("the label painted %d white pixels", got)
	}
}

func TestEditorDoneCommitsTheLabelBeingTyped(t *testing.T) {
	e := NewEditor(solid(400, 300, grey), 1)
	click(t, e, ControlTool, ToolText, 0)
	l := e.Layout()
	e.PointerDown(l.ToWindow(Pt{40, 40}).round())
	e.KeyPress(Key{Rune: 'A'})
	if r := click(t, e, ControlDone, 0, 0); r != Done {
		t.Fatalf("Done returned %v", r)
	}
	if !e.Document().Edited() {
		t.Fatal("Done dropped the label being typed")
	}
	if r := click(t, e, ControlCancel, 0, 0); r != Cancel {
		t.Fatalf("Cancel returned %v", r)
	}
}

func TestEditorKeyboardShortcuts(t *testing.T) {
	e := NewEditor(solid(400, 300, grey), 1)
	e.KeyPress(Key{Rune: 'r'})
	if e.Tool() != ToolRect {
		t.Fatal("r did not pick the rectangle")
	}
	drag(e, Pt{10, 10}, Pt{100, 100})
	e.KeyPress(Key{Rune: 'z', Ctrl: true})
	if e.Document().Edited() {
		t.Fatal("ctrl+z did not undo")
	}
	if e.KeyPress(Key{Name: "enter"}) != Done {
		t.Fatal("enter is Done")
	}
	if e.KeyPress(Key{Name: "escape"}) != Cancel {
		t.Fatal("escape is Cancel")
	}
}

func TestEditorCropAndRotateRefitThePicture(t *testing.T) {
	e := NewEditor(solid(400, 300, grey), 1)
	click(t, e, ControlTool, ToolCrop, 0)
	drag(e, Pt{100, 50}, Pt{300, 150})
	if got := e.Document().Size(); got != image.Pt(200, 100) {
		t.Fatalf("crop gave %v, want 200x100", got)
	}
	click(t, e, ControlRotate, 0, 0)
	if got := e.Document().Size(); got != image.Pt(100, 200) {
		t.Fatalf("rotate gave %v, want 100x200", got)
	}
	v := e.Layout().View
	if v.Dx()*2 != v.Dy() {
		t.Fatalf("the view %v does not follow the turned picture", v)
	}
	click(t, e, ControlUndo, 0, 0)
	click(t, e, ControlUndo, 0, 0)
	if got := e.Document().Size(); got != image.Pt(400, 300) {
		t.Fatalf("undo left %v", got)
	}
}

func TestLayoutKeepsControlsApartAndThePictureInItsArea(t *testing.T) {
	for _, scale := range []float64{1, 1.5, 2} {
		min := MinSize(scale)
		for _, sz := range []image.Point{min, {1600, 1000}} {
			l := NewLayout(sz, image.Pt(3000, 1000), scale)
			for i, a := range l.Buttons {
				if !a.Rect.In(l.Bar) {
					t.Errorf("scale %v size %v: button %d %v leaves the bar %v", scale, sz, i, a.Rect, l.Bar)
				}
				for _, b := range l.Buttons[i+1:] {
					if a.Rect.Overlaps(b.Rect) {
						t.Errorf("scale %v size %v: buttons %v and %v overlap", scale, sz, a.Rect, b.Rect)
					}
				}
			}
			if !l.View.In(l.Area) {
				t.Errorf("scale %v size %v: view %v leaves the area %v", scale, sz, l.View, l.Area)
			}
			if r := float64(l.View.Dx()) / float64(l.View.Dy()); r < 2.9 || r > 3.1 {
				t.Errorf("scale %v size %v: view %v lost the 3:1 aspect", scale, sz, l.View)
			}
		}
	}
}

func TestLayoutMapsBetweenWindowAndPicture(t *testing.T) {
	l := NewLayout(image.Pt(1200, 800), image.Pt(2400, 1600), 1)
	p := Pt{1000, 700}
	back := l.ToPicture(l.ToWindow(p).round())
	if back.dist(p) > 1/l.K+0.01 {
		t.Fatalf("round trip moved %v to %v", p, back)
	}
}

func TestPreferredSizeFitsTheScreenAndTheToolbar(t *testing.T) {
	screen := image.Pt(1920, 1080)
	small := PreferredSize(image.Pt(200, 100), screen, 1)
	if min := MinSize(1); small.X < min.X || small.Y < min.Y {
		t.Fatalf("a small picture opens a window %v under the toolbar's minimum %v", small, min)
	}
	big := PreferredSize(image.Pt(5000, 4000), screen, 1)
	if big.X > screen.X || big.Y > screen.Y {
		t.Fatalf("a big picture opens a window %v larger than the screen", big)
	}
}

func TestFramePaintsTheToolbarState(t *testing.T) {
	e := NewEditor(solid(400, 300, grey), 1)
	f := e.Frame()
	if f.Bounds().Size() != e.Layout().Size {
		t.Fatalf("frame %v, layout %v", f.Bounds().Size(), e.Layout().Size)
	}
	done, _ := e.Layout().Button(ControlDone, 0, 0)
	if p := f.RGBAAt(done.Rect.Min.X+3, done.Rect.Min.Y+done.Rect.Dy()/2); p.B < 0xe0 || p.R > 0x30 {
		t.Fatalf("Done is not blue: %v", p)
	}
	center := e.Layout().View.Min.Add(e.Layout().View.Size().Div(2))
	if p := f.RGBAAt(center.X, center.Y); p != grey {
		t.Fatalf("the picture is not in the view: %v", p)
	}
	// The ring of the chosen swatch moves with the choice.
	ringAt := func(i int) color.RGBA {
		b, _ := e.Layout().Button(ControlColor, 0, i)
		c := b.Rect.Min.Add(b.Rect.Size().Div(2))
		return e.Frame().RGBAAt(c.X+b.Rect.Dx()/2+1, c.Y)
	}
	if p := ringAt(0); p.R < 0xe0 || p.G < 0xe0 {
		t.Fatalf("the chosen swatch has no ring: %v", p)
	}
	click(t, e, ControlColor, 0, 3)
	if p := ringAt(0); p.R > 0x80 {
		t.Fatalf("the ring stayed on the old swatch: %v", p)
	}
}

func TestEditorShortcutsFollowTheKeyNotTheLayout(t *testing.T) {
	e := NewEditor(solid(400, 300, grey), 1)
	// The R key in a Russian layout, and with caps lock on.
	e.KeyPress(Key{Rune: 'к', Base: 'r'})
	if e.Tool() != ToolRect {
		t.Fatalf("the R key in a Russian layout picked %v", e.Tool())
	}
	e.KeyPress(Key{Rune: 'A', Base: 'a'})
	if e.Tool() != ToolArrow {
		t.Fatalf("the A key with caps lock picked %v", e.Tool())
	}
	drag(e, Pt{10, 10}, Pt{100, 100})
	// ctrl+z where the layout makes it ctrl+я.
	e.KeyPress(Key{Rune: 'я', Ctrl: true, Base: 'z'})
	if e.Document().Edited() {
		t.Fatal("ctrl+z in a Russian layout did not undo")
	}
	// A window that cannot tell the base still takes caps-locked letters.
	e.KeyPress(Key{Rune: 'P'})
	if e.Tool() != ToolPen {
		t.Fatalf("P picked %v", e.Tool())
	}
}
