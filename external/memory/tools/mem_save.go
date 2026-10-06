//go:build memory

package memtools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	memstorage "github.com/EvilFreelancer/coddy-agent/external/memory/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// truncateNoteBody trims body to maxChars characters and appends a "..."
// marker line. It counts characters (runes), not bytes, so non-Latin text
// such as Cyrillic gets the same budget as ASCII and the cut never lands
// inside a multi-byte character (issue #429). maxChars <= 0 means no cap. It
// reports whether anything was dropped.
func truncateNoteBody(body string, maxChars int) (string, bool) {
	if maxChars <= 0 || utf8.RuneCountInString(body) <= maxChars {
		return body, false
	}
	r := []rune(body)
	return string(r[:maxChars]) + "\n...", true
}

// noteBodyDescription is the schema text of the body argument: it states the
// cap, so the model knows it before the call.
func noteBodyDescription(maxChars int) string {
	if maxChars <= 0 {
		return "Markdown or plain text body to store"
	}
	return fmt.Sprintf("Markdown or plain text body to store, at most %d characters; a longer body is truncated, so split a longer topic into several notes", maxChars)
}

func memorySaveTool(store *memstorage.Store, mem *config.MemoryConfig) *tooling.Tool {
	maxChars := mem.EffectiveMaxNoteChars()
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        NameSave,
			Description: "Write or overwrite a distilled memory note. Prefer relative_path with folders for reusable organization.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"title": map[string]interface{}{"type": "string", "description": "Short title; used for default flat filename when relative_path is omitted"},
					"body":  map[string]interface{}{"type": "string", "description": noteBodyDescription(maxChars)},
					"scope": map[string]interface{}{"type": "string", "enum": []interface{}{"global", "project"}},
					"relative_path": map[string]interface{}{
						"type":        "string",
						"description": "Optional path under scope root with .md or .txt extension, e.g. design/auth-flow.md. When omitted, a slug from title is written at scope root.",
					},
				},
				"required": []interface{}{"title", "body", "scope"},
			},
		},
		Execute: func(ctx context.Context, argsJSON string, _ *tooling.Env) (string, error) {
			_ = ctx
			var args struct {
				Title        string `json:"title"`
				Body         string `json:"body"`
				Scope        string `json:"scope"`
				RelativePath string `json:"relative_path"`
			}
			if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
				return "", err
			}
			body, truncated := truncateNoteBody(strings.TrimSpace(args.Body), maxChars)
			rel := strings.TrimSpace(args.RelativePath)
			p, err := store.WriteFlexible(args.Scope, args.Title, rel, body)
			if err != nil {
				return "", err
			}
			if truncated {
				return fmt.Sprintf("saved as %s (warning: body truncated to %d characters)", p, maxChars), nil
			}
			return "saved as " + p, nil
		},
	}
}
