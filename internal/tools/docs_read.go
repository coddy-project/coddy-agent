package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/docs"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// ToolDocsRead is the name of the reader of the built-in documentation.
const ToolDocsRead = "coddy_docs_read"

type docsReadArgs struct {
	Page   string `json:"page"`
	Offset int    `json:"offset"`
}

// DocsReadTool reads a page or a section of Coddy's own documentation,
// embedded in the binary, or its contents when no page is named. It is
// read-only, needs no permission and is offered in every mode.
func DocsReadTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolDocsRead,
			Description: "Read a page or one section of Coddy's own documentation, built into this binary. " +
				"Leave page out for the contents: every page with a one-line summary. " +
				"A long page comes in parts, each ending with the offset to continue at and the sections of the page, so read the section you need rather than the whole page. " +
				"Links between pages are written coddy:<page>#<section>; pass one as page to follow it. " +
				"To point the user at a page, write @coddy:<page>#<section> or a Markdown link [title](coddy:<page>#<section>): in the web UI both open that page in its documentation reader. " +
				"Outside Coddy, or when the user wants to share it, give the public address https://coddy.dev/docs/<page>.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"page": map[string]interface{}{
						"type": "string",
						"description": "The page, optionally with a section: a reference from " + ToolDocsSearch +
							" such as features/mentions or features/mentions#completion, a coddy: link from a page, or a page's title.",
					},
					"offset": map[string]interface{}{
						"type":        "integer",
						"description": "Line to continue at, from 1, counted within the page or the section; the previous part names it.",
					},
				},
			},
		},
		Execute: executeDocsRead,
	}
}

func executeDocsRead(_ context.Context, argsJSON string, _ *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[docsReadArgs](argsJSON)
	if err != nil {
		return "", err
	}
	lib, err := docs.Default()
	if err != nil {
		return "", fmt.Errorf("%s: %w", ToolDocsRead, err)
	}
	if strings.TrimSpace(args.Page) == "" {
		var b strings.Builder
		fmt.Fprintf(&b, "[Coddy %s documentation] Contents: every page as \"- <page> - <title>: <summary>\". Read one with %s, or search with %s.\n\n", lib.Version, ToolDocsRead, ToolDocsSearch)
		b.WriteString(lib.Contents())
		return b.String(), nil
	}
	page, anchor, err := lib.Resolve(args.Page)
	if err != nil {
		return "", fmt.Errorf("%s: %w", ToolDocsRead, err)
	}
	r, err := page.Read(docs.ReadOptions{Anchor: anchor, Offset: args.Offset})
	if err != nil {
		return "", fmt.Errorf("%s: %w", ToolDocsRead, err)
	}
	return formatDocsReading(lib.Version, r), nil
}

func formatDocsReading(version string, r docs.Reading) string {
	var b strings.Builder
	title := r.Page.Title
	anchor := ""
	if r.Heading != nil {
		title += " > " + r.Heading.Text
		anchor = r.Heading.Anchor
	}
	ref := docs.Ref(r.Page.Slug, anchor)
	fmt.Fprintf(&b, "[Coddy %s documentation] %s\n", version, title)
	fmt.Fprintf(&b, "reference: %s, lines %d-%d of %d; public address: %s\n\n", ref, r.From, r.To, r.Total, docs.SiteBase+ref)
	b.WriteString(r.Text)
	b.WriteByte('\n')
	if r.Next == 0 {
		return b.String()
	}
	what := "page"
	if r.Heading != nil {
		what = "section"
	}
	fmt.Fprintf(&b, "\n[The %s continues at line %d of %d: call %s with page %q and offset=%d", what, r.Next, r.Total, ToolDocsRead, ref, r.Next)
	if r.Heading == nil && len(r.Page.Sections()) > 0 {
		fmt.Fprintf(&b, ", or read one section with page \"%s#<anchor>\". The sections of the page:\n%s]\n", r.Page.Slug, r.Page.Outline())
		return b.String()
	}
	b.WriteString(".]\n")
	return b.String()
}
