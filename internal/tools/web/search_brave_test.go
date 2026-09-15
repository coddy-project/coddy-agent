package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestParseBraveResultsFromCapturedPage reads a fixture cut from a real Brave
// result page, build hashes and all. Brave appends a per-deploy Svelte hash to
// every class it emits, so this is the test that fails if the parser ever goes
// back to matching a whole class name.
func TestParseBraveResultsFromCapturedPage(t *testing.T) {
	body, err := os.ReadFile("testdata/brave_serp.html")
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseBraveResults(body, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 results from the fixture, got %d", len(got))
	}
	first := got[0]
	if first.URL != "https://go.dev/doc/database/cancel-operations" {
		t.Errorf("url: %q", first.URL)
	}
	if first.Title != "Canceling in-progress operations - The Go Programming Language" {
		t.Errorf("title: %q", first.Title)
	}
	if !strings.Contains(first.Snippet, "context.Context") {
		t.Errorf("snippet: %q", first.Snippet)
	}
	for i, r := range got {
		if !strings.HasPrefix(r.URL, "https://") {
			t.Errorf("result %d has a non-absolute url %q", i, r.URL)
		}
		if r.Title == "" {
			t.Errorf("result %d has no title", i)
		}
	}
}

// TestParseBraveResultsIgnoresTheBuildHash proves the parser keys on the stable
// attribute and class prefix rather than on the hash Brave regenerates.
func TestParseBraveResultsIgnoresTheBuildHash(t *testing.T) {
	page := `<html><body>
<div class="snippet svelte-DIFFERENTHASH" data-pos="0" data-type="web">
  <a href="https://example.com/a" class="svelte-OTHER l1">
    <div class="title search-snippet-title line-clamp-1 svelte-XYZ" title="A Title">A Title</div>
  </a>
  <div class="generic-snippet svelte-Q"><div class="content desktop-default-regular svelte-Q">A description here.</div></div>
</div></body></html>`
	got, err := parseBraveResults([]byte(page), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if got[0].Title != "A Title" || got[0].URL != "https://example.com/a" {
		t.Fatalf("parsed %+v", got[0])
	}
	if got[0].Snippet != "A description here." {
		t.Errorf("snippet: %q", got[0].Snippet)
	}
}

func TestParseBraveResultsRespectsMaxResults(t *testing.T) {
	var b strings.Builder
	b.WriteString("<html><body>")
	for i := 0; i < 8; i++ {
		b.WriteString(`<div class="snippet svelte-a" data-pos="0" data-type="web">` +
			`<a href="https://example.com/` + string(rune('a'+i)) + `" class="l1">` +
			`<div class="title search-snippet-title svelte-b" title="T">T</div></a></div>`)
	}
	b.WriteString("</body></html>")
	got, err := parseBraveResults([]byte(b.String()), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
}

// TestBraveHTMLReportsAMissingLayoutAsBlocked is the guard the reviewers asked
// for: when Brave changes its markup the engine must break loudly, because a
// silent empty answer reaches the model as "the web has nothing".
func TestBraveHTMLReportsAMissingLayoutAsBlocked(t *testing.T) {
	restore := pointBraveHTMLAt(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body><div class="something-else">a page with no result markup</div></body></html>`))
	})
	defer restore()

	_, err := runBrave(context.Background(), Query{Text: "golang", MaxResults: 5}, Settings{})
	reason, isBlocked := asBlocked(err)
	if !isBlocked {
		t.Fatalf("expected a blocked outcome, got %v", err)
	}
	if !strings.Contains(reason, "layout changed") {
		t.Errorf("reason should say the layout is gone: %q", reason)
	}
}

func TestBraveHTMLParsesARealAnswer(t *testing.T) {
	body, err := os.ReadFile("testdata/brave_serp.html")
	if err != nil {
		t.Fatal(err)
	}
	restore := pointBraveHTMLAt(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(body)
	})
	defer restore()

	rows, err := runBrave(context.Background(), Query{Text: "golang context", Page: 1, MaxResults: 10}, Settings{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}
}

func TestBraveHTMLReportsAChallengePageAsBlocked(t *testing.T) {
	restore := pointBraveHTMLAt(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><head><title>Captcha</title></head><body>Please complete the captcha to continue.</body></html>`))
	})
	defer restore()

	_, err := runBrave(context.Background(), Query{Text: "golang", MaxResults: 5}, Settings{})
	reason, isBlocked := asBlocked(err)
	if !isBlocked || !strings.Contains(reason, "captcha") {
		t.Fatalf("expected a challenge to be reported, got %v", err)
	}
}

func TestBraveHTMLReportsANonOKStatusAsBlocked(t *testing.T) {
	restore := pointBraveHTMLAt(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	defer restore()

	_, err := runBrave(context.Background(), Query{Text: "golang", MaxResults: 5}, Settings{})
	reason, isBlocked := asBlocked(err)
	if !isBlocked || !strings.Contains(reason, "429") {
		t.Fatalf("expected http 429 to be reported as blocked, got %v", err)
	}
}

// TestHTTPGetReportsDuckDuckGos202AsBlocked pins the status that made the old
// implementation return an empty list on every single search.
func TestHTTPGetReportsDuckDuckGos202AsBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("<html><title>DuckDuckGo</title></html>"))
	}))
	defer srv.Close()

	_, err := httpGet(context.Background(), srv.URL, nil)
	reason, isBlocked := asBlocked(err)
	if !isBlocked || !strings.Contains(reason, "202") {
		t.Fatalf("202 must be reported as blocked, got %v", err)
	}
}

func TestBraveAPIUsedWhenAKeyIsConfigured(t *testing.T) {
	var gotKey, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Subscription-Token")
		gotQuery = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"web":{"results":[{"title":"Go","url":"https://go.dev","description":"The <strong>Go</strong> site."}]}}`))
	}))
	defer srv.Close()
	old := braveAPIEndpoint
	braveAPIEndpoint = srv.URL
	defer func() { braveAPIEndpoint = old }()

	rows, err := runBrave(context.Background(),
		Query{Text: "golang context", MaxResults: 5},
		Settings{BraveAPIKey: "test-key"})
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "test-key" {
		t.Errorf("key not sent as a header: %q", gotKey)
	}
	if gotQuery != "golang context" {
		t.Errorf("query: %q", gotQuery)
	}
	if len(rows) != 1 || rows[0].URL != "https://go.dev" {
		t.Fatalf("rows: %+v", rows)
	}
	if rows[0].Snippet != "The Go site." {
		t.Errorf("markup not stripped from the description: %q", rows[0].Snippet)
	}
}

func TestBraveAPIRejectedKeyIsBlockedNotEmpty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	old := braveAPIEndpoint
	braveAPIEndpoint = srv.URL
	defer func() { braveAPIEndpoint = old }()

	_, err := runBrave(context.Background(), Query{Text: "go"}, Settings{BraveAPIKey: "bad"})
	reason, isBlocked := asBlocked(err)
	if !isBlocked || !strings.Contains(reason, "key") {
		t.Fatalf("a rejected key must be reported, got %v", err)
	}
}

// pointBraveHTMLAt runs the Brave HTML backend against a local handler.
func pointBraveHTMLAt(t *testing.T, h http.HandlerFunc) func() {
	t.Helper()
	srv := httptest.NewServer(h)
	old := braveHTMLEndpoint
	braveHTMLEndpoint = srv.URL
	return func() {
		braveHTMLEndpoint = old
		srv.Close()
	}
}

func TestEngineQueryFoldsTheSiteRestriction(t *testing.T) {
	for _, tc := range []struct{ site, want string }{
		{"", "golang context"},
		{"go.dev", "golang context site:go.dev"},
		{"https://go.dev/", "golang context site:go.dev"},
		{"http://pkg.go.dev", "golang context site:pkg.go.dev"},
		{"   ", "golang context"},
	} {
		got := engineQuery(Query{Text: "golang context", Site: tc.site})
		if got != tc.want {
			t.Errorf("site %q: got %q, want %q", tc.site, got, tc.want)
		}
	}
}

// TestBraveOffsetIsAPageIndexNotAResultCount pins a measured fact that reads
// like a bug and is not one. Brave's "offset" counts result pages, not results
// to skip: offset=0 and offset=1 were observed returning twenty rows each with
// zero URLs in common, which a skip-one-result offset could not produce, and
// offset=15 returns nothing because the pagination ends around page ten.
// Turning it into (page-1)*count would ask for a page past the end.
func TestBraveOffsetIsAPageIndexNotAResultCount(t *testing.T) {
	var gotOffset string
	restore := pointBraveHTMLAt(t, func(w http.ResponseWriter, r *http.Request) {
		gotOffset = r.URL.Query().Get("offset")
		_, _ = w.Write([]byte(`<html><body>
<div class="snippet svelte-a" data-pos="0" data-type="web">
<a href="https://example.com/a" class="l1"><div class="title search-snippet-title svelte-b" title="T">T</div></a>
</div></body></html>`))
	})
	defer restore()

	if _, err := runBrave(context.Background(), Query{Text: "go", Page: 3, MaxResults: 15}, Settings{}); err != nil {
		t.Fatal(err)
	}
	if gotOffset != "2" {
		t.Fatalf("page 3 must ask for offset 2 (a page index), got %q", gotOffset)
	}
}

func TestBraveFirstPageAsksForOffsetZero(t *testing.T) {
	var gotOffset string
	restore := pointBraveHTMLAt(t, func(w http.ResponseWriter, r *http.Request) {
		gotOffset = r.URL.Query().Get("offset")
		_, _ = w.Write([]byte(`<html><body>
<div class="snippet" data-pos="0" data-type="web">
<a href="https://example.com/a"><div class="title search-snippet-title" title="T">T</div></a>
</div></body></html>`))
	})
	defer restore()

	for _, page := range []int{0, 1} {
		if _, err := runBrave(context.Background(), Query{Text: "go", Page: page, MaxResults: 15}, Settings{}); err != nil {
			t.Fatal(err)
		}
		if gotOffset != "0" {
			t.Fatalf("page %d must ask for offset 0, got %q", page, gotOffset)
		}
	}
}
