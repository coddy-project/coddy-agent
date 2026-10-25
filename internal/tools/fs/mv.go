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
		Execute: executeMove,
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

	report, err := movePath(src, dst)
	if err != nil {
		return "", fmt.Errorf("mv: %w", err)
	}
	return withNote(fmt.Sprintf("moved %s -> %s", src, dst), report), nil
}

func movePath(src, dst string) (platform.WriteReport, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return platform.WriteReport{}, err
	}
	if err := os.Rename(src, dst); err == nil {
		return platform.WriteReport{}, nil
	}
	return copyThenRemove(src, dst)
}

// copyThenRemove is the move across disks: the copy replaces dst atomically, and
// the source goes only once the copy is known to be on disk.
func copyThenRemove(src, dst string) (platform.WriteReport, error) {
	in, err := os.Open(src)
	if err != nil {
		return platform.WriteReport{}, err
	}
	defer func() { _ = in.Close() }()

	fi, err := in.Stat()
	if err != nil {
		return platform.WriteReport{}, err
	}
	if fi.IsDir() {
		return platform.WriteReport{}, fmt.Errorf("cross-device move of directories is not supported; use a copy tool or same filesystem")
	}
	// A dst that is src under another name (a link to it, the same folder
	// through two mounts) would be replaced by itself and then removed with it.
	if di, err := os.Stat(dst); err == nil && os.SameFile(fi, di) {
		return platform.WriteReport{}, fmt.Errorf("%s and %s are the same file", src, dst)
	}

	report, err := platform.ReplaceFileFrom(dst, in, fi.Mode()&0777)
	if err != nil {
		return report, err
	}
	if report.DirSyncErr != nil {
		return report, fmt.Errorf("copied to %s but not yet known to be on disk (%w); the source was kept", dst, report.DirSyncErr)
	}
	// Windows refuses to remove a file that is still open.
	_ = in.Close()
	return report, os.Remove(src)
}
