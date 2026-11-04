package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pointDeepSeekAt runs the DeepSeek backend against a local handler, so the
// tests exercise the real request, parsing and classification.
func pointDeepSeekAt(t *testing.T, h http.HandlerFunc) func() {
	t.Helper()
	srv := httptest.NewServer(h)
	old := deepseekEndpoint
	deepseekEndpoint = srv.URL
	return func() {
		deepseekEndpoint = old
		srv.Close()
	}
}

func TestDeepSeekParsesItsJSON(t *testing.T) {
	restore := pointDeepSeekAt(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method: %s", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("content type: %q", got)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("authorization: %q", got)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("body not json: %v", err)
		}
		if body["query"] != "golang context" {
			t.Errorf("query: %v", body["query"])
		}
		if body["count"] != float64(10) {
			t.Errorf("count: %v", body["count"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[
{"title":"Context package","url":"https://pkg.go.dev/context","snippet":"Package context."},
{"title":"Go blog","url":"https://go.dev/blog/context","snippet":"Share by communicating."}]}`))
	})
	defer restore()

	rows, err := runDeepSeek(context.Background(),
		Query{Text: "golang context", Page: 1, MaxResults: 10},
		Settings{DeepSeekAPIKey: "test-key"})
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

func TestDeepSeekWithoutAKeyIsBlockedNotEmpty(t *testing.T) {
	_, err := runDeepSeek(context.Background(), Query{Text: "go"}, Settings{})
	reason, isBlocked := asBlocked(err)
	if !isBlocked || !strings.Contains(reason, "deepseek_api_key") {
		t.Fatalf("a missing key must be reported, got %v", err)
	}
}

func TestDeepSeekRejectedKeyIsBlocked(t *testing.T) {
	restore := pointDeepSeekAt(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	})
	defer restore()

	_, err := runDeepSeek(context.Background(), Query{Text: "go"}, Settings{DeepSeekAPIKey: "bad"})
	reason, isBlocked := asBlocked(err)
	if !isBlocked || !strings.Contains(reason, "key") {
		t.Fatalf("a rejected key must be reported, got %v", err)
	}
}

// TestDeepSeekForbiddenIsBlocked pins the second status code of the same
// branch: a key the API accepts but refuses for this request (403) reports the
// same way as 401 rather than as an empty answer.
func TestDeepSeekForbiddenIsBlocked(t *testing.T) {
	restore := pointDeepSeekAt(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	defer restore()

	_, err := runDeepSeek(context.Background(), Query{Text: "go"}, Settings{DeepSeekAPIKey: "key"})
	reason, isBlocked := asBlocked(err)
	if !isBlocked || !strings.Contains(reason, "key") {
		t.Fatalf("a 403 must be reported, got %v", err)
	}
}

func TestDeepSeekInsufficientBalanceIsBlocked(t *testing.T) {
	restore := pointDeepSeekAt(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
	})
	defer restore()

	_, err := runDeepSeek(context.Background(), Query{Text: "go"}, Settings{DeepSeekAPIKey: "key"})
	reason, isBlocked := asBlocked(err)
	if !isBlocked || !strings.Contains(reason, "balance") {
		t.Fatalf("an unpaid account must be reported, got %v", err)
	}
}

func TestDeepSeekRateLimitIsBlocked(t *testing.T) {
	restore := pointDeepSeekAt(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	defer restore()

	_, err := runDeepSeek(context.Background(), Query{Text: "go"}, Settings{DeepSeekAPIKey: "key"})
	reason, isBlocked := asBlocked(err)
	if !isBlocked || !strings.Contains(reason, "429") {
		t.Fatalf("a rate limit must be reported, got %v", err)
	}
}

func TestDeepSeekErrorMessageIsNamedInsteadOfTheCode(t *testing.T) {
	restore := pointDeepSeekAt(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"'count' must be between 1 and 50","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`))
	})
	defer restore()

	_, err := runDeepSeek(context.Background(), Query{Text: "go"}, Settings{DeepSeekAPIKey: "key"})
	reason, isBlocked := asBlocked(err)
	if !isBlocked || !strings.Contains(reason, "must be between 1 and 50") {
		t.Fatalf("the API's message should be reported, got %v", err)
	}
}

func TestDeepSeekNonJSONAnswerIsBlocked(t *testing.T) {
	restore := pointDeepSeekAt(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`<html><body>a search page, not json</body></html>`))
	})
	defer restore()

	_, err := runDeepSeek(context.Background(), Query{Text: "go"}, Settings{DeepSeekAPIKey: "key"})
	if _, isBlocked := asBlocked(err); !isBlocked {
		t.Fatalf("a non-json answer must be reported as blocked, got %v", err)
	}
}

func TestDeepSeekRespectsMaxResultsAndCapsCount(t *testing.T) {
	for _, tc := range []struct {
		maxResults int
		wantCount  int
	}{
		{0, 10},
		{5, 5},
		{200, 50},
	} {
		var gotCount float64
		restore := pointDeepSeekAt(t, func(w http.ResponseWriter, r *http.Request) {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if c, ok := body["count"].(float64); ok {
				gotCount = c
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"results":[]}`))
		})
		_, err := runDeepSeek(context.Background(),
			Query{Text: "go", MaxResults: tc.maxResults}, Settings{DeepSeekAPIKey: "key"})
		restore()
		if err != nil {
			t.Fatal(err)
		}
		if gotCount != float64(tc.wantCount) {
			t.Errorf("maxResults %d: count sent = %v, want %d", tc.maxResults, gotCount, tc.wantCount)
		}
	}
}

// TestDeepSeekFoldsTheSiteRestriction pins that the engine sends the query
// through engineQuery, so the tool's site argument reaches DeepSeek, which
// understands its own site: operator rather than needing a filter of its own.
func TestDeepSeekFoldsTheSiteRestriction(t *testing.T) {
	var gotQuery string
	restore := pointDeepSeekAt(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotQuery, _ = body["query"].(string)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	})
	defer restore()

	if _, err := runDeepSeek(context.Background(),
		Query{Text: "golang context", Site: "https://go.dev/", MaxResults: 5},
		Settings{DeepSeekAPIKey: "key"}); err != nil {
		t.Fatal(err)
	}
	if gotQuery != "golang context site:go.dev" {
		t.Fatalf("query: %q", gotQuery)
	}
}

// TestDeepSeekNonJSONErrorBodyFallsBackToTheCode: an error the API answers in
// HTML still names the status rather than failing to parse, so the report reads
// "http 500" instead of an empty reason.
func TestDeepSeekNonJSONErrorBodyFallsBackToTheCode(t *testing.T) {
	restore := pointDeepSeekAt(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`<html><body>internal server error</body></html>`))
	})
	defer restore()

	_, err := runDeepSeek(context.Background(), Query{Text: "go"}, Settings{DeepSeekAPIKey: "key"})
	reason, isBlocked := asBlocked(err)
	if !isBlocked || !strings.Contains(reason, "500") {
		t.Fatalf("a non-json 500 must be reported by its code, got %v", err)
	}
}

// Later pages must not repeat the first page or spend another API request.
func TestDeepSeekRejectsLaterPages(t *testing.T) {
	var calls int
	restore := pointDeepSeekAt(t, func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Go","url":"https://go.dev","snippet":"The site."}]}`))
	})
	defer restore()

	first, err := runDeepSeek(context.Background(), Query{Text: "go", Page: 1, MaxResults: 3}, Settings{DeepSeekAPIKey: "key"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runDeepSeek(context.Background(), Query{Text: "go", Page: 3, MaxResults: 3}, Settings{DeepSeekAPIKey: "key"})
	if reason, ok := asBlocked(err); !ok || !strings.Contains(reason, "pagination") {
		t.Fatalf("later page should explain the limitation, got %v", err)
	}
	if calls != 1 || len(first) != 1 {
		t.Fatalf("later page should not call API again: calls=%d first=%+v", calls, first)
	}
}

func TestDeepSeekBoundsSuccessfulResponses(t *testing.T) {
	restore := pointDeepSeekAt(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"results":[],"content":"%s"}`, strings.Repeat("x", maxSERPBytes))
	})
	defer restore()
	_, err := runDeepSeek(context.Background(), Query{Text: "go"}, Settings{DeepSeekAPIKey: "key"})
	if reason, ok := asBlocked(err); !ok || !strings.Contains(reason, "limit") {
		t.Fatalf("oversized response should be blocked, got %v", err)
	}
}

func TestDeepSeekRejectsMalformedSuccessfulEnvelopes(t *testing.T) {
	for _, body := range []string{`{}`, `null`, `{"results":null}`, `{"results":[null]}`, `{"results":[{}]}`, `{"error":{"message":"upstream failed"}}`, `{"results":[]} {}`} {
		t.Run(body, func(t *testing.T) {
			restore := pointDeepSeekAt(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(body))
			})
			defer restore()
			_, err := runDeepSeek(context.Background(), Query{Text: "go"}, Settings{DeepSeekAPIKey: "key"})
			if _, ok := asBlocked(err); !ok {
				t.Fatalf("malformed success should be blocked, got %v", err)
			}
		})
	}
}

func TestDeepSeekLaterPageDoesNotCrowdOutOtherEngines(t *testing.T) {
	stubEngines(t)
	deepseekSearchFunc = nil
	restore := pointDeepSeekAt(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"title":"Go","url":"https://go.dev"}]}`))
	})
	defer restore()
	braveSearchFunc = func(_ context.Context, q Query, _ Settings) ([]Result, error) {
		return []Result{{Title: "Go next page", URL: fmt.Sprintf("https://example.com/go/%d", q.Page)}}, nil
	}
	env := envWith(EngineDeepSeek, EngineBrave)
	env.WebSearch.DeepSeekAPIKey = "key"
	out := runSearch(t, env, `{"query":"go","page":2,"max_results":1}`)
	if len(out.Results) != 1 || out.Results[0].Source != EngineBrave {
		t.Fatalf("first-page rows displaced later-page results: %+v", out.Results)
	}
	if rep := reportFor(t, out, EngineDeepSeek); rep.Status != OutcomeBlocked || !strings.Contains(rep.Reason, "pagination") {
		t.Fatalf("pagination limitation not reported: %+v", rep)
	}
}

func TestDeepSeekOnlySearchDoesNotOfferPagination(t *testing.T) {
	stubEngines(t)
	deepseekSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		return []Result{{Title: "Go", URL: "https://go.dev"}}, nil
	}
	for _, engines := range [][]string{{EngineDeepSeek}, {EngineDeepSeek, EngineBrave}} {
		out := runSearch(t, envWith(engines...), `{"query":"go","max_results":1}`)
		if strings.Contains(out.Hint, "page incremented") || !strings.Contains(out.Hint, "pagination") {
			t.Fatalf("engines %v: hint promises unavailable pagination: %q", engines, out.Hint)
		}
	}
}

func TestDeepSeekChecksQueryLengthAfterSiteFolding(t *testing.T) {
	for _, tc := range []struct {
		text, site string
		wantCall   bool
	}{
		{strings.Repeat("я", 128), "", true},
		{strings.Repeat("я", 129), "", false},
		{strings.Repeat("a", 120), "go.dev", false},
		{"   ", "", false},
	} {
		calls := 0
		restore := pointDeepSeekAt(t, func(w http.ResponseWriter, _ *http.Request) {
			calls++
			_, _ = w.Write([]byte(`{"results":[]}`))
		})
		_, err := runDeepSeek(context.Background(), Query{Text: tc.text, Site: tc.site}, Settings{DeepSeekAPIKey: "key"})
		restore()
		if tc.wantCall {
			if err != nil || calls != 1 {
				t.Fatalf("valid Unicode query: calls=%d err=%v", calls, err)
			}
		} else if reason, ok := asBlocked(err); !ok || !strings.Contains(reason, "128") || calls != 0 {
			t.Fatalf("invalid query should be rejected locally: calls=%d err=%v", calls, err)
		}
	}
}
