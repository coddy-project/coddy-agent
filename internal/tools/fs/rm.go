package fs

import (
	"context"
	"fmt"
	"os"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// RemoveTool removes a file or directory (recursive optional, similar to rm / rm -r).
func RemoveTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "rm",
			Description: "Remove a file or directory. Set recursive to true to delete a non-empty directory tree.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to file or directory to remove",
					},
					"recursive": map[string]interface{}{
						"type":        "boolean",
						"description": "If true, remove directories and their contents (default: false)",
					},
				},
				"required": []string{"path"},
			},
		},
		AllowedInPlanMode: false,
		Execute:           executeRemove,
	}
}

type removeArgs struct {
	Path      string `json:"path"`
	Recursive *bool  `json:"recursive,omitempty"`
}

func executeRemove(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[removeArgs](argsJSON)
	if err != nil {
		return "", err
	}

	path := ResolvePath(args.Path, env.CWD)
	if env.RestrictToCWD {
		if err := CheckInsideCWD(path, env.CWD); err != nil {
			return "", err
		}
	}

	recursive := false
	if args.Recursive != nil {
		recursive = *args.Recursive
	}

	fi, statErr := os.Stat(path)
	if statErr != nil {
		return "", fmt.Errorf("rm: %w", statErr)
	}

	if fi.IsDir() && recursive {
		if err := os.RemoveAll(path); err != nil {
			return "", fmt.Errorf("rm: %w", err)
		}
		return fmt.Sprintf("removed tree %s", path), nil
	}

	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("rm: %w", err)
	}
	return fmt.Sprintf("removed %s", path), nil
}
