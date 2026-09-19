//go:build cli

package cli

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/EvilFreelancer/coddy-agent/internal/docs"
)

func testDocsLibrary(t *testing.T) *docs.Library {
	t.Helper()
	long := strings.Repeat("A line of the long section.\n\n", 40)
	fsys := fstest.MapFS{
		"nav.yaml": {Data: []byte(`groups:
  - id: guide
    title: Guide
    summary: The guide.
    pages:
      - path: guide/one.md
        title: First page
        summary: Where it starts.
      - path: guide/two.md
        title: Second page
        summary: Where it goes on.
`)},
		"guide/one.md": {Data: []byte("# First page\n\nIntro.\n\n## Alpha\n\n" + long + "## Beta\n\nThe beta section talks about proxies.\n\n![shot](../assets/x.png)\n")},
		"guide/two.md": {Data: []byte("# Second page\n\n## Gamma\n\nThe end.\n")},
	}
	lib, err := docs.Load(fsys, "dev")
	if err != nil {
		t.Fatal(err)
	}
	return lib
}

func newTestDocsModal(t *testing.T) (*docsModal, *bool) {
	t.Helper()
	closed := false
	m := newDocsModal(testDocsLibrary(t), newTheme("dark"), markdownTheme(newTheme("dark"), false), func() int { return 20 }, func() {})
	m.OnClose = func() { closed = true }
	return m, &closed
}

func TestDocsModalListsTheContentsAndSearches(t *testing.T) {
	m, closed := newTestDocsModal(t)
	out := plainLines(m.Render(100))
	if !strings.Contains(out, "First page") || !strings.Contains(out, "Second page") || !strings.Contains(out, "guide/one  Where it starts.") {
		t.Fatalf("the empty query lists the pages, the selected one with its reference and summary:\n%s", out)
	}
	if strings.Contains(out, "\x1b") || strings.Contains(out, "[1m") {
		t.Fatalf("styling leaked into the text:\n%q", out)
	}
	for _, r := range "proxies" {
		m.HandleInput([]byte(string(r)))
	}
	out = plainLines(m.Render(100))
	if !strings.Contains(out, "› proxies") || !strings.Contains(out, "First page › Beta") || strings.Contains(out, "Second page") {
		t.Fatalf("search results:\n%s", out)
	}
	m.HandleInput([]byte("\x7f"))
	if m.query != "proxie" {
		t.Fatalf("backspace: %q", m.query)
	}
	m.InsertPaste("s\nplease")
	if m.query != "proxies please" {
		t.Fatalf("paste: %q", m.query)
	}
	m.HandleInput([]byte("\x1b"))
	if !*closed {
		t.Fatal("escape in the search view closes the help")
	}
}

func TestDocsModalOpensAtTheSectionAndTurnsPages(t *testing.T) {
	m, closed := newTestDocsModal(t)
	m.SetQuery("proxies")
	m.HandleInput([]byte("\r"))
	if m.PageTitle() != "First page" {
		t.Fatalf("enter opens the hit's page, got %q", m.PageTitle())
	}
	out := plainLines(m.Render(80))
	if !strings.Contains(out, "Beta") || strings.Contains(out, "Intro.") || !strings.Contains(out, "[image: shot]") {
		t.Fatalf("the page opens scrolled to the section, images as captions:\n%s", out)
	}
	m.HandleInput([]byte("\x1b[Z")) // shift+tab: the section before
	out = plainLines(m.Render(80))
	if !strings.Contains(out, "Alpha") {
		t.Fatalf("shift+tab goes to the section before:\n%s", out)
	}
	m.HandleInput([]byte("p"))
	if m.PageTitle() != "First page" {
		t.Fatal("p on the first page stays there")
	}
	m.HandleInput([]byte("n"))
	if m.PageTitle() != "Second page" {
		t.Fatalf("n turns to the next page, got %q", m.PageTitle())
	}
	m.HandleInput([]byte("n"))
	if m.PageTitle() != "Second page" {
		t.Fatal("n on the last page stays there")
	}
	m.HandleInput([]byte("\x1b"))
	if m.PageTitle() != "" || *closed {
		t.Fatal("escape leaves the page for the search view first")
	}
	m.HandleInput([]byte("\x1bOP"))
	if !*closed {
		t.Fatal("F1 closes the help")
	}
}

func TestDocsModalScrollsWithinTheTerminal(t *testing.T) {
	m, _ := newTestDocsModal(t)
	page, _ := m.lib.Page("guide/one")
	m.OpenPage(page, "")
	lines := m.Render(80)
	if len(lines) > 20 {
		t.Fatalf("the overlay takes %d rows of a 20-row terminal", len(lines))
	}
	if !strings.Contains(plainLines(lines), "lines 1-") {
		t.Fatalf("a long page says where the reader is:\n%s", plainLines(lines))
	}
	m.HandleInput([]byte("\x1b[F")) // end
	out := plainLines(m.Render(80))
	if !strings.Contains(out, "100%") {
		t.Fatalf("end scrolls to the bottom:\n%s", out)
	}
	m.HandleInput([]byte("\x1b[H")) // home
	if out := plainLines(m.Render(80)); !strings.Contains(out, "Intro.") {
		t.Fatalf("home scrolls to the top:\n%s", out)
	}
}

func TestSlashDocsOpensAPageOrASearch(t *testing.T) {
	a := newTestApp(t)
	if !a.dispatchSlash("/docs features/mentions#completion") {
		t.Fatal("/docs is a console command")
	}
	m, ok := a.modal.(*docsModal)
	if !ok || m.PageTitle() != "Mentions" {
		t.Fatalf("/docs <page> opens the page: %#v", a.modal)
	}
	a.closeModal()
	a.dispatchSlash("/docs telegram proxy")
	m, ok = a.modal.(*docsModal)
	if !ok || m.PageTitle() != "" || m.query != "telegram proxy" || len(m.entries) == 0 {
		t.Fatalf("/docs <words> searches: %#v", a.modal)
	}
}
