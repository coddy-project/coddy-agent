//go:build desktop

package desktop

// What a drag of the tab strip does to the window around it, as arithmetic
// alone: the window manager's side of it lives in window_x11.go, which is X11
// only and cannot be exercised without a display and a running manager.

// windowRect is a window on the screen, in root coordinates.
type windowRect struct{ X, Y, W, H int }

// dragStart is what has to happen before the window can start following the
// pointer, and where it stands once it has.
type dragStart struct {
	// Restore says the window is pinned and has to be let loose first.
	Restore bool
	// X and Y are where the window stands when the drag begins; every move of
	// the gesture is measured from there.
	X, Y int
}

// planDragStart answers where the window stands when a drag of the bar begins.
//
// A window the window manager has pinned - maximised, or quick-tiled to half
// the screen with Meta+Left - refuses every move request on the axis it fills.
// Checked against KWin on this desktop: a quick-tiled window carries
// _NET_WM_STATE_MAXIMIZED_VERT, and _NET_MOVERESIZE_WINDOW on it is answered
// with nothing at all, so a drag of the bar left the window sitting where it
// was. Dragging a title bar restores such a window first; so does this.
//
// Letting it loose gives the window its own, smaller size back, and the point
// the person grabbed would then be somewhere else along the bar - a window that
// jumps out from under the pointer. It is put back under it in proportion,
// which is what every desktop does with a maximised window dragged by its title
// bar. The top edge does not move: the pointer is somewhere inside the bar's
// own height, and the bar is as tall either way.
func planDragStart(pinned bool, cur, restored windowRect, pointerX int) dragStart {
	if !pinned {
		return dragStart{X: cur.X, Y: cur.Y}
	}
	// A window pinned on the vertical axis alone keeps its width, and the
	// arithmetic below then answers cur.X by itself. Sizes that make no sense
	// leave the window where it is; letting it loose is still the point.
	if cur.W <= 0 || restored.W <= 0 {
		return dragStart{Restore: true, X: cur.X, Y: cur.Y}
	}
	grab := pointerX - cur.X
	if grab < 0 {
		grab = 0
	}
	if grab > cur.W {
		grab = cur.W
	}
	return dragStart{Restore: true, X: pointerX - grab*restored.W/cur.W, Y: cur.Y}
}

// clampMove bounds where a drag may put the window.
//
// Only the top edge is bounded, and only so the bar cannot be pushed up under
// a panel where nothing can take hold of it again. Nothing stops the window
// going off the left, right or bottom of the screen: every desktop lets a
// window hang off an edge, and the first version of this clamped x to zero,
// which made a drag to the left stop dead at the edge while the pointer kept
// going - the window going right was fine, so it read as a bug on one side
// only. Nothing has to hold the window on screen anyway: the pointer does,
// because the bar is under it for the whole gesture.
func clampMove(x, y int) (int, int) {
	if y < 0 {
		y = 0
	}
	return x, y
}
