//go:build desktop && linux && !android

package desktop

import "testing"

func TestResizeShapeUsesTheNewWindowGeometry(t *testing.T) {
	start := windowRect{X: 100, Y: 200, W: 800, H: 600}
	next, ok := resizeRect(start, "se", 40, 30)
	if !ok {
		t.Fatal("the resize was refused")
	}
	rects := resizeShape(next)
	if len(rects) == 0 {
		t.Fatal("a floating window got no rounded shape")
	}
	var maxX, maxY int
	for _, rect := range rects {
		if right := int(rect.X) + int(rect.Width); right > maxX {
			maxX = right
		}
		if bottom := int(rect.Y) + int(rect.Height); bottom > maxY {
			maxY = bottom
		}
	}
	if maxX != next.W || maxY != next.H {
		t.Fatalf("the rebuilt shape covers %dx%d, want the resized window's %dx%d", maxX, maxY, next.W, next.H)
	}
	old := resizeShape(start)
	if len(old) == len(rects) && old[0].Width == rects[0].Width && old[len(old)-1].Width == rects[len(rects)-1].Width {
		t.Fatal("the resize kept the old corner geometry")
	}
}

func TestResizeShapeRejectsGeometryXCannotRepresent(t *testing.T) {
	if rects := resizeShape(windowRect{W: int(^uint16(0)) + 1, H: 600}); rects != nil {
		t.Fatal("an oversized resize produced truncated X rectangles")
	}
}
