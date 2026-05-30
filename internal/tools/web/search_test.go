package web

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kuhahalong/ddgsearch"
)

func TestSearchWebMockedBackend(t *testing.T) {
	oldDDG, oldGoogle, oldBing := ddgSearchFunc, googleSearchFunc, bingSearchFunc
	defer func() { ddgSearchFunc = oldDDG; googleSearchFunc = oldGoogle; bingSearchFunc = oldBing }()
	ddgSearchFunc = func(ctx context.Context, params *ddgsearch.SearchParams) (*ddgsearch.SearchResponse, error) {
		_ = ctx
		if params.Query != "qtest" {
			t.Fatalf("query: %q", params.Query)
		}
		return &ddgsearch.SearchResponse{
			Results: []ddgsearch.SearchResult{
				{Title: "A", URL: "https://a.example", Description: "d1"},
			},
		}, nil
	}
	googleSearchFunc = func(_ context.Context, _ string, _, _ int) ([]googleResult, error) {
		return nil, nil
	}
	bingSearchFunc = func(_ context.Context, _ string, _, _ int) ([]bingResult, error) {
		return nil, nil
	}
	tool := WebSearchTool()
	out, err := tool.Execute(context.Background(), `{"query":"qtest","page":1}`, nil)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Results []struct {
			Title string `json:"title"`
			URL   string `json:"url"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatal(err)
	}
	if len(parsed.Results) != 1 || parsed.Results[0].Title != "A" {
		t.Fatalf("unexpected payload: %s", out)
	}
}
