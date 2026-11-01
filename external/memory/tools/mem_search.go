//go:build memory

package memtools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	memstorage "github.com/EvilFreelancer/coddy-agent/external/memory/storage"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

func memorySearchTool(store *memstorage.Store, mem *config.MemoryConfig) *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        NameSearch,
			Description: "Search all memory files under the scope roots; use hits as entry points, then open files with coddy_memory_read and follow scope:relative or Markdown links inside bodies when you need more context.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"query": map[string]interface{}{"type": "string", "description": "Search query text"},
					"scope": map[string]interface{}{"type": "string", "enum": []interface{}{"global", "project", "both"}, "description": "Which memory roots to search"},
				},
				"required": []interface{}{"query"},
			},
		},
		Execute: func(ctx context.Context, argsJSON string, _ *tooling.Env) (string, error) {
			_ = ctx
			var args struct {
				Query string `json:"query"`
				Scope string `json:"scope"`
			}
			if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
				return "", err
			}
			hits, err := store.Search(args.Query, args.Scope, mem.MaxSearchHits)
			if err != nil {
				return "", err
			}
			if len(hits) == 0 {
				return "No matching memory files.", nil
			}
			var b strings.Builder
			for i, h := range hits {
				_, _ = fmt.Fprintf(&b, "### Hit %d (%s score=%.2f path=%s)\n%s\n\n", i+1, h.Scope, h.Score, h.Path, h.Snippet)
			}
			return b.String(), nil
		},
	}
}
