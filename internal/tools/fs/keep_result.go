package fs

import (
	"context"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// KeepResultTool returns the keep_result built-in: a lightweight marker that pins
// an already-produced read page or grep result in the LLM context so the context
// eviction pass does not collapse it. It performs no filesystem access — the call
// itself, recorded in history, is the pin (see internal/agent result eviction).
func KeepResultTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "keep_result",
			Description: "Mark an already-read page or grep result as useful so it survives context eviction " +
				"(unmarked read/grep results collapse to placeholders once you move on). No re-read or re-search " +
				"happens; this only pins what you already saw. Pass path (optionally with offset/limit) to pin a " +
				"read page, or pattern (optionally with path) to pin a grep result. The pin lasts until you write " +
				"to the file. Alternatively, set keep:true on the original read/grep call.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "For a read page: the file path (absolute or relative to the working directory). For a grep result: optional search path to disambiguate.",
					},
					"offset": map[string]interface{}{
						"type":        "integer",
						"description": "For a read page: 1-based start line of the range to pin (optional; omit to pin the whole file).",
					},
					"limit": map[string]interface{}{
						"type":        "integer",
						"description": "For a read page: number of lines from offset to pin (optional).",
					},
					"pattern": map[string]interface{}{
						"type":        "string",
						"description": "For a grep result: the search pattern that produced the results to pin.",
					},
				},
			},
		},
		Execute: executeKeepResult,
	}
}

type keepResultArgs struct {
	Path    string `json:"path"`
	Offset  int    `json:"offset"`
	Limit   int    `json:"limit"`
	Pattern string `json:"pattern"`
}

func executeKeepResult(_ context.Context, argsJSON string, _ *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[keepResultArgs](argsJSON)
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(args.Path)
	pattern := strings.TrimSpace(args.Pattern)
	if path == "" && pattern == "" {
		return "", fmt.Errorf("keep_result: provide path (read page) or pattern (grep result)")
	}

	if pattern != "" {
		if path != "" {
			return fmt.Sprintf("marked as useful: grep %q in %s", pattern, path), nil
		}
		return fmt.Sprintf("marked as useful: grep %q", pattern), nil
	}
	if args.Offset > 0 || args.Limit > 0 {
		start := args.Offset
		if start < 1 {
			start = 1
		}
		if args.Limit > 0 {
			return fmt.Sprintf("marked as useful: %s lines %d-%d", path, start, start+args.Limit-1), nil
		}
		return fmt.Sprintf("marked as useful: %s from line %d", path, start), nil
	}
	return fmt.Sprintf("marked as useful: %s (whole file)", path), nil
}
