package web

import (
	"context"
	"time"

	"github.com/kuhahalong/ddgsearch"
)

// ddgSearchFunc is swapped in tests to avoid live DuckDuckGo calls.
var ddgSearchFunc func(ctx context.Context, params *ddgsearch.SearchParams) (*ddgsearch.SearchResponse, error)

func defaultDDGSearch(ctx context.Context, params *ddgsearch.SearchParams) (*ddgsearch.SearchResponse, error) {
	cfg := &ddgsearch.Config{
		Timeout:    25 * time.Second,
		MaxRetries: 2,
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
