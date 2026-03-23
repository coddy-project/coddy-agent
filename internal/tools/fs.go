package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// readFileTool returns the read_file built-in tool.
func readFileTool() *Tool {
	return &Tool{
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

func executeReadFile(_ context.Context, argsJSON string, env *Env) (string, error) {
	args, err := parseArgs[readFileArgs](argsJSON)
	if err != nil {
		return "", err
	}

	path := resolvePath(args.Path, env.CWD)
	if env.RestrictToCWD {
		if err := checkInsideCWD(path, env.CWD); err != nil {
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

// writeFileTool returns the write_file built-in tool.
func writeFileTool() *Tool {
	return &Tool{
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
		AllowedInPlanMode: false, // restricted in plan mode (only text/md files)
		RequiresPermission: false,
		Execute:            executeWriteFile,
	}
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func executeWriteFile(_ context.Context, argsJSON string, env *Env) (string, error) {
	args, err := parseArgs[writeFileArgs](argsJSON)
	if err != nil {
		return "", err
	}

	path := resolvePath(args.Path, env.CWD)
	if env.RestrictToCWD {
		if err := checkInsideCWD(path, env.CWD); err != nil {
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

// writeTextFileTool returns a plan-mode-safe write tool that only allows text/md.
func writeTextFileTool() *Tool {
	base := writeFileTool()
	base.Definition.Name = "write_text_file"
	base.Definition.Description = "Write or create a text or markdown file. Only .txt, .md, .mdx files are allowed."
	base.AllowedInPlanMode = true

	baseExec := base.Execute
	base.Execute = func(ctx context.Context, argsJSON string, env *Env) (string, error) {
		args, err := parseArgs[writeFileArgs](argsJSON)
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

// listDirTool returns the list_dir built-in tool.
func listDirTool() *Tool {
	return &Tool{
		Definition: llm.ToolDefinition{
			Name:        "list_dir",
			Description: "List files and subdirectories at the given path.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Directory path (default: working directory)",
					},
					"recursive": map[string]interface{}{
						"type":        "boolean",
						"description": "Include all subdirectories recursively (default: false)",
					},
				},
			},
		},
		AllowedInPlanMode: true,
		Execute:           executeListDir,
	}
}

type listDirArgs struct {
	Path      string `json:"path"`
	Recursive bool   `json:"recursive"`
}

func executeListDir(_ context.Context, argsJSON string, env *Env) (string, error) {
	args, err := parseArgs[listDirArgs](argsJSON)
	if err != nil {
		return "", err
	}

	dirPath := env.CWD
	if args.Path != "" {
		dirPath = resolvePath(args.Path, env.CWD)
	}

	if env.RestrictToCWD {
		if err := checkInsideCWD(dirPath, env.CWD); err != nil {
			return "", err
		}
	}

	var entries []string
	if args.Recursive {
		err = filepath.Walk(dirPath, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return nil // skip errors
			}
			rel, _ := filepath.Rel(dirPath, p)
			if rel == "." {
				return nil
			}
			if info.IsDir() {
				entries = append(entries, rel+"/")
			} else {
				entries = append(entries, rel)
			}
			return nil
		})
	} else {
		des, readErr := os.ReadDir(dirPath)
		err = readErr
		for _, de := range des {
			if de.IsDir() {
				entries = append(entries, de.Name()+"/")
			} else {
				entries = append(entries, de.Name())
			}
		}
	}

	if err != nil {
		return "", fmt.Errorf("list_dir: %w", err)
	}

	return strings.Join(entries, "\n"), nil
}

// applyDiffTool returns the apply_diff built-in tool.
func applyDiffTool() *Tool {
	return &Tool{
		Definition: llm.ToolDefinition{
			Name:        "apply_diff",
			Description: "Apply a unified diff/patch to a file. Use this to make targeted changes without rewriting the whole file.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "File path to patch",
					},
					"diff": map[string]interface{}{
						"type":        "string",
						"description": "Unified diff content (output of diff -u or git diff)",
					},
				},
				"required": []string{"path", "diff"},
			},
		},
		AllowedInPlanMode: false,
		Execute:           executeApplyDiff,
	}
}

type applyDiffArgs struct {
	Path string `json:"path"`
	Diff string `json:"diff"`
}

func executeApplyDiff(_ context.Context, argsJSON string, env *Env) (string, error) {
	args, err := parseArgs[applyDiffArgs](argsJSON)
	if err != nil {
		return "", err
	}

	path := resolvePath(args.Path, env.CWD)
	if env.RestrictToCWD {
		if err := checkInsideCWD(path, env.CWD); err != nil {
			return "", err
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("apply_diff read: %w", err)
	}

	patched, err := applyUnifiedDiff(string(data), args.Diff)
	if err != nil {
		return "", fmt.Errorf("apply_diff: %w", err)
	}

	if err := os.WriteFile(path, []byte(patched), 0o644); err != nil {
		return "", fmt.Errorf("apply_diff write: %w", err)
	}

	return fmt.Sprintf("patch applied successfully to %s", path), nil
}

// switchToAgentModeTool is available in plan mode to request a mode switch.
func switchToAgentModeTool() *Tool {
	return &Tool{
		Definition: llm.ToolDefinition{
			Name:        "switch_to_agent_mode",
			Description: "Request switching from plan mode to agent mode to begin implementation. Only available in plan mode.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"plan_summary": map[string]interface{}{
						"type":        "string",
						"description": "Summary of the implementation plan",
					},
				},
				"required": []string{"plan_summary"},
			},
		},
		AllowedInPlanMode:  true,
		RequiresPermission: true,
		Execute: func(_ context.Context, _ string, _ *Env) (string, error) {
			// Actual mode switching is handled at the ReAct loop level.
			// This tool just signals intent; the loop checks RequiresPermission.
			return "mode_switch_requested", nil
		},
	}
}

// ---- helpers ----

// resolvePath returns an absolute path, resolving relative to cwd.
func resolvePath(path, cwd string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cwd, path)
}

// checkInsideCWD returns an error if path escapes the cwd.
func checkInsideCWD(path, cwd string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		return err
	}
	if !strings.HasPrefix(abs, cwdAbs+string(filepath.Separator)) && abs != cwdAbs {
		return fmt.Errorf("path %s is outside working directory %s", path, cwd)
	}
	return nil
}

// sliceLines returns lines [start, end] from content (1-based, inclusive).
func sliceLines(content string, start, end int) string {
	lines := strings.Split(content, "\n")
	if start < 1 {
		start = 1
	}
	if end < 1 || end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start-1:end], "\n")
}

// applyUnifiedDiff is a simple unified diff applicator.
// It handles the standard --- / +++ / @@ hunk format.
func applyUnifiedDiff(original, diff string) (string, error) {
	lines := strings.Split(original, "\n")
	diffLines := strings.Split(diff, "\n")

	result := make([]string, len(lines))
	copy(result, lines)

	var hunkStart, origOffset int
	inHunk := false

	for _, dl := range diffLines {
		if strings.HasPrefix(dl, "@@") {
			// Parse @@ -start,count +start,count @@
			var origStart, newStart int
			fmt.Sscanf(dl, "@@ -%d", &origStart)
			fmt.Sscanf(dl, "@@ -%*d,%*d +%d", &newStart)
			hunkStart = origStart - 1
			origOffset = 0
			inHunk = true
			_ = newStart
			continue
		}

		if !inHunk {
			continue
		}

		switch {
		case strings.HasPrefix(dl, "---") || strings.HasPrefix(dl, "+++"):
			continue
		case strings.HasPrefix(dl, "-"):
			// Remove line at hunkStart + origOffset.
			idx := hunkStart + origOffset
			if idx < len(result) {
				result = append(result[:idx], result[idx+1:]...)
				// Don't increment origOffset since we removed a line.
			}
		case strings.HasPrefix(dl, "+"):
			// Insert line at hunkStart + origOffset.
			idx := hunkStart + origOffset
			newLine := dl[1:]
			result = append(result[:idx], append([]string{newLine}, result[idx:]...)...)
			origOffset++
		case strings.HasPrefix(dl, " "):
			origOffset++
		}
	}

	return strings.Join(result, "\n"), nil
}
