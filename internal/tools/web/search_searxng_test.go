package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSearXNGParsesItsJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("format"); got != "json" {
			t.Errorf("format: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[
{"title":"Context package","url":"https://pkg.go.dev/context","content":"Package context."},
{"title":"Go blog","url":"https://go.dev/blog/context","content":"Share by communicating."}]}`))
	}))
	defer srv.Close()

	rows, err := runSearXNG(context.Background(),
		Query{Text: "golang context", Page: 1, MaxResults: 10},
		Settings{SearXNGURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].URL != "https://pkg.go.dev/context" {
		t.Fatalf("rows: %+v", rows)
	}
	if rows[0].Snippet != "Package context." {
		t.Errorf("snippet: %q", rows[0].Snippet)
	}
}

// TestSearXNGWithoutTheJSONFormatIsBlockedWithTheFix names the mistake an
// operator actually makes: SearXNG's default settings.yml does not serve JSON.
func TestSearXNGWithoutTheJSONFormatIsBlockedWithTheFix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := runSearXNG(context.Background(), Query{Text: "go"}, Settings{SearXNGURL: srv.URL})
	reason, isBlocked := asBlocked(err)
	if !isBlocked || !strings.Contains(reason, "settings.yml") {
		t.Fatalf("the answer should name the fix, got %v", err)
	}
}

// TestSearXNGDoesNotFollowARedirect is the hardening a reviewer asked for: the
// operator vets the address they configured, not where it forwards to, and a
// redirect is how an ordinary LAN address reaches the metadata service.
func TestSearXNGDoesNotFollowARedirect(t *testing.T) {
	var reached bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/search", http.StatusFound)
	}))
	defer srv.Close()

	_, err := runSearXNG(context.Background(), Query{Text: "go"}, Settings{SearXNGURL: srv.URL})
	if reached {
		t.Fatal("the redirect was followed to an address nobody vetted")
	}
	reason, isBlocked := asBlocked(err)
	if !isBlocked || !strings.Contains(reason, "redirect") {
		t.Fatalf("a redirect should be reported, got %v", err)
	}
}

func TestSearXNGWithoutAnAddressIsBlockedNotEmpty(t *testing.T) {
	_, err := runSearXNG(context.Background(), Query{Text: "go"}, Settings{})
	reason, isBlocked := asBlocked(err)
	if !isBlocked || !strings.Contains(reason, "searxng_url") {
		t.Fatalf("got %v", err)
	}
}

func TestSearXNGNonJSONAnswerIsBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body>a search page, not json</body></html>`))
	}))
	defer srv.Close()

	_, err := runSearXNG(context.Background(), Query{Text: "go"}, Settings{SearXNGURL: srv.URL})
	if _, isBlocked := asBlocked(err); !isBlocked {
		t.Fatalf("got %v", err)
	}
}

func TestSearXNGRespectsMaxResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		var b strings.Builder
		b.WriteString(`{"results":[`)
		for i := 0; i < 20; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(`{"title":"T","url":"https://example.com/` + string(rune('a'+i)) + `","content":"c"}`)
		}
		b.WriteString(`]}`)
		_, _ = w.Write([]byte(b.String()))
	}))
	defer srv.Close()

	rows, err := runSearXNG(context.Background(),
		Query{Text: "go", MaxResults: 5}, Settings{SearXNGURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(rows))
	}
}
