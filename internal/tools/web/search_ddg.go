package web

import (
	"context"
	"strings"
	"time"

	"github.com/kuhahalong/ddgsearch"
)

// ddgSearchFunc is swapped in tests to avoid live DuckDuckGo calls.
var ddgSearchFunc func(ctx context.Context, params *ddgsearch.SearchParams) (*ddgsearch.SearchResponse, error)

// runDDG asks DuckDuckGo. It is not in the default engine set: measured from a
// server, both the html and the lite endpoint answer every query with HTTP 202
// and a challenge page, which the library reports as "no results found". The
// backend stays available for an operator whose egress DuckDuckGo still serves,
// and the wrapper below makes sure that failure arrives as blocked rather than
// as an empty answer.
func runDDG(ctx context.Context, q Query, s Settings) ([]Result, error) {
	fn := ddgSearchFunc
	if fn == nil {
		fn = defaultDDGSearch
	}
	max := q.MaxResults
	if max <= 0 {
		max = 15
	}
	page := q.Page
	if page < 1 {
		page = 1
	}
	resp, err := fn(ctx, &ddgsearch.SearchParams{
		Query:      engineQuery(q),
		Page:       page,
		MaxResults: max,
	})
	if err != nil {
		// The library collapses the anti-bot interstitial into this message, so
		// it is the only signal available that the engine answered a challenge
		// rather than an empty index.
		if strings.Contains(strings.ToLower(err.Error()), "no results found") {
			return nil, blocked("no results returned (anti-bot interstitial?)")
		}
		return nil, err
	}
	if resp == nil {
		return nil, blocked("empty response")
	}
	out := make([]Result, 0, len(resp.Results))
	for _, r := range resp.Results {
		u := strings.TrimSpace(r.URL)
		if u == "" {
			continue
		}
		out = append(out, Result{
			Title:   strings.TrimSpace(r.Title),
			URL:     u,
			Snippet: strings.TrimSpace(r.Description),
		})
	}
	return out, nil
}

func defaultDDGSearch(ctx context.Context, params *ddgsearch.SearchParams) (*ddgsearch.SearchResponse, error) {
	cfg := &ddgsearch.Config{
		Timeout:    15 * time.Second,
		MaxRetries: 1,
	}
	c, err := ddgsearch.New(cfg)
	if err != nil {
		return nil, err
	}
	if params.Region == "" {
		params.Region = ddgsearch.RegionUS
	}
	if params.SafeSearch == "" {
		params.SafeSearch = ddgsearch.SafeSearchModerate
	}
	return c.Search(ctx, params)
}
