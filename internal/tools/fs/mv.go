package fs

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// MoveTool moves or renames a file or directory (similar to mv).
func MoveTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "mv",
			Description: "Move or rename a file or directory from src to dst. Creates destination parent directories if needed.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"src": map[string]interface{}{
						"type":        "string",
						"description": "Source path",
					},
					"dst": map[string]interface{}{
						"type":        "string",
						"description": "Destination path",
					},
				},
				"required": []string{"src", "dst"},
			},
		},
		AllowedInPlanMode: false,
		Execute:           executeMove,
	}
}

type moveArgs struct {
	Src string `json:"src"`
	Dst string `json:"dst"`
}

func executeMove(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[moveArgs](argsJSON)
	if err != nil {
		return "", err
	}

	src := ResolvePath(args.Src, env.CWD)
	dst := ResolvePath(args.Dst, env.CWD)
	if env.RestrictToCWD {
		if err := CheckInsideCWD(src, env.CWD); err != nil {
			return "", err
		}
		if err := CheckInsideCWD(dst, env.CWD); err != nil {
			return "", err
		}
	}

	if err := movePath(src, dst); err != nil {
		return "", fmt.Errorf("mv: %w", err)
	}
	return fmt.Sprintf("moved %s -> %s", src, dst), nil
}

func movePath(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	return copyThenRemove(src, dst)
}

func copyThenRemove(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	fi, err := in.Stat()
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("cross-device move of directories is not supported; use a copy tool or same filesystem")
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode()&0777)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Remove(src)
}
