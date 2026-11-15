//go:build desktop && ((linux && !android) || freebsd)

package desktop

import (
	"slices"
	"testing"
)

// Linux and FreeBSD share this list because they share the browser. They do
// not share the names: the FreeBSD ports install the Chromium family under
// their own, and with only the Linux spellings in the list findBrowser missed
// every one of them, so `coddy desktop` on a FreeBSD machine with Chromium
// installed opened no window at all. The names come from the pkg-plist of
// each port, so this test is what notices if one of them is dropped.
func TestTheCandidatesCoverTheFreeBSDPorts(t *testing.T) {
	candidates := browserCandidates()
	for port, binary := range map[string]string{
		"www/chromium":           "chrome",
		"www/ungoogled-chromium": "ungoogled-chromium",
		"www/brave":              "brave",
		"www/iridium":            "iridium",
	} {
		if !slices.Contains(candidates, binary) {
			t.Errorf("the FreeBSD port %s installs %q, which no candidate names: a FreeBSD desktop running it gets no window", port, binary)
		}
	}
}

// The Linux names still come first, so a Linux box that has one of them keeps
// the browser it had before the FreeBSD names were added to the tail.
func TestTheLinuxCandidatesStayAhead(t *testing.T) {
	candidates := browserCandidates()
	first := slices.Index(candidates, "google-chrome")
	if first != 0 {
		t.Fatalf("google-chrome is at %d, want 0: the preference order moved", first)
	}
	if got, want := slices.Index(candidates, "chromium"), slices.Index(candidates, "chrome"); got >= want {
		t.Fatalf("chromium is at %d and chrome at %d: the Linux name must be tried first", got, want)
	}
}
