package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// TouchTool creates an empty file or updates its modification time if it exists.
func TouchTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "touch",
			Description: "Create an empty file if it does not exist, or refresh its modification time if it exists. Creates parent directories when create_parents is true.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "File path",
					},
					"create_parents": map[string]interface{}{
						"type":        "boolean",
						"description": "Create parent directories if missing (default: true)",
					},
				},
				"required": []string{"path"},
			},
		},
		AllowedInPlanMode: false,
		Execute:           executeTouch,
	}
}

type touchArgs struct {
	Path          string `json:"path"`
	CreateParents *bool  `json:"create_parents,omitempty"`
}

func executeTouch(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[touchArgs](argsJSON)
	if err != nil {
		return "", err
	}

	path := ResolvePath(args.Path, env.CWD)
	if env.RestrictToCWD {
		if err := CheckInsideCWD(path, env.CWD); err != nil {
			return "", err
		}
	}

	createParents := true
	if args.CreateParents != nil {
		createParents = *args.CreateParents
	}

	if createParents {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", fmt.Errorf("touch mkdir: %w", err)
		}
	}

	now := time.Now()
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			return "", fmt.Errorf("touch: %w", err)
		}
		return fmt.Sprintf("created file %s", path), nil
	} else if statErr != nil {
		return "", fmt.Errorf("touch: %w", statErr)
	}

	if err := os.Chtimes(path, now, now); err != nil {
		return "", fmt.Errorf("touch: %w", err)
	}
	return fmt.Sprintf("updated timestamps for %s", path), nil
}
