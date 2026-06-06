package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
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
					"filePath": map[string]interface{}{
						"type":        "string",
						"description": "File path (absolute or relative to working directory)",
					},
					"content": map[string]interface{}{
						"type":        "string",
						"description": "Full content to write to the file",
					},
				},
				"required": []string{"filePath", "content"},
			},
		},
		RequiresPermission: false,
		Execute:            executeWrite,
	}
}

type writeArgs struct {
	FilePath string `json:"filePath"`
	Content  string `json:"content"`
}

func executeWrite(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[writeArgs](argsJSON)
	if err != nil {
		return "", err
	}

	path := ResolvePath(args.FilePath, env.CWD)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("write mkdir: %w", err)
	}

	if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
		return "", fmt.Errorf("write: %w", err)
	}

	return fmt.Sprintf("wrote %d bytes to %s", len(args.Content), path), nil
}
