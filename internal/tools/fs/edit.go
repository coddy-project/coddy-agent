package fs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// EditTool performs exact string replacement in an existing file.
func EditTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "edit",
			Description: "Edit a file by replacing an exact contiguous range of text (oldString) with newString. LF, CRLF, and CR are treated as equivalent while preserving the file's line endings. If oldString is empty, new content replaces the entire file when creating from empty.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Path to the file to edit",
					},
					"oldString": map[string]interface{}{
						"type":        "string",
						"description": "Text to search for (exact content and whitespace; line endings are normalized). Use empty with newString to replace full file when file is empty or creating.",
					},
					"newString": map[string]interface{}{
						"type":        "string",
						"description": "Replacement text",
					},
					"replaceAll": map[string]interface{}{
						"type":        "boolean",
						"description": "Replace every occurrence of oldString (default: false)",
					},
				},
				"required": []string{"path", "newString"},
			},
		},
		RequiresPermission: false,
		Execute:            executeEdit,
	}
}

type editArgs struct {
	Path       string `json:"path"`
	OldString  string `json:"oldString"`
	NewString  string `json:"newString"`
	ReplaceAll *bool  `json:"replaceAll"`
}

func executeEdit(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[editArgs](argsJSON)
	if err != nil {
		return "", err
	}

	path := ResolvePath(args.Path, env.CWD)

	if args.OldString == args.NewString && args.OldString != "" {
		return "", fmt.Errorf("edit: oldString and newString must differ")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("edit: read: %w", err)
	}

	content := string(data)
	old := args.OldString
	replacement := args.NewString
	if old != "" {
		ending := detectLineEnding(content)
		old = convertToLineEnding(old, ending)
		replacement = convertToLineEnding(replacement, ending)
		if old == replacement {
			return "", fmt.Errorf("edit: oldString and newString must differ")
		}
	}
	replaceAll := args.ReplaceAll != nil && *args.ReplaceAll

	var out string
	if old == "" {
		out = replacement
	} else if replaceAll {
		if !strings.Contains(content, old) {
			return "", fmt.Errorf("edit: oldString not found in file")
		}
		out = strings.ReplaceAll(content, old, replacement)
	} else {
		idx := strings.Index(content, old)
		if idx < 0 {
			return "", fmt.Errorf("edit: oldString not found in file")
		}
		out = content[:idx] + replacement + content[idx+len(old):]
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("edit mkdir: %w", err)
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return "", fmt.Errorf("edit: write: %w", err)
	}

	return fmt.Sprintf("edited %s (%d bytes written)", path, len(out)), nil
}
