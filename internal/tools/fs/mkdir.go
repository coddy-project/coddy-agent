package fs

import (
	"context"
	"fmt"
	"os"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// MkdirTool creates a directory (like mkdir -p when parents is true).
func MkdirTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "mkdir",
			Description: "Create a directory at the given path. When parents is true, missing parent directories are created (similar to mkdir -p).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Directory path to create",
					},
					"parents": map[string]interface{}{
						"type":        "boolean",
						"description": "Create parent directories as needed (default: true)",
					},
				},
				"required": []string{"path"},
			},
		},
		AllowedInPlanMode: false,
		Execute:           executeMkdir,
	}
}

type mkdirArgs struct {
	Path    string `json:"path"`
	Parents *bool  `json:"parents,omitempty"`
}

func executeMkdir(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[mkdirArgs](argsJSON)
	if err != nil {
		return "", err
	}

	path := ResolvePath(args.Path, env.CWD)
	if env.RestrictToCWD {
		if err := CheckInsideCWD(path, env.CWD); err != nil {
			return "", err
		}
	}

	parents := true
	if args.Parents != nil {
		parents = *args.Parents
	}

	var mkErr error
	if parents {
		mkErr = os.MkdirAll(path, 0o755)
	} else {
		mkErr = os.Mkdir(path, 0o755)
	}
	if mkErr != nil {
		return "", fmt.Errorf("mkdir: %w", mkErr)
	}

	return fmt.Sprintf("created directory %s", path), nil
}
