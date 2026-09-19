package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/docs"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// ToolDocsSearch is the name of the search over the built-in documentation.
const ToolDocsSearch = "coddy_docs_search"

const (
	docsSearchDefaultLimit = 8
	docsSearchMaxLimit     = 20
)

type docsSearchArgs struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

// DocsSearchTool searches Coddy's own documentation, embedded in the binary.
// It is read-only, needs no permission and is offered in every mode.
func DocsSearchTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolDocsSearch,
			Description: "Search Coddy's own documentation, built into this binary: the pages of https://coddy.dev/docs as they are for this exact version. " +
				"Use it when the user asks how Coddy works, how to install, configure or run it, or what one of its features, tools, commands, keys or config.yaml settings does, " +
				"and before answering such a question from memory. Returns the best matching sections, each with a reference to pass to coddy_docs_read.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Words to look for, in English, e.g. \"telegram proxy\", \"max_turns\", \"permission modes\". A word may be cut short: \"config\" also finds configuration.",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": fmt.Sprintf("How many sections to return, %d by default, at most %d.", docsSearchDefaultLimit, docsSearchMaxLimit),
					},
				},
				"required": []interface{}{"query"},
			},
		},
		Execute: executeDocsSearch,
	}
}

func executeDocsSearch(_ context.Context, argsJSON string, _ *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[docsSearchArgs](argsJSON)
	if err != nil {
		return "", err
	}
	query := strings.TrimSpace(args.Query)
	if query == "" {
		return "", fmt.Errorf("%s: query is required", ToolDocsSearch)
	}
	limit := args.Limit
	if limit <= 0 {
		limit = docsSearchDefaultLimit
	}
	limit = min(limit, docsSearchMaxLimit)
	lib, err := docs.Default()
	if err != nil {
		return "", fmt.Errorf("%s: %w", ToolDocsSearch, err)
	}
	hits := lib.Search(query, limit)
	if len(hits) == 0 {
		return fmt.Sprintf("No section of the Coddy %s documentation matches %q. Try other or fewer words, or call %s without a page for the contents.", lib.Version, query, ToolDocsRead), nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Coddy %s documentation: %d sections for %q, best first. Read one with %s and its reference.\n\n", lib.Version, len(hits), query, ToolDocsRead)
	b.WriteString(docs.FormatHits(hits))
	return b.String(), nil
}
