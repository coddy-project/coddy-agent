package fs

import (
	"context"
	"fmt"
	"os"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// ReadFileTool returns the read_file built-in tool.
func ReadFileTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "read_file",
			Description: "Read the contents of a file. Returns file content as text.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "File path (absolute or relative to working directory)",
					},
					"start_line": map[string]interface{}{
						"type":        "integer",
						"description": "First line to read (1-based, optional)",
					},
					"end_line": map[string]interface{}{
						"type":        "integer",
						"description": "Last line to read (1-based, optional)",
					},
				},
				"required": []string{"path"},
			},
		},
		AllowedInPlanMode: true,
		Execute:           executeReadFile,
	}
}

type readFileArgs struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

func executeReadFile(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[readFileArgs](argsJSON)
	if err != nil {
		return "", err
	}

	path := ResolvePath(args.Path, env.CWD)
	if env.RestrictToCWD {
		if err := CheckInsideCWD(path, env.CWD); err != nil {
			return "", err
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read_file: %w", err)
	}

	content := string(data)
	if args.StartLine > 0 || args.EndLine > 0 {
		content = sliceLines(content, args.StartLine, args.EndLine)
	}

	return content, nil
}
