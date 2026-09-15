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
