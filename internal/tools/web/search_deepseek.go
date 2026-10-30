package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// deepseekSearchFunc is swapped in tests to avoid a live API key.
var deepseekSearchFunc func(ctx context.Context, q Query, s Settings) ([]Result, error)

// deepseekEndpoint is DeepSeek's Search API. It is a variable so a test can
// point it at a local server and exercise the real request, parsing and
// classification rather than a stand-in for them.
var deepseekEndpoint = "https://api.deepseek.com/v1/tools/search"

// deepseekClient never follows a redirect. The endpoint is a constant of the
// API, not an address an operator vets per run, and a redirect is how an
// otherwise ordinary request reaches somewhere it was never checked against.
var deepseekClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// deepseekResponse is the shape of DeepSeek's Search API answer. The engine
// needs only the rows; the rest of the schema (rerank_score, summary,
// content, date, authority) is metadata a merged search row does not carry.
type deepseekResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Snippet string `json:"snippet"`
	} `json:"results"`
}

// deepseekError is the OpenAI-shaped error body DeepSeek answers a failure
// with, so the report can name what actually went wrong rather than only the
// status code.
type deepseekError struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}

// runDeepSeek asks DeepSeek's Search API. Unlike the keyless backends, there
// is no HTML page to fall back on: the API is the only way in, so a missing
// or refused key is reported as blocked rather than silently empty.
//
// The API has no pagination - its only bound is count, capped at 50 - so the
// Page of a Query is ignored: every call returns the top maxResults rows.
func runDeepSeek(ctx context.Context, q Query, s Settings) ([]Result, error) {
	if deepseekSearchFunc != nil {
		return deepseekSearchFunc(ctx, q, s)
	}
	key := strings.TrimSpace(s.DeepSeekAPIKey)
	if key == "" {
		return nil, blocked("tools.websearch.deepseek_api_key is not set")
	}
	count := q.MaxResults
	if count <= 0 {
		count = 10
	}
	if count > 50 {
		count = 50
	}
	body, err := json.Marshal(map[string]interface{}{
		"query": engineQuery(q),
		"count": count,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, deepseekEndpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := deepseekClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, blocked("deepseek api rejected the key (http %d)", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusPaymentRequired {
		return nil, blocked("deepseek api: the account has insufficient balance (http 402)")
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, blocked("deepseek api rate limit (http 429)")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := "http " + fmt.Sprint(resp.StatusCode)
		if buf, err := io.ReadAll(io.LimitReader(resp.Body, maxSERPBytes)); err == nil {
			var e deepseekError
			if json.Unmarshal(buf, &e) == nil && e.Error.Message != "" {
				msg = "deepseek api: " + e.Error.Message
			}
		}
		return nil, blocked("%s", msg)
	}
	var parsed deepseekResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, blocked("deepseek did not answer json (%v)", err)
	}
	max := q.MaxResults
	out := make([]Result, 0, len(parsed.Results))
	for _, r := range parsed.Results {
		u := strings.TrimSpace(r.URL)
		if u == "" {
			continue
		}
		out = append(out, Result{
			Title:   strings.TrimSpace(r.Title),
			URL:     u,
			Snippet: strings.TrimSpace(r.Snippet),
		})
		// The API honours count, but capping what it returned matches the
		// sibling engines: a response larger than asked for never reaches the
		// merge.
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out, nil
}
