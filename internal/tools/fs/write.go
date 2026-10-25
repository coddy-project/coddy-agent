package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// WriteTool creates or overwrites a file with full content (OpenCode-aligned name).
func WriteTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "write",
			Description: "Create a new file or overwrite an existing file with the given content. Creates parent directories if needed.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "File path (absolute or relative to working directory)",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Full content to write to the file",
					},
				},
				"required": []string{"path", "content"},
			},
		},
		RequiresPermission: false,
		Execute:            executeWrite,
	}
}

type writeArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func executeWrite(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[writeArgs](argsJSON)
	if err != nil {
		return "", err
	}

	path := ResolvePath(args.Path, env.CWD)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("write mkdir: %w", err)
	}

	report, err := platform.WriteFileReplacing(path, []byte(args.Content), 0o644)
	if err != nil {
		return "", fmt.Errorf("write: %w", err)
	}

	return withNote(fmt.Sprintf("wrote %d bytes to %s", len(args.Content), path), report), nil
}
