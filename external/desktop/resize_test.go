//go:build desktop

package desktop

import "testing"

func TestResizeKeepsTheOppositeEdgeAnchored(t *testing.T) {
	start := windowRect{X: 100, Y: 200, W: 800, H: 600}
	for _, tc := range []struct {
		direction string
		dx, dy    int
		want      windowRect
	}{
		{"n", 0, -40, windowRect{X: 100, Y: 160, W: 800, H: 640}},
		{"s", 0, 40, windowRect{X: 100, Y: 200, W: 800, H: 640}},
		{"w", -40, 0, windowRect{X: 60, Y: 200, W: 840, H: 600}},
		{"e", 40, 0, windowRect{X: 100, Y: 200, W: 840, H: 600}},
		{"nw", -40, -30, windowRect{X: 60, Y: 170, W: 840, H: 630}},
		{"ne", 40, -30, windowRect{X: 100, Y: 170, W: 840, H: 630}},
		{"sw", -40, 30, windowRect{X: 60, Y: 200, W: 840, H: 630}},
		{"se", 40, 30, windowRect{X: 100, Y: 200, W: 840, H: 630}},
	} {
		t.Run(tc.direction, func(t *testing.T) {
			got, ok := resizeRect(start, tc.direction, tc.dx, tc.dy)
			if !ok || got != tc.want {
				t.Fatalf("resizeRect(%v, %q, %d, %d) = %v, %v; want %v, true", start, tc.direction, tc.dx, tc.dy, got, ok, tc.want)
			}
		})
	}
}

func TestResizeHonoursTheMinimumWithoutMovingTheAnchor(t *testing.T) {
	start := windowRect{X: 100, Y: 200, W: 800, H: 600}
	for _, tc := range []struct {
		direction string
		dx, dy    int
		want      windowRect
	}{
		{"w", 1000, 0, windowRect{X: 100 + 800 - minWindowWidth, Y: 200, W: minWindowWidth, H: 600}},
		{"n", 0, 1000, windowRect{X: 100, Y: 200 + 600 - minWindowHeight, W: 800, H: minWindowHeight}},
		{"e", -1000, 0, windowRect{X: 100, Y: 200, W: minWindowWidth, H: 600}},
		{"s", 0, -1000, windowRect{X: 100, Y: 200, W: 800, H: minWindowHeight}},
	} {
		got, ok := resizeRect(start, tc.direction, tc.dx, tc.dy)
		if !ok || got != tc.want {
			t.Fatalf("%s minimum resize = %v, %v; want %v, true", tc.direction, got, ok, tc.want)
		}
	}
}

func TestResizeRefusesAnUnknownDirection(t *testing.T) {
	if _, ok := resizeRect(windowRect{X: 1, Y: 2, W: 300, H: 200}, "sideways", 10, 10); ok {
		t.Fatal("an unknown resize direction was accepted")
	}
}
