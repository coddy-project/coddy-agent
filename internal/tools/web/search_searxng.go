package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// searxngSearchFunc is swapped in tests to avoid a live instance.
var searxngSearchFunc func(ctx context.Context, q Query, s Settings) ([]Result, error)

// searxngClient never follows a redirect. The operator vets the address they
// configured; they cannot vet where it forwards to, and a redirect is the one
// way an otherwise ordinary LAN address reaches somewhere it was never checked
// against - the cloud metadata service, most of all.
var searxngClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// searxngResponse is the shape of a SearXNG JSON answer.
type searxngResponse struct {
	Results []struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	} `json:"results"`
}

// runSearXNG asks the operator's own SearXNG instance through its JSON API.
// A self-hosted aggregator is the durable answer to every failure the keyless
// backends have: it is not rate-limited against its owner, it is not served a
// decoy, and it is the operator's own infrastructure rather than a page being
// scraped.
func runSearXNG(ctx context.Context, q Query, s Settings) ([]Result, error) {
	if searxngSearchFunc != nil {
		return searxngSearchFunc(ctx, q, s)
	}
	base := strings.TrimRight(strings.TrimSpace(s.SearXNGURL), "/")
	if base == "" {
		return nil, blocked("tools.websearch.searxng_url is not set")
	}
	page := q.Page
	if page < 1 {
		page = 1
	}
	endpoint := fmt.Sprintf("%s/search?q=%s&format=json&pageno=%d",
		base, url.QueryEscape(engineQuery(q)), page)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", browserUserAgent)
	resp, err := searxngClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, blocked("searxng redirected to %q; point searxng_url at the instance itself",
			resp.Header.Get("Location"))
	}
	if resp.StatusCode == http.StatusForbidden {
		// A SearXNG instance serves the JSON format only when its settings
		// enable it; the default configuration answers 403 here.
		return nil, blocked("searxng refused the json format (http 403; enable it in settings.yml)")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, blocked("searxng http %d", resp.StatusCode)
	}
	var parsed searxngResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, blocked("searxng did not answer json (%v)", err)
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
			Snippet: strings.TrimSpace(r.Content),
		})
		if max > 0 && len(out) >= max {
			break
		}
	}
	return out, nil
}
