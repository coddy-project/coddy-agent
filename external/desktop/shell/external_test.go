//go:build desktop

package shell

import (
	"strings"
	"testing"
)

// What the window will hand to this machine's own browser, and what it will
// not. A link in a chat is text a model wrote, so the strip opens only the two
// schemes a person means by "a link", and the launcher starts an opener with
// whatever it is given.

func TestAnOrdinaryWebAddressIsOpenedOutside(t *testing.T) {
	for _, raw := range []string{
		"https://coddy.dev/docs/surfaces-desktop",
		"http://127.0.0.1:9000/health",
		"https://github.com/coddy-project/coddy-agent/issues/1?tab=x#top",
	} {
		if got, ok := ExternalURL(raw); !ok || got != raw {
			t.Fatalf("%q answered %q, %v", raw, got, ok)
		}
	}
}

func TestOnlyTheWebSchemesLeaveTheWindow(t *testing.T) {
	for _, raw := range []string{
		"",
		"   ",
		"/coddy/sessions",
		"#/s/sess_1",
		"javascript:alert(1)",
		"data:text/html,<script>alert(1)</script>",
		"file:///etc/shadow",
		"ssh://box/repo.git",
		"vscode://file/etc/passwd",
		"HTTPS:/missing-host",
		"https://",
	} {
		if got, ok := ExternalURL(raw); ok {
			t.Fatalf("%q was taken for a web address and answered %q", raw, got)
		}
	}
}

// An opener is started with this string, so a value long enough to be a
// payload rather than a link is refused before it gets there.
func TestAnAddressTooLongToBeALinkIsRefused(t *testing.T) {
	long := "https://example.com/" + strings.Repeat("a", MaxExternalURL)
	if _, ok := ExternalURL(long); ok {
		t.Fatal("an address past the bound was taken")
	}
}
