package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// searchDescription is what the model reads before it decides to search. The
// text it replaced ended with an instruction never to call the tool more than
// twice for one information need, which taught the model to abandon a subject
// after two attempts at a tool that was failing on every one of them.
const searchDescription = `Search the public web across several engines at once and return merged results: title, URL and a short snippet each.

The answer carries an "engines" block reporting what every backend did: "ok" with a count, "empty" when it answered a result page with nothing on it, or "blocked" with a reason when it answered a challenge, an unreadable layout, or a set of results unrelated to the query. Read it before concluding anything:
- results present: use them, and call webfetch on the ones worth reading in full - a snippet is not an answer;
- zero results with every engine "ok" or "empty": the web really has nothing under those words. Search again with different words - broader, or the terms the sources themselves would use - rather than repeating the query;
- engines "blocked": search from this machine is degraded, not the subject. Say so instead of concluding the subject does not exist.

Use "page" to go further down the same result list, "site" to restrict to one domain, "max_results" to ask for fewer or more rows.`

// WebSearchTool returns the websearch built-in tool.
func WebSearchTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "websearch",
			Description: searchDescription,
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{
						"type":        "string",
						"description": "Search query in the language the answer is likely written in",
					},
					"page": map[string]interface{}{
						"type":        "integer",
						"description": "Result page number starting at 1 (default 1)",
					},
					"max_results": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum results to return for this page (default 15, cap 25)",
					},
					"site": map[string]interface{}{
						"type":        "string",
						"description": "Restrict results to one domain, e.g. \"go.dev\" or \"github.com\"",
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
	Site       string `json:"site"`
}

// searchRow is one merged result as the model receives it.
type searchRow struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Description string `json:"description,omitempty"`
	Source      string `json:"source"`
}

// searchOutput is the tool's answer. Engines is not optional: it is what makes
// an empty result list readable, by saying whether the engines were healthy.
type searchOutput struct {
	Query   string      `json:"query"`
	Page    int         `json:"page"`
	Engines []Report    `json:"engines"`
	Results []searchRow `json:"results"`
	Hint    string      `json:"hint,omitempty"`
}

func executeSearchWeb(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
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

	settings := SettingsFromEnv(env)
	names := settings.ResolvedEngines()
	query := Query{Text: q, Page: page, MaxResults: maxRes, Site: strings.TrimSpace(args.Site)}

	runs := runEngines(ctx, names, query, settings)

	seen := make(map[string]bool)
	rows := make([]searchRow, 0, maxRes)
	reports := make([]Report, 0, len(runs))
	healthy := 0
	for i := range runs {
		run := &runs[i]
		kept := 0
		for _, r := range run.rows {
			url := strings.TrimSpace(r.URL)
			if url == "" {
				continue
			}
			key := dedupKey(url)
			if seen[key] {
				continue
			}
			seen[key] = true
			if len(rows) >= maxRes {
				continue
			}
			kept++
			rows = append(rows, searchRow{
				Title:       strings.TrimSpace(r.Title),
				URL:         url,
				Description: clipSnippet(r.Snippet, settings.SnippetLimit()),
				Source:      run.name,
			})
		}
		// The report counts what this engine contributed to the merged answer,
		// not what it parsed: a row another engine already supplied is not a
		// second result, and saying otherwise makes the counts unreadable.
		if run.report.Status == OutcomeOK {
			run.report.Results = kept
			healthy++
		}
		if run.report.Status == OutcomeEmpty {
			healthy++
		}
		reports = append(reports, run.report)
	}

	// Every engine turned away is a failure of the search, not an answer about
	// the world. Reporting it as an error rather than as an empty result list
	// is what stops the model concluding that the subject does not exist; the
	// message names each engine so the operator can see what to fix.
	if healthy == 0 && len(runs) > 0 {
		return "", fmt.Errorf("every search engine was unavailable (%s). Search from this machine is degraded, not the subject: say so rather than concluding nothing exists. The operator can configure tools.websearch.searxng_url (a self-hosted SearXNG) or a Brave Search API key (tools.websearch.brave_api_key, or the BRAVE_API_KEY environment variable) for a reliable backend",
			describeFailures(runs))
	}

	out := searchOutput{Query: q, Page: page, Engines: reports, Results: rows}
	switch {
	case len(rows) == 0:
		out.Hint = "Every engine answered, and none of them had anything under these words. Search again with different wording rather than repeating this query."
	case len(rows) >= maxRes:
		out.Hint = "More results are available: call websearch again with page incremented, or narrow the query."
	}
	if blockedNames := blockedEngines(runs); len(blockedNames) > 0 {
		out.Hint = strings.TrimSpace(out.Hint + " Engines unavailable for this call: " + strings.Join(blockedNames, ", ") + ".")
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// describeFailures renders one line per engine for the all-blocked error.
func describeFailures(runs []engineRun) string {
	parts := make([]string, 0, len(runs))
	for _, r := range runs {
		reason := r.report.Reason
		if reason == "" {
			reason = string(r.report.Status)
		}
		parts = append(parts, r.name+": "+reason)
	}
	return strings.Join(parts, "; ")
}

// blockedEngines names the engines that contributed nothing because something
// stood in the way, so a partial answer still says what it is missing.
func blockedEngines(runs []engineRun) []string {
	var out []string
	for _, r := range runs {
		if r.report.Status == OutcomeBlocked || r.report.Status == OutcomeError {
			out = append(out, r.name)
		}
	}
	return out
}

// SettingsFromEnv reads the resolved tools.websearch section off the tool
// environment, falling back to the built-in defaults when nothing was wired.
// The conversion is direct because tooling.WebSearchSettings carries the same
// fields in the same order: the data lives on the environment, the behaviour
// stays in this package.
func SettingsFromEnv(env *tooling.Env) Settings {
	if env == nil || env.WebSearch == nil {
		return Settings{}
	}
	return Settings(*env.WebSearch)
}
