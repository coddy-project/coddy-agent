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
	Lang  string `json:"lang"`
}

// docsLangProperty is the lang argument both documentation tools take.
func docsLangProperty(fallback string) map[string]interface{} {
	return map[string]interface{}{
		"type": "string",
		"enum": []interface{}{docs.English, docs.Russian},
		"description": "The language of the pages: \"en\" or \"ru\". Leave it out to follow the person's interface, " + fallback +
			"; pass \"ru\" when the person writes Russian. References are the same in every language.",
	}
}

// docsLang is the language a documentation tool reads in: the call's, then
// the surface's, then the fallback's (the query's script for a search).
func docsLang(arg string, env *tooling.Env, fallback string) string {
	if strings.TrimSpace(arg) != "" {
		return docs.Lang(arg)
	}
	if env != nil && strings.TrimSpace(env.Lang) != "" {
		return docs.Lang(env.Lang)
	}
	return fallback
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
						"description": "Words to look for, in English or in Russian, e.g. \"telegram proxy\", \"max_turns\", \"режимы разрешений\". A word may be cut short: \"config\" also finds configuration.",
					},
					"lang": docsLangProperty("else the language of the query"),
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

func executeDocsSearch(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
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
	lib, err := docs.For(docsLang(args.Lang, env, docs.LangOfText(query)))
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
