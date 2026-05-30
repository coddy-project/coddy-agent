package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
	"github.com/kuhahalong/ddgsearch"
)

// WebSearchTool returns the websearch built-in tool (DuckDuckGo + Google + Bing, parallel, merged).
func WebSearchTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "websearch",
			Description: "Search the public web. Queries DuckDuckGo, Google, and Bing simultaneously, merges results (DDG first, duplicates removed). Returns titles, URLs, and short snippets. Use page for pagination (about 10 results per page). If results are empty, try ONE differently-worded query and stop — never repeat the same query or call this tool more than twice for the same information need.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search query",
					},
					"page": map[string]interface{}{
						"type":        "integer",
						"description": "Result page number starting at 1 (default 1)",
					},
					"max_results": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum results to return for this page (default 15, cap 25)",
					},
				},
				"required": []string{"query"},
			},
		},
		RequiresPermission: false,
		Execute:            executeSearchWeb,
	}
}

type searchWebArgs struct {
	Query      string `json:"query"`
	Page       int    `json:"page"`
	MaxResults int    `json:"max_results"`
}

func executeSearchWeb(ctx context.Context, argsJSON string, _ *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[searchWebArgs](argsJSON)
	if err != nil {
		return "", err
	}
	q := strings.TrimSpace(args.Query)
	if q == "" {
		return "", fmt.Errorf("query is required")
	}
	page := args.Page
	if page < 1 {
		page = 1
	}
	maxRes := args.MaxResults
	if maxRes <= 0 {
		maxRes = 15
	}
	if maxRes > 25 {
		maxRes = 25
	}

	// Fire all three backends in parallel.
	type ddgOut struct {
		resp *ddgsearch.SearchResponse
		err  error
	}
	type googleOut struct {
		rows []googleResult
		err  error
	}
	type bingOut struct {
		rows []bingResult
		err  error
	}
	ddgCh := make(chan ddgOut, 1)
	googleCh := make(chan googleOut, 1)
	bingCh := make(chan bingOut, 1)

	ddgFn := ddgSearchFunc
	if ddgFn == nil {
		ddgFn = defaultDDGSearch
	}
	go func() {
		resp, err := ddgFn(ctx, &ddgsearch.SearchParams{
			Query:      q,
			Page:       page,
			MaxResults: maxRes,
		})
		ddgCh <- ddgOut{resp, err}
	}()

	gFn := googleSearchFunc
	if gFn == nil {
		gFn = defaultGoogleSearch
	}
	go func() {
		rows, err := gFn(ctx, q, page, maxRes)
		googleCh <- googleOut{rows, err}
	}()

	bFn := bingSearchFunc
	if bFn == nil {
		bFn = defaultBingSearch
	}
	go func() {
		rows, err := bFn(ctx, q, page, maxRes)
		bingCh <- bingOut{rows, err}
	}()

	ddgR := <-ddgCh
	googleR := <-googleCh
	bingR := <-bingCh

	// All three errored — nothing to return.
	if ddgR.err != nil && googleR.err != nil && bingR.err != nil {
		return "", fmt.Errorf("search failed (ddg: %w; google: %v; bing: %v)", ddgR.err, googleR.err, bingR.err)
	}

	// Merge with URL deduplication; order: DDG → Google → Bing.
	type row struct {
		Title       string `json:"title"`
		URL         string `json:"url"`
		Description string `json:"description"`
	}
	seen := make(map[string]bool)
	var rows []row

	if ddgR.err == nil && ddgR.resp != nil {
		for _, r := range ddgR.resp.Results {
			u := strings.TrimSpace(r.URL)
			if u != "" && !seen[u] {
				seen[u] = true
				rows = append(rows, row{
					Title:       strings.TrimSpace(r.Title),
					URL:         u,
					Description: strings.TrimSpace(r.Description),
				})
			}
		}
	}
	if googleR.err == nil {
		for _, r := range googleR.rows {
			if r.URL != "" && !seen[r.URL] {
				seen[r.URL] = true
				rows = append(rows, row{Title: r.Title, URL: r.URL, Description: r.Snippet})
			}
		}
	}
	if bingR.err == nil {
		for _, r := range bingR.rows {
			if r.URL != "" && !seen[r.URL] {
				seen[r.URL] = true
				rows = append(rows, row{Title: r.Title, URL: r.URL, Description: r.Snippet})
			}
		}
	}

	if len(rows) > maxRes {
		rows = rows[:maxRes]
	}

	out := struct {
		Query       string `json:"query"`
		Page        int    `json:"page"`
		HasMoreHint string `json:"has_more_hint,omitempty"`
		Results     []row  `json:"results"`
	}{
		Query:   q,
		Page:    page,
		Results: rows,
	}
	if len(rows) == 0 {
		out.HasMoreHint = "No results; try rephrasing the query."
	} else if len(rows) >= maxRes {
		out.HasMoreHint = "If you need more links, call websearch again with page incremented or a refined query."
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
