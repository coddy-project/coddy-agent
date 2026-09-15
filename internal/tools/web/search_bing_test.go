package web

import (
	"fmt"
	"testing"
)

// https://example.com (19 bytes) base64-RawStd = "aHR0cHM6Ly9leGFtcGxlLmNvbQ"
const bingExampleURL = "https://example.com"
const bingExampleB64 = "aHR0cHM6Ly9leGFtcGxlLmNvbQ"

// bingResultHTML builds a minimal Bing result page for testing.
func bingResultHTML(results ...struct{ title, b64URL, snippet string }) string {
	s := "<html><body><ol id=\"b_results\">"
	for _, r := range results {
		href := fmt.Sprintf("https://www.bing.com/ck/a?!&&p=abc&u=a1%s&ntb=1", r.b64URL)
		s += fmt.Sprintf(`<li class="b_algo"><h2><a href=%q>%s</a></h2><div class="b_caption"><p>%s</p></div></li>`,
			href, r.title, r.snippet)
	}
	s += "</ol></body></html>"
	return s
}

// ---- parseBingResults unit tests ----

func TestParseBingResults_SingleResult(t *testing.T) {
	body := bingResultHTML(struct{ title, b64URL, snippet string }{
		title:   "Example Site",
		b64URL:  bingExampleB64,
		snippet: "A description of the example site.",
	})
	got, err := parseBingResults([]byte(body), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if got[0].Title != "Example Site" {
		t.Errorf("title: %q", got[0].Title)
	}
	if got[0].URL != bingExampleURL {
		t.Errorf("url: %q", got[0].URL)
	}
	if got[0].Snippet != "A description of the example site." {
		t.Errorf("snippet: %q", got[0].Snippet)
	}
}

func TestParseBingResults_MultipleResults(t *testing.T) {
	body := bingResultHTML(
		struct{ title, b64URL, snippet string }{"A", "aHR0cHM6Ly9hLmNvbQ", ""},
		struct{ title, b64URL, snippet string }{"B", "aHR0cHM6Ly9iLmNvbQ", ""},
		struct{ title, b64URL, snippet string }{"C", "aHR0cHM6Ly9jLmNvbQ", ""},
	)
	got, err := parseBingResults([]byte(body), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 results, got %d", len(got))
	}
}

func TestParseBingResults_RespectsMaxResults(t *testing.T) {
	body := bingResultHTML(
		struct{ title, b64URL, snippet string }{"A", "aHR0cHM6Ly9hLmNvbQ", ""},
		struct{ title, b64URL, snippet string }{"B", "aHR0cHM6Ly9iLmNvbQ", ""},
		struct{ title, b64URL, snippet string }{"C", "aHR0cHM6Ly9jLmNvbQ", ""},
	)
	got, err := parseBingResults([]byte(body), 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results (maxResults cap), got %d", len(got))
	}
}

func TestParseBingResults_EmptyPage(t *testing.T) {
	body := []byte(`<html><body><ol id="b_results"></ol></body></html>`)
	got, err := parseBingResults(body, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 results, got %d", len(got))
	}
}

// ---- decodeBingURL unit tests ----

func TestDecodeBingURL_TrackingURL(t *testing.T) {
	href := fmt.Sprintf("https://www.bing.com/ck/a?!&&p=abc&u=a1%s&ntb=1", bingExampleB64)
	got := decodeBingURL(href)
	if got != bingExampleURL {
		t.Errorf("got %q, want %q", got, bingExampleURL)
	}
}

func TestDecodeBingURL_DirectHTTPS(t *testing.T) {
	got := decodeBingURL("https://example.org/page")
	if got != "https://example.org/page" {
		t.Errorf("got %q", got)
	}
}

func TestDecodeBingURL_NonHTTP(t *testing.T) {
	got := decodeBingURL("/relative/path")
	if got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestDecodeBingURL_MissingA1Prefix(t *testing.T) {
	href := fmt.Sprintf("https://www.bing.com/ck/a?!&&p=abc&u=%s&ntb=1", bingExampleB64)
	got := decodeBingURL(href)
	if got != "" {
		t.Errorf("expected empty when a1 prefix missing, got %q", got)
	}
}
