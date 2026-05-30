package web

import (
	"testing"
)

// ---- parseGoogleResults unit tests ----

func TestParseGoogleResults_DirectHTTPSLink(t *testing.T) {
	body := []byte(`<html><body>
<div class="g">
  <a href="https://example.com/page">
    <h3>Example Title</h3>
  </a>
</div>
</body></html>`)
	got, err := parseGoogleResults(body, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if got[0].Title != "Example Title" {
		t.Errorf("title: %q", got[0].Title)
	}
	if got[0].URL != "https://example.com/page" {
		t.Errorf("url: %q", got[0].URL)
	}
}

func TestParseGoogleResults_DecodesRedirectURL(t *testing.T) {
	body := []byte(`<html><body>
<div class="g">
  <a href="/url?q=https://example.com/path%3Fkey%3Dval&amp;sa=U&amp;ved=abc">
    <h3>Title Two</h3>
  </a>
</div>
</body></html>`)
	got, err := parseGoogleResults(body, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d: %v", len(got), got)
	}
	if got[0].URL != "https://example.com/path?key=val" {
		t.Errorf("url: %q", got[0].URL)
	}
	if got[0].Title != "Title Two" {
		t.Errorf("title: %q", got[0].Title)
	}
}

func TestParseGoogleResults_SkipsGoogleOwnURLs(t *testing.T) {
	body := []byte(`<html><body>
<div class="g">
  <a href="https://www.google.com/search?q=foo">
    <h3>Google's own page</h3>
  </a>
</div>
<div class="g">
  <a href="https://example.com/real-result">
    <h3>Real Result</h3>
  </a>
</div>
</body></html>`)
	got, err := parseGoogleResults(body, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 result, got %d", len(got))
	}
	if got[0].URL != "https://example.com/real-result" {
		t.Errorf("url: %q", got[0].URL)
	}
}

func TestParseGoogleResults_RespectsMaxResults(t *testing.T) {
	body := []byte(`<html><body>
<div class="g"><a href="https://a.com"><h3>A</h3></a></div>
<div class="g"><a href="https://b.com"><h3>B</h3></a></div>
<div class="g"><a href="https://c.com"><h3>C</h3></a></div>
</body></html>`)
	got, err := parseGoogleResults(body, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 results (maxResults cap), got %d", len(got))
	}
}

func TestParseGoogleResults_EmptyHTML(t *testing.T) {
	body := []byte(`<html><body><div>No results</div></body></html>`)
	got, err := parseGoogleResults(body, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("expected 0 results, got %d", len(got))
	}
}

// ---- decodeGoogleHref unit tests ----

func TestDecodeGoogleHref_DirectHTTPS(t *testing.T) {
	got := decodeGoogleHref("https://example.com/page")
	if got != "https://example.com/page" {
		t.Errorf("got %q", got)
	}
}

func TestDecodeGoogleHref_RedirectURL(t *testing.T) {
	got := decodeGoogleHref("/url?q=https://example.com/result&sa=U")
	if got != "https://example.com/result" {
		t.Errorf("got %q", got)
	}
}

func TestDecodeGoogleHref_NonHTTP(t *testing.T) {
	if got := decodeGoogleHref("/maps/place/foo"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestDecodeGoogleHref_RedirectToNonHTTP(t *testing.T) {
	if got := decodeGoogleHref("/url?q=/relative/path"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// ---- isGoogleDomain unit tests ----

func TestIsGoogleDomain(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://www.google.com/search?q=foo", true},
		{"https://google.com/", true},
		{"https://google.co.uk/", true},
		{"https://translate.google.com/", true},
		{"https://example.com/", false},
		{"https://notgoogle.com/", false},
		{"https://mygoogle.com/", false},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			if got := isGoogleDomain(tc.url); got != tc.want {
				t.Errorf("isGoogleDomain(%q) = %v, want %v", tc.url, got, tc.want)
			}
		})
	}
}
