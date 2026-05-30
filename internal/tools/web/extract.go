package web

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

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

func executeExtractPageContent(ctx context.Context, argsJSON string, _ *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[extractPageArgs](argsJSON)
	if err != nil {
		return "", err
	}
	u, err := ValidateFetchURL(ctx, args.URL)
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

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "coddy-agent/1.0 (+https://github.com/EvilFreelancer/coddy-agent)")

	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	limited := io.LimitReader(resp.Body, maxFetchHTMLBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return "", err
	}
	if len(body) > maxFetchHTMLBytes {
		return "", fmt.Errorf("response body exceeds %d bytes", maxFetchHTMLBytes)
	}

	article, err := readability.FromReader(bytes.NewReader(body), u)
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
