package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ddgResultPage builds the no-JavaScript result page DuckDuckGo serves.
func ddgResultPage(rows ...[3]string) string {
	var b strings.Builder
	b.WriteString(`<html><body><div id="links" class="results">`)
	for _, r := range rows {
		b.WriteString(`<div class="result results_links results_links_deep web-result">` +
			`<div class="result__body links_main">` +
			`<h2 class="result__title"><a class="result__a" href="` + r[1] + `">` + r[0] + `</a></h2>` +
			`<a class="result__snippet" href="` + r[1] + `">` + r[2] + `</a>` +
			`</div></div>`)
	}
	b.WriteString(`</div></body></html>`)
	return b.String()
}

func pointDDGAt(t *testing.T, h http.HandlerFunc) func() {
	t.Helper()
	srv := httptest.NewServer(h)
	old := ddgEndpoint
	ddgEndpoint = srv.URL + "/html/"
	return func() {
		ddgEndpoint = old
		srv.Close()
	}
}

func TestParseDDGResultsUnwrapsTheRedirect(t *testing.T) {
	page := ddgResultPage(
		[3]string{"Context package", "//duckduckgo.com/l/?uddg=https%3A%2F%2Fpkg.go.dev%2Fcontext&rut=abc", "Package context."},
		[3]string{"Go blog", "https://go.dev/blog/context", "Share by communicating."},
	)
	got, err := parseDDGResults([]byte(page), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results, got %d", len(got))
	}
	if got[0].URL != "https://pkg.go.dev/context" {
		t.Errorf("redirect not unwrapped: %q", got[0].URL)
	}
	if got[0].Title != "Context package" || got[0].Snippet != "Package context." {
		t.Errorf("parsed %+v", got[0])
	}
	if got[1].URL != "https://go.dev/blog/context" {
		t.Errorf("direct href: %q", got[1].URL)
	}
}

func TestParseDDGResultsRespectsMaxResults(t *testing.T) {
	rows := make([][3]string, 0, 8)
	for i := 0; i < 8; i++ {
		rows = append(rows, [3]string{"T", "https://example.com/" + string(rune('a'+i)), "s"})
	}
	got, err := parseDDGResults([]byte(ddgResultPage(rows...)), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
}

// TestDDGReports202AsBlocked is the measured failure: both DuckDuckGo endpoints
// answer a server with 202 and an interstitial. It must arrive as blocked, not
// as an empty index.
func TestDDGReports202AsBlocked(t *testing.T) {
	restore := pointDDGAt(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`<html><head><title>DuckDuckGo</title></head><body></body></html>`))
	})
	defer restore()

	_, err := runDDG(context.Background(), Query{Text: "golang context", MaxResults: 10}, Settings{})
	reason, isBlocked := asBlocked(err)
	if !isBlocked || !strings.Contains(reason, "202") {
		t.Fatalf("expected a blocked 202, got %v", err)
	}
}

// TestDDGGenuinelyEmptyIsEmptyNotBlocked is the distinction a search library
// cannot make, and the reason this backend does its own request: an operator
// whose egress DuckDuckGo still serves must not be told the engine is broken
// when the index simply had nothing.
func TestDDGGenuinelyEmptyIsEmptyNotBlocked(t *testing.T) {
	restore := pointDDGAt(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body><div id="links" class="results">` +
			`<div class="no-results">No results.</div></div></body></html>`))
	})
	defer restore()

	rows, err := runDDG(context.Background(), Query{Text: "zzqx nonexistent", MaxResults: 10}, Settings{})
	if err != nil {
		t.Fatalf("an empty index is not an error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no rows, got %d", len(rows))
	}
}

func TestDDGParsesARealShapedAnswer(t *testing.T) {
	restore := pointDDGAt(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(ddgResultPage(
			[3]string{"Context package", "//duckduckgo.com/l/?uddg=https%3A%2F%2Fpkg.go.dev%2Fcontext", "Package context."},
		)))
	})
	defer restore()

	rows, err := runDDG(context.Background(), Query{Text: "golang context", MaxResults: 10}, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].URL != "https://pkg.go.dev/context" {
		t.Fatalf("rows: %+v", rows)
	}
}

func TestDecodeDDGHrefRejectsNonHTTP(t *testing.T) {
	for _, raw := range []string{
		"javascript:alert(1)",
		"//duckduckgo.com/l/?uddg=javascript%3Aalert(1)",
		"",
		"/y.js?ad_provider=x",
	} {
		if got := decodeDDGHref(raw); got != "" {
			t.Errorf("%q should be refused, got %q", raw, got)
		}
	}
}
