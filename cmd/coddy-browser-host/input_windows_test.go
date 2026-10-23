//go:build windows

package main

import "testing"

// The destination guard compares the page's own title against the foreground
// window title. It used to accept any substring, so a page that titled itself
// "Mozilla" kept receiving keystrokes after the user switched to another
// Firefox tab; the title has to be the whole leading segment instead.
func TestFirefoxTabTitle(t *testing.T) {
	cases := []struct{ window, want string }{
		{"Get — Mozilla Firefox", "Get"},
		{"Download - Mozilla Firefox", "Download"},
		{"A — B — Mozilla Firefox", "A — B"},
		{"Download Firefox — Mozilla Firefox", "Download Firefox"},
		{"Page — Mozilla Firefox (Private Browsing)", "Page"},
		{"Page — Firefox Developer Edition", "Page"},
		{"Mozilla Firefox", "Mozilla Firefox"},
		// A page title that merely mentions Firefox is not a browser suffix,
		// so it stays whole.
		{"Firefox tips — Newsletter", "Firefox tips — Newsletter"},
		{"  Spaced — Mozilla Firefox", "Spaced"},
	}
	for _, c := range cases {
		if got := firefoxTabTitle(c.window); got != c.want {
			t.Errorf("firefoxTabTitle(%q) = %q, want %q", c.window, got, c.want)
		}
	}
}

// A page whose title is a substring of another tab's window title used to pass
// the guard. It must not.
func TestFirefoxTabTitleRejectsASubstringOfAnotherTab(t *testing.T) {
	const other = "Other Tab — Mozilla Firefox"
	for _, title := range []string{"Mozilla", "e", "Other", "Tab —"} {
		if firefoxTabTitle(other) == title {
			t.Fatalf("a page titled %q would claim %q", title, other)
		}
	}
}

func TestWheelUnitsConvertsPixelsIntoWheelNotches(t *testing.T) {
	if wheelUnits(0) != 0 {
		t.Fatalf("zero scroll became %d", wheelUnits(0))
	}
	half, whole := wheelUnits(200), wheelUnits(400)
	if half <= 0 || whole <= 0 {
		t.Fatalf("scroll down lost its sign: %d %d", half, whole)
	}
	if half*2 < whole-1 || half*2 > whole+1 {
		t.Fatalf("not proportional: wheelUnits(200)=%d wheelUnits(400)=%d", half, whole)
	}
	// One WHEEL_DELTA of wheel data is one notch, and one notch scrolls
	// wheelScrollLines() text lines of 20px: exactly that many CSS pixels is
	// one notch. Handing the pixels to mouse_event unchanged — the old
	// behaviour — gets this wrong at every wheel setting but one.
	const wheelDelta = 120
	perNotch := float64(wheelScrollLines() * 20)
	if got := wheelUnits(perNotch); got != wheelDelta {
		t.Fatalf("wheelUnits(%v CSS px) = %d, want one notch (%d)", perNotch, got, wheelDelta)
	}
}
