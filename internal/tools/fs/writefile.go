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

// WriteFileTool returns the write_file built-in tool.
func WriteFileTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "write_file",
			Description: "Write or create a file with the given content. Creates parent directories if needed.",
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
		AllowedInPlanMode:  false,
		RequiresPermission: false,
		Execute:            executeWriteFile,
	}
}

// WriteTextFileTool returns a plan-mode write tool that only allows text and markdown files.
func WriteTextFileTool() *tooling.Tool {
	base := WriteFileTool()
	base.Definition.Name = "write_text_file"
	base.Definition.Description = "Write or create a text or markdown file. Only .txt, .md, .mdx files are allowed."
	base.AllowedInPlanMode = true
	base.RequiresPermission = false
	base.PlanOnly = true

	baseExec := base.Execute
	base.Execute = func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
		args, err := tooling.ParseArgs[writeFileArgs](argsJSON)
		if err != nil {
			return "", err
		}
		ext := strings.ToLower(filepath.Ext(args.Path))
		allowed := map[string]bool{".txt": true, ".md": true, ".mdx": true}
		if !allowed[ext] {
			return "", fmt.Errorf("write_text_file: only .txt, .md, .mdx files allowed in plan mode (got %s)", ext)
		}
		return baseExec(ctx, argsJSON, env)
	}
	return base
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func executeWriteFile(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[writeFileArgs](argsJSON)
	if err != nil {
		return "", err
	}

	path := ResolvePath(args.Path, env.CWD)
	if env.RestrictToCWD {
		if err := CheckInsideCWD(path, env.CWD); err != nil {
			return "", err
		}
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("write_file mkdir: %w", err)
	}

	if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
		return "", fmt.Errorf("write_file: %w", err)
	}

	return fmt.Sprintf("wrote %d bytes to %s", len(args.Content), path), nil
}
