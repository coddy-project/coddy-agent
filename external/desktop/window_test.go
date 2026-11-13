//go:build desktop

package desktop

import (
	"context"
	"testing"
	"time"
)

// What the strip is told, and by whom.
//
// The queue the page long-polls belongs to the handle, not to the window: a
// machine where no window can be driven - a Wayland session, macOS, Windows,
// a run with no window at all - still has a tray icon to pick New chat from
// and still has switches that move outside the page. While the queue lived in
// the X11 control, every one of those messages was dropped on the floor on
// exactly those machines.

func TestTheStripIsToldEvenWhereNoWindowCanBeDriven(t *testing.T) {
	h := NewWindowHandle()
	// No Attach: this is a machine with no window the launcher can drive.
	h.RequestChord(chordNewTab)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if got := h.NextChord(ctx); got.Name != chordNewTab {
		t.Fatalf("the strip was told %q", got.Name)
	}
}

// A key the launcher took at the X server travels down the same queue, so the
// page holds one poll and reads both kinds of message on it.
func TestAGrabbedKeyAndAMenuRowShareTheQueue(t *testing.T) {
	h := NewWindowHandle()
	h.Attach(&chordWindow{})
	h.pushGrabbed(chordNextTab)
	h.RequestChord(chordNewTab)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, second := h.NextChord(ctx), h.NextChord(ctx)
	if first.Name != chordNextTab || second.Name != chordNewTab {
		t.Fatalf("the strip was told %q then %q", first.Name, second.Name)
	}
}

// A page that asks before anything is pressed waits rather than being told
// nothing: the long poll is what delivers the first chord.
func TestAPollWithNothingToSayWaitsForTheBound(t *testing.T) {
	h := NewWindowHandle()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	if got := h.NextChord(ctx); got.Name != "" {
		t.Fatalf("the strip was told %q out of nowhere", got.Name)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Fatal("the poll answered at once instead of waiting")
	}
}

// chordWindow is a window that can be driven but takes no chords of its own.
type chordWindow struct{ noWindowControl }

func (chordWindow) Supported() bool { return true }
