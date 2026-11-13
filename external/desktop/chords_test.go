//go:build desktop

package desktop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/external/desktop/shell"
)

// The edges of the chords the launcher takes from the browser. The happy path
// - a key pressed on the window reaching the strip - is
// features/desktop_window.feature.

func TestGrabMasksCoverEveryCombinationOfTheLocks(t *testing.T) {
	const ctrl, caps, num uint16 = 1 << 2, 1 << 1, 1 << 4
	got := grabMasks(ctrl, caps, num)
	want := map[uint16]bool{
		ctrl:              false,
		ctrl | caps:       false,
		ctrl | num:        false,
		ctrl | caps | num: false,
	}
	if len(got) != len(want) {
		t.Fatalf("grabbed under %d combinations, wanted %d: %v", len(got), len(want), got)
	}
	for _, mask := range got {
		seen, known := want[mask]
		if !known {
			t.Fatalf("grabbed under %b, which is not a combination of the locks", mask)
		}
		if seen {
			t.Fatalf("grabbed under %b twice", mask)
		}
		want[mask] = true
	}
}

func TestGrabMasksIgnoreALockThisKeyboardDoesNotHave(t *testing.T) {
	const ctrl uint16 = 1 << 2
	if got := grabMasks(ctrl, 0); len(got) != 1 || got[0] != ctrl {
		t.Fatalf("a keyboard with no lock modifier is grabbed under %v, wanted just the base", got)
	}
}

func TestOnlyTheKeysTheBrowserAnswersAreTaken(t *testing.T) {
	if chord, ok := chordOf(keysymT, false); !ok || chord != chordNewTab {
		t.Fatalf("Ctrl+T answered %q, %v", chord, ok)
	}
	if chord, ok := chordOf(keysymW, false); !ok || chord != chordCloseTab {
		t.Fatalf("Ctrl+W answered %q, %v", chord, ok)
	}
	// Ctrl+R is the browser's reload, and in a window whose document is the
	// strip that reloads the whole application: every tab's frame goes back to
	// its chat at once and the strip is rebuilt around them. Taken here, it
	// reloads the one chat the operator is looking at.
	if chord, ok := chordOf(keysymR, false); !ok || chord != chordReloadTab {
		t.Fatalf("Ctrl+R answered %q, %v", chord, ok)
	}
	if !containsKeysym(chordKeysyms(), keysymR) {
		t.Fatal("Ctrl+R is answered but never grabbed, so the browser still reloads the window")
	}
	// A digit is not taken: an application window has no tabs of its own to
	// switch to, so it hands Ctrl+1..9 to the page, which reads them there.
	if _, ok := chordOf('1', false); ok {
		t.Fatal("Ctrl+1 was taken from the browser, which already gives it to the page")
	}
}

// Tab is one key and two chords, told apart by Shift. A passive grab matches
// the modifiers exactly, so the two are grabbed separately and read back the
// same way; a Shift read off the wrong one walks the tabs backwards.
func TestTabWalksTheRowBothWays(t *testing.T) {
	if chord, ok := chordOf(keysymTab, false); !ok || chord != chordNextTab {
		t.Fatalf("Ctrl+Tab answered %q, %v", chord, ok)
	}
	if chord, ok := chordOf(keysymTab, true); !ok || chord != chordPrevTab {
		t.Fatalf("Ctrl+Shift+Tab answered %q, %v", chord, ok)
	}
	// Shift on a key that does not use it is not that key's chord: Ctrl+Shift+T
	// reopens a closed tab in a browser and means nothing here, and it must not
	// be read as Ctrl+T.
	if chord, ok := chordOf(keysymT, true); ok {
		t.Fatalf("Ctrl+Shift+T answered %q", chord)
	}
	// One keyboard map search per distinct key, Tab named once for its two:
	// T, W, R and Tab.
	if got := len(chordKeysyms()); got != 4 {
		t.Fatalf("the keyboard map is searched for %d keys, wanted 4", got)
	}
}

func TestAKeyPressedBetweenTwoPollsIsStillDelivered(t *testing.T) {
	q := newChordQueue()
	q.push(chordMessage{Name: chordNewTab})
	q.push(chordMessage{Name: chordCloseTab})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if got := q.next(ctx); got.Name != chordNewTab {
		t.Fatalf("the first key was %q", got.Name)
	}
	if got := q.next(ctx); got.Name != chordCloseTab {
		t.Fatalf("the second key was %q", got.Name)
	}
}

func TestAPollThatRunsOutAnswersNoChord(t *testing.T) {
	q := newChordQueue()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if got := q.next(ctx); got.Name != "" {
		t.Fatalf("a poll with no key pressed answered %q", got.Name)
	}
}

func TestTheChordsAreRefusedToAPageOfAnotherOrigin(t *testing.T) {
	window := NewChordHandle()
	window.Press(chordNewTab)
	srv := httptest.NewServer(shell.Handler(window))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+shell.Prefix+"keys", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("a page of another origin was answered %s; it could take the key the window's own page is waiting for", res.Status)
	}
}

func TestAWindowThatTakesNoChordsSaysSo(t *testing.T) {
	srv := httptest.NewServer(shell.Handler(noWindowControl{}))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+shell.Prefix+"keys", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusNotImplemented {
		t.Fatalf("a window with no grab behind it answered %s, wanted 501 so the page backs off", res.Status)
	}
}

func TestTheChordRouteAnswersOneKeyPerPoll(t *testing.T) {
	window := NewChordHandle()
	window.Press(chordCloseTab)
	srv := httptest.NewServer(shell.Handler(window))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+shell.Prefix+"keys", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		Chord string `json:"chord"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Chord != chordCloseTab {
		t.Fatalf("the poll answered %q", body.Chord)
	}
}
