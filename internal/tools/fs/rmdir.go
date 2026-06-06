package fs

import (
	"context"
	"fmt"
	"os"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// RmdirTool removes an empty directory (like rmdir).
func RmdirTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "rmdir",
			Description: "Remove an empty directory. Fails if the directory is not empty (use rm with recursive for trees).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the directory to remove",
					},
				},
				"required": []string{"path"},
			},
		},
		Execute: executeRmdir,
	}
}

type rmdirArgs struct {
	Path string `json:"path"`
}

func executeRmdir(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[rmdirArgs](argsJSON)
	if err != nil {
		return "", err
	}

	path := ResolvePath(args.Path, env.CWD)

	fi, statErr := os.Stat(path)
	if statErr != nil {
		return "", fmt.Errorf("rmdir: %w", statErr)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("rmdir: not a directory: %s", path)
	}

	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("rmdir: %w", err)
	}
	return fmt.Sprintf("removed directory %s", path), nil
}
