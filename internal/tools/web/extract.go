package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
	"github.com/go-shiori/go-readability"
)

const maxFetchHTMLBytes = 4 << 20

// WebFetchTool returns the webfetch built-in tool (fetch URL as markdown).
func WebFetchTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "webfetch",
			Description: "Download a public http(s) page and return main article text as Markdown (readability extraction). Respects size limits. Blocked for private networks and localhost (SSRF guard).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"url": map[string]interface{}{
						"type":        "string",
						"description": "Absolute http or https URL",
					},
					"timeout_seconds": map[string]interface{}{
						"type":        "integer",
						"description": "HTTP timeout in seconds (default 30, max 120)",
					},
					"max_chars": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum markdown characters to return (default 120000)",
					},
				},
				"required": []string{"url"},
			},
		},
		RequiresPermission: false,
		Execute:            executeExtractPageContent,
	}
}

type extractPageArgs struct {
	URL            string `json:"url"`
	TimeoutSeconds int    `json:"timeout_seconds"`
	MaxChars       int    `json:"max_chars"`
}

// fetchGuard vets every address webfetch contacts, the page and each redirect
// on the way to it. It is a variable so tests can reach a local server.
var fetchGuard = func(ctx context.Context, u *url.URL) error {
	_, err := ValidateFetchURL(ctx, u.String())
	return err
}

// executeExtractPageContent is http_request with a narrower contract: a GET
// the model cannot shape, every hop held to the SSRF guard, and the answer run
// through readability instead of being returned as it came.
func executeExtractPageContent(ctx context.Context, argsJSON string, _ *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[extractPageArgs](argsJSON)
	if err != nil {
		return "", err
	}
	timeout := 30
	if args.TimeoutSeconds > 0 {
		timeout = args.TimeoutSeconds
	}
	if timeout > 120 {
		timeout = 120
	}
	maxChars := args.MaxChars
	if maxChars <= 0 {
		maxChars = 120_000
	}
	requestArgs, err := json.Marshal(map[string]interface{}{
		"url":             args.URL,
		"timeout_seconds": timeout,
	})
	if err != nil {
		return "", err
	}
	req, err := ParseHTTPRequest(string(requestArgs), "")
	if err != nil {
		return "", err
	}
	tr, err := req.send(ctx, transferPolicy{
		timeout:         req.Timeout,
		followRedirects: true,
		guard:           fetchGuard,
		decompress:      true,
	})
	if err != nil {
		return "", err
	}
	defer tr.Close()
	resp := tr.resp
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	body, truncated, err := readLimited(resp.Body, maxFetchHTMLBytes)
	if err != nil {
		return "", err
	}
	if truncated {
		return "", fmt.Errorf("response body exceeds %d bytes", maxFetchHTMLBytes)
	}

	// Relative links resolve against the page that was finally served, not the
	// address a redirect moved away from.
	article, err := readability.FromReader(bytes.NewReader(body), resp.Request.URL)
	if err != nil {
		return "", fmt.Errorf("readability: %w", err)
	}
	html := strings.TrimSpace(article.Content)
	if html == "" {
		html = strings.TrimSpace(article.TextContent)
	}
	md, err := HTMLToMarkdown(html)
	if err != nil {
		return "", err
	}
	title := strings.TrimSpace(article.Title)
	var b strings.Builder
	if title != "" {
		b.WriteString("# ")
		b.WriteString(title)
		b.WriteString("\n\n")
	}
	b.WriteString(strings.TrimSpace(md))
	out := b.String()
	if len(out) > maxChars {
		out = out[:maxChars] + "\n\n...truncated..."
	}
	return out, nil
}
