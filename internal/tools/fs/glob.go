package fs

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

const globMaxFiles = 100

type fileWithMtime struct {
	path  string
	mtime int64
}

// GlobTool lists files matching a glob pattern under a path (ripgrep --files), sorted by modification time (newest first).
func GlobTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "glob",
			Description: "Find files matching a glob pattern. Results are sorted by modification time (newest first), capped at 100 paths.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{
						"type":        "string",
						"description": "Glob pattern (e.g. **/*.go, *.ts)",
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Directory to search (default: working directory)",
					},
				},
				"required": []string{"pattern"},
			},
		},
		Execute: executeGlob,
	}
}

type globArgs struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
}

func executeGlob(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[globArgs](argsJSON)
	if err != nil {
		return "", err
	}
	pattern := strings.TrimSpace(args.Pattern)
	if pattern == "" {
		return "", fmt.Errorf("glob: pattern is required")
	}

	searchPath := env.CWD
	if args.Path != "" {
		searchPath = ResolvePath(args.Path, env.CWD)
	}
	st, err := os.Stat(searchPath)
	if err != nil {
		return "", fmt.Errorf("glob: %w", err)
	}
	if !st.IsDir() {
		return "", fmt.Errorf("glob: path must be a directory: %s", searchPath)
	}

	rgArgs := []string{
		"--files",
		"--glob", pattern,
		"--follow",
		searchPath,
	}

	cmd := exec.CommandContext(ctx, "rg", rgArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "(no files matched)", nil
		}
		if strings.Contains(err.Error(), "executable file not found") {
			return "", fmt.Errorf("glob: ripgrep (rg) not found in PATH")
		}
		return "", fmt.Errorf("glob: %s", stderr.String())
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	var paths []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, line)
		}
	}
	// Hide Coddy's own session store (before the cap) so other sessions' files
	// neither leak into results nor crowd out real workspace files.
	paths = dropStorePaths(paths, sessionStoreRoot(env.SessionDir))
	if len(paths) == 0 {
		return "(no files matched)", nil
	}

	truncated := false
	if len(paths) > globMaxFiles {
		truncated = true
		paths = paths[:globMaxFiles]
	}

	files := make([]fileWithMtime, 0, len(paths))
	for _, p := range paths {
		fi, statErr := os.Stat(p)
		if statErr != nil {
			continue
		}
		files = append(files, fileWithMtime{path: p, mtime: fi.ModTime().Unix()})
	}
	sort.Slice(files, func(i, j int) bool {
		return files[i].mtime > files[j].mtime
	})

	var b strings.Builder
	for i, f := range files {
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(f.path)
	}
	out := b.String()
	if truncated {
		out += "\n\n(truncated to " + fmt.Sprintf("%d", globMaxFiles) + " files)"
	}
	return out, nil
}
