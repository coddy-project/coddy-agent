package web

import (
	"strings"
	"testing"
)

func TestDedupKeyFoldsTheSamePageReachedDifferently(t *testing.T) {
	same := [][]string{
		{"https://pkg.go.dev/context", "http://pkg.go.dev/context"},
		{"https://pkg.go.dev/context", "https://www.pkg.go.dev/context"},
		{"https://pkg.go.dev/context", "https://pkg.go.dev/context/"},
		{"https://pkg.go.dev/context", "https://pkg.go.dev/context?utm_source=bing&utm_medium=cpc"},
		{"https://pkg.go.dev/context", "https://pkg.go.dev/context#Background"},
		{"https://pkg.go.dev/context", "https://PKG.GO.DEV/context"},
		{"https://example.com/a?fbclid=123", "https://example.com/a?gclid=456"},
	}
	for _, pair := range same {
		if dedupKey(pair[0]) != dedupKey(pair[1]) {
			t.Errorf("%q and %q should be one page: %q vs %q",
				pair[0], pair[1], dedupKey(pair[0]), dedupKey(pair[1]))
		}
	}
}

func TestDedupKeyKeepsDifferentPagesApart(t *testing.T) {
	different := [][]string{
		{"https://pkg.go.dev/context", "https://pkg.go.dev/sync"},
		{"https://example.com/a", "https://example.org/a"},
		// A query parameter that selects content is not tracking.
		{"https://example.com/search?q=go", "https://example.com/search?q=rust"},
		{"https://example.com/a", "https://example.com/a/b"},
	}
	for _, pair := range different {
		if dedupKey(pair[0]) == dedupKey(pair[1]) {
			t.Errorf("%q and %q collapsed to one key %q", pair[0], pair[1], dedupKey(pair[0]))
		}
	}
}

func TestDedupKeyLeavesAnUnparseableValueAlone(t *testing.T) {
	if got := dedupKey("not a url"); got != "not a url" {
		t.Errorf("got %q", got)
	}
}

func TestClipSnippetCutsOnAWordBoundary(t *testing.T) {
	long := strings.Repeat("alpha beta ", 60)
	got := clipSnippet(long, 40)
	if len([]rune(got)) > 44 {
		t.Fatalf("not clipped: %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("clipped text should be marked: %q", got)
	}
	if strings.Contains(got, "alph...") {
		t.Errorf("cut in the middle of a word: %q", got)
	}
}

func TestClipSnippetCollapsesWhitespace(t *testing.T) {
	got := clipSnippet("  a \n\n  b\tc  ", 100)
	if got != "a b c" {
		t.Errorf("got %q", got)
	}
}

func TestClipSnippetLeavesShortTextAlone(t *testing.T) {
	if got := clipSnippet("a short description", 100); got != "a short description" {
		t.Errorf("got %q", got)
	}
}

// TestDedupKeyKeepsContentSelectingParameters is the fix for a real defect: the
// tracking list matched "ref" and "source" by prefix, which merged two
// revisions of one file on a source host and killed "refresh" and "referral"
// along the way.
func TestDedupKeyKeepsContentSelectingParameters(t *testing.T) {
	different := [][]string{
		{"https://github.com/o/r/blob/main/f.go?ref=v1", "https://github.com/o/r/blob/main/f.go?ref=v2"},
		{"https://api.example.com/x?source=archive", "https://api.example.com/x?source=live"},
		{"https://example.com/p?refresh=1", "https://example.com/p?refresh=2"},
		{"https://example.com/p?referral_code=a", "https://example.com/p?referral_code=b"},
		{"https://example.com/p?id=1", "https://example.com/p?id=2"},
	}
	for _, pair := range different {
		if dedupKey(pair[0]) == dedupKey(pair[1]) {
			t.Errorf("%q and %q are different pages but collapsed to %q",
				pair[0], pair[1], dedupKey(pair[0]))
		}
	}
}

func TestDedupKeyStillStripsRealTrackingParameters(t *testing.T) {
	base := dedupKey("https://example.com/p")
	for _, raw := range []string{
		"https://example.com/p?utm_source=x&utm_campaign=y",
		"https://example.com/p?fbclid=123",
		"https://example.com/p?gclid=123",
		"https://example.com/p?msclkid=123",
		"https://example.com/p?mc_cid=1&mc_eid=2",
		"https://example.com/p?referrer=newsletter",
		"https://example.com/p?ref_src=twsrc",
	} {
		if dedupKey(raw) != base {
			t.Errorf("%q should fold to the bare page, got %q", raw, dedupKey(raw))
		}
	}
}
