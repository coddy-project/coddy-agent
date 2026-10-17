//go:build cli

package xwindow

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"

	"github.com/EvilFreelancer/coddy-agent/external/cli/markup"
	"github.com/EvilFreelancer/coddy-agent/external/cli/xutil/xvfbtest"
)

type shown struct {
	win    xproto.Window
	layout markup.Layout
}

type edited struct {
	png []byte
	ok  bool
	err error
}

// openEditor runs Edit on the display and waits until its window is on
// screen; at maps a picture point to a root position for the driver.
func openEditor(t *testing.T, ctx context.Context, display string, d *xvfbtest.Driver, img image.Image) (<-chan edited, markup.Layout, func(markup.Pt) (int, int)) {
	t.Helper()
	ready := make(chan shown, 1)
	mapped = func(win xproto.Window, l markup.Layout) { ready <- shown{win, l} }
	t.Cleanup(func() { mapped = nil })
	done := make(chan edited, 1)
	go func() {
		b, ok, err := Edit(ctx, display, img)
		done <- edited{b, ok, err}
	}()
	var s shown
	select {
	case s = <-ready:
	case r := <-done:
		t.Fatalf("Edit returned before its window showed: %+v", r.err)
	case <-time.After(20 * time.Second):
		t.Fatal("the window did not show within 20s")
	}
	tr, err := xproto.TranslateCoordinates(d.C.Conn, s.win, d.C.Screen.Root, 0, 0).Reply()
	if err != nil {
		t.Fatal(err)
	}
	ox, oy := int(tr.DstX), int(tr.DstY)
	at := func(p markup.Pt) (int, int) {
		w := s.layout.ToWindow(p)
		return ox + int(w.X+0.5), oy + int(w.Y+0.5)
	}
	return done, s.layout, at
}

func wait(t *testing.T, done <-chan edited) edited {
	t.Helper()
	select {
	case r := <-done:
		return r
	case <-time.After(20 * time.Second):
		t.Fatal("Edit did not return within 20s")
	}
	return edited{}
}

func grey(w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = 0x80, 0x80, 0x80, 0xff
	}
	return img
}

func redIn(img image.Image, r image.Rectangle) int {
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			c := color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
			if c.R > 0xd0 && c.G < 0x80 && c.B < 0x80 {
				n++
			}
		}
	}
	return n
}

func TestEditDrawsTypesAndReturnsThePictureOnDone(t *testing.T) {
	display := xvfbtest.Start(t)
	d := xvfbtest.Drive(t, display)
	// Keys the bare server has no Cyrillic for are bound before the window
	// reads the keymap.
	keyT, keyA, keyEnter := d.Keycode('t'), d.Keycode(0x6c1), d.Keycode(0xff0d)
	done, l, at := openEditor(t, context.Background(), display, d, grey(400, 300))

	// A pen stroke across the top, in the red the editor opens with.
	d.Move(at(markup.Pt{X: 20, Y: 20}))
	d.Press()
	d.Move(at(markup.Pt{X: 150, Y: 40}))
	d.Move(at(markup.Pt{X: 300, Y: 60}))
	d.Release()
	// A label at the bottom left, typed in Cyrillic.
	d.Key(keyT)
	d.Click(at(markup.Pt{X: 30, Y: 200}))
	d.Key(keyA)
	d.Key(keyA)
	d.Key(keyEnter)

	doneBtn, _ := l.Button(markup.ControlDone, 0, 0)
	ox, oy := at(markup.Pt{})
	ox -= l.View.Min.X
	oy -= l.View.Min.Y
	mid := doneBtn.Rect.Min.Add(doneBtn.Rect.Size().Div(2))
	d.Click(ox+mid.X, oy+mid.Y)

	r := wait(t, done)
	if r.err != nil || !r.ok {
		t.Fatalf("Edit = ok %v, err %v; want the picture", r.ok, r.err)
	}
	img, err := png.Decode(bytes.NewReader(r.png))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Size() != image.Pt(400, 300) {
		t.Fatalf("picture size %v", img.Bounds().Size())
	}
	if n := redIn(img, image.Rect(0, 0, 400, 100)); n < 100 {
		t.Fatalf("the stroke left %d red pixels", n)
	}
	if n := redIn(img, image.Rect(20, 190, 200, 260)); n < 30 {
		t.Fatalf("the label left %d red pixels", n)
	}
}

func TestEditEscapeCancels(t *testing.T) {
	display := xvfbtest.Start(t)
	d := xvfbtest.Drive(t, display)
	keyEsc := d.Keycode(0xff1b)
	done, _, at := openEditor(t, context.Background(), display, d, grey(200, 100))
	d.Move(at(markup.Pt{X: 10, Y: 10}))
	d.Key(keyEsc)
	r := wait(t, done)
	if r.err != nil || r.ok || r.png != nil {
		t.Fatalf("escape gave ok %v err %v, want a plain cancel", r.ok, r.err)
	}
}

func TestEditEndsWithItsContext(t *testing.T) {
	display := xvfbtest.Start(t)
	d := xvfbtest.Drive(t, display)
	ctx, cancel := context.WithCancel(context.Background())
	done, _, _ := openEditor(t, ctx, display, d, grey(200, 100))
	cancel()
	r := wait(t, done)
	if r.ok || r.err == nil {
		t.Fatalf("a cancelled context gave ok %v err %v", r.ok, r.err)
	}
}

func TestEditWithoutADisplay(t *testing.T) {
	if _, _, err := Edit(context.Background(), "", grey(10, 10)); err == nil {
		t.Fatal("no display name and no error")
	}
}
