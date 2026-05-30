package web

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/kuhahalong/ddgsearch"
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

// ---- parallel merge integration tests (DDG + Google + Bing) ----

func noopGoogle(_ context.Context, _ string, _, _ int) ([]googleResult, error) { return nil, nil }

func TestSearchMerge_AllThreeReturnResults_MergedWithDedup(t *testing.T) {
	ddgCalled, googleCalled, bingCalled := false, false, false

	oldDDG, oldGoogle, oldBing := ddgSearchFunc, googleSearchFunc, bingSearchFunc
	defer func() { ddgSearchFunc = oldDDG; googleSearchFunc = oldGoogle; bingSearchFunc = oldBing }()

	ddgSearchFunc = func(_ context.Context, _ *ddgsearch.SearchParams) (*ddgsearch.SearchResponse, error) {
		ddgCalled = true
		return &ddgsearch.SearchResponse{Results: []ddgsearch.SearchResult{
			{Title: "DDG Only", URL: "https://ddg-only.example.com"},
			{Title: "Shared", URL: "https://shared.example.com"},
		}}, nil
	}
	googleSearchFunc = func(_ context.Context, _ string, _, _ int) ([]googleResult, error) {
		googleCalled = true
		return []googleResult{
			{Title: "Shared (Google)", URL: "https://shared.example.com"},
			{Title: "Google Only", URL: "https://google-only.example.com"},
		}, nil
	}
	bingSearchFunc = func(_ context.Context, _ string, _, _ int) ([]bingResult, error) {
		bingCalled = true
		return []bingResult{
			{Title: "Bing Only", URL: "https://bing-only.example.com"},
		}, nil
	}

	tool := WebSearchTool()
	out, err := tool.Execute(context.Background(), `{"query":"test","max_results":10}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ddgCalled || !googleCalled || !bingCalled {
		t.Errorf("all three backends should be called: ddg=%v google=%v bing=%v", ddgCalled, googleCalled, bingCalled)
	}

	var parsed struct {
		Results []struct{ URL string } `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	// expect 4: DDG Only, Shared (DDG wins dedup), Google Only, Bing Only
	if len(parsed.Results) != 4 {
		t.Fatalf("expected 4 results after dedup, got %d: %s", len(parsed.Results), out)
	}
	urls := make([]string, len(parsed.Results))
	for i, r := range parsed.Results {
		urls[i] = r.URL
	}
	want := []string{
		"https://ddg-only.example.com",
		"https://shared.example.com",
		"https://google-only.example.com",
		"https://bing-only.example.com",
	}
	for i, w := range want {
		if urls[i] != w {
			t.Errorf("result[%d]: got %q, want %q", i, urls[i], w)
		}
	}
}

func TestSearchMerge_DDGEmptyBingHasResults(t *testing.T) {
	oldDDG, oldGoogle, oldBing := ddgSearchFunc, googleSearchFunc, bingSearchFunc
	defer func() { ddgSearchFunc = oldDDG; googleSearchFunc = oldGoogle; bingSearchFunc = oldBing }()

	ddgSearchFunc = func(_ context.Context, _ *ddgsearch.SearchParams) (*ddgsearch.SearchResponse, error) {
		return &ddgsearch.SearchResponse{NoResults: true}, nil
	}
	googleSearchFunc = noopGoogle
	bingSearchFunc = func(_ context.Context, _ string, _, _ int) ([]bingResult, error) {
		return []bingResult{{Title: "Bing Result", URL: "https://bing-result.example.com"}}, nil
	}

	out, err := WebSearchTool().Execute(context.Background(), `{"query":"test"}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Results []struct{ URL string } `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Results) != 1 || parsed.Results[0].URL != "https://bing-result.example.com" {
		t.Errorf("expected Bing result, got: %s", out)
	}
}

func TestSearchMerge_BingAndGoogleEmptyDDGHasResults(t *testing.T) {
	oldDDG, oldGoogle, oldBing := ddgSearchFunc, googleSearchFunc, bingSearchFunc
	defer func() { ddgSearchFunc = oldDDG; googleSearchFunc = oldGoogle; bingSearchFunc = oldBing }()

	ddgSearchFunc = func(_ context.Context, _ *ddgsearch.SearchParams) (*ddgsearch.SearchResponse, error) {
		return &ddgsearch.SearchResponse{Results: []ddgsearch.SearchResult{
			{Title: "DDG Result", URL: "https://ddg.example.com"},
		}}, nil
	}
	googleSearchFunc = noopGoogle
	bingSearchFunc = func(_ context.Context, _ string, _, _ int) ([]bingResult, error) {
		return nil, fmt.Errorf("bing: timeout")
	}

	out, err := WebSearchTool().Execute(context.Background(), `{"query":"test"}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Results []struct{ URL string } `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Results) != 1 || parsed.Results[0].URL != "https://ddg.example.com" {
		t.Errorf("expected DDG result, got: %s", out)
	}
}

func TestSearchMerge_AllFail_ReturnsError(t *testing.T) {
	oldDDG, oldGoogle, oldBing := ddgSearchFunc, googleSearchFunc, bingSearchFunc
	defer func() { ddgSearchFunc = oldDDG; googleSearchFunc = oldGoogle; bingSearchFunc = oldBing }()

	ddgSearchFunc = func(_ context.Context, _ *ddgsearch.SearchParams) (*ddgsearch.SearchResponse, error) {
		return nil, fmt.Errorf("ddg: unavailable")
	}
	googleSearchFunc = func(_ context.Context, _ string, _, _ int) ([]googleResult, error) {
		return nil, fmt.Errorf("google: blocked")
	}
	bingSearchFunc = func(_ context.Context, _ string, _, _ int) ([]bingResult, error) {
		return nil, fmt.Errorf("bing: blocked")
	}

	_, err := WebSearchTool().Execute(context.Background(), `{"query":"test"}`, nil)
	if err == nil {
		t.Error("expected error when all backends fail")
	}
}

func TestSearchMerge_AllEmpty_ReturnsHint(t *testing.T) {
	oldDDG, oldGoogle, oldBing := ddgSearchFunc, googleSearchFunc, bingSearchFunc
	defer func() { ddgSearchFunc = oldDDG; googleSearchFunc = oldGoogle; bingSearchFunc = oldBing }()

	ddgSearchFunc = func(_ context.Context, _ *ddgsearch.SearchParams) (*ddgsearch.SearchResponse, error) {
		return &ddgsearch.SearchResponse{NoResults: true}, nil
	}
	googleSearchFunc = noopGoogle
	bingSearchFunc = func(_ context.Context, _ string, _, _ int) ([]bingResult, error) {
		return nil, nil
	}

	out, err := WebSearchTool().Execute(context.Background(), `{"query":"test"}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Results     []struct{ URL string } `json:"results"`
		HasMoreHint string                 `json:"has_more_hint"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Results) != 0 {
		t.Errorf("expected 0 results, got %d", len(parsed.Results))
	}
	if parsed.HasMoreHint == "" {
		t.Error("expected has_more_hint when no results")
	}
}

func TestSearchMerge_CapsAtMaxResults(t *testing.T) {
	oldDDG, oldGoogle, oldBing := ddgSearchFunc, googleSearchFunc, bingSearchFunc
	defer func() { ddgSearchFunc = oldDDG; googleSearchFunc = oldGoogle; bingSearchFunc = oldBing }()

	ddgSearchFunc = func(_ context.Context, _ *ddgsearch.SearchParams) (*ddgsearch.SearchResponse, error) {
		results := make([]ddgsearch.SearchResult, 8)
		for i := range results {
			results[i] = ddgsearch.SearchResult{Title: fmt.Sprintf("DDG %d", i), URL: fmt.Sprintf("https://ddg-%d.example.com", i)}
		}
		return &ddgsearch.SearchResponse{Results: results}, nil
	}
	googleSearchFunc = noopGoogle
	bingSearchFunc = func(_ context.Context, _ string, _, _ int) ([]bingResult, error) {
		results := make([]bingResult, 8)
		for i := range results {
			results[i] = bingResult{Title: fmt.Sprintf("Bing %d", i), URL: fmt.Sprintf("https://bing-%d.example.com", i)}
		}
		return results, nil
	}

	out, err := WebSearchTool().Execute(context.Background(), `{"query":"test","max_results":10}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Results []struct{ URL string } `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Results) > 10 {
		t.Errorf("expected at most 10 results, got %d", len(parsed.Results))
	}
}
