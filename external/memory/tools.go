package memory

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// ToolDefinitions returns tool schemas only for the memory copilot (never exposed to the main agent).
func ToolDefinitions() []llm.ToolDefinition {
	schemaSearch := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "Search query text"},
			"scope": map[string]any{"type": "string", "enum": []any{"global", "project", "both"}, "description": "Which memory roots to search"},
		},
		"required": []any{"query"},
	}
	schemaRead := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "Path as scope:relative, e.g. global:preferences.md"},
		},
		"required": []any{"path"},
	}
	schemaSave := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title": map[string]any{"type": "string"},
			"body":  map[string]any{"type": "string", "description": "Markdown body to store"},
			"scope": map[string]any{"type": "string", "enum": []any{"global", "project"}},
		},
		"required": []any{"title", "body", "scope"},
	}
	schemaDelete := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string", "description": "scope:relative path to delete"},
		},
		"required": []any{"path"},
	}
	return []llm.ToolDefinition{
		{Name: "coddy_memory_search", Description: "Search long-term memory files (global root from memory.dir or $CODDY_HOME/memory, and project <cwd>/memory) for snippets relevant to the query.", InputSchema: schemaSearch},
		{Name: "coddy_memory_read", Description: "Read one memory file by scope:relative path.", InputSchema: schemaRead},
		{Name: "coddy_memory_save", Description: "Write or overwrite a distilled memory note as markdown.", InputSchema: schemaSave},
		{Name: "coddy_memory_delete", Description: "Delete a memory file the user asked to forget or that is obsolete.", InputSchema: schemaDelete},
	}
}

func execTool(store *Store, mem *config.MemoryConfig, name, inputJSON string) (string, error) {
	switch name {
	case "coddy_memory_search":
		var args struct {
			Query string `json:"query"`
			Scope string `json:"scope"`
		}
		if err := json.Unmarshal([]byte(inputJSON), &args); err != nil {
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
			b.WriteString(fmt.Sprintf("### Hit %d (%s score=%d path=%s)\n%s\n\n", i+1, h.Scope, h.Score, h.Path, h.Snippet))
		}
		return b.String(), nil
	case "coddy_memory_read":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(inputJSON), &args); err != nil {
			return "", err
		}
		return store.Read(args.Path)
	case "coddy_memory_save":
		var args struct {
			Title string `json:"title"`
			Body  string `json:"body"`
			Scope string `json:"scope"`
		}
		if err := json.Unmarshal([]byte(inputJSON), &args); err != nil {
			return "", err
		}
		p, err := store.Write(args.Scope, args.Title, args.Body)
		if err != nil {
			return "", err
		}
		return "saved as " + p, nil
	case "coddy_memory_delete":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(inputJSON), &args); err != nil {
			return "", err
		}
		if err := store.Delete(args.Path); err != nil {
			return "", err
		}
		return "deleted " + args.Path, nil
	default:
		return "", fmt.Errorf("unknown memory tool %q", name)
	}
}
