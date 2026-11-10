//go:build desktop

package shell

import (
	"net/url"
	"strings"
)

// Links that leave Coddy.
//
// A chat is full of addresses - the documentation, an issue, whatever the
// model wrote - and a click on one inside a frame would load that site into
// the frame, where it would sit under a tab strip that says "New chat" with no
// address bar, no back button and no way out but closing the tab. So the strip
// catches such a click and hands the address to this machine's own browser,
// where a page from the internet belongs.
//
// What may be handed over is decided here, because the launcher starts an
// opener (`xdg-open` and its kin) with whatever it is given, and an opener
// hands an address to whichever application claims its scheme. Only the two
// schemes a person means by "a link" are taken.

// MaxExternalURL bounds the address, which is a string from a page rather than
// from the operator: long enough for any real link, short enough that nothing
// is started with a payload.
const MaxExternalURL = 4096

// ExternalURL answers the address to open outside the window, and whether
// there is one.
func ExternalURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > MaxExternalURL {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}
	// Scheme and host both: `https:/example.com` parses with no host at all,
	// and an opener would make what it liked of it.
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", false
	}
	if u.Host == "" {
		return "", false
	}
	return raw, true
}

// ExternalOpener is a window control that can also hand an address to this
// machine's own browser.
type ExternalOpener interface {
	OpenExternal(url string) error
}
