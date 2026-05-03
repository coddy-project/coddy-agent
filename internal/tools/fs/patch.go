package fs

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// ApplyDiffTool returns the apply_diff built-in tool.
func ApplyDiffTool() *tooling.Tool {
	return &tooling.Tool{
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

func executeApplyDiff(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[applyDiffArgs](argsJSON)
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

// applyUnifiedDiff is a simple unified diff applicator for standard --- / +++ / @@ hunks.
func applyUnifiedDiff(original, diff string) (string, error) {
	lines := strings.Split(original, "\n")
	diffLines := strings.Split(diff, "\n")

	result := make([]string, len(lines))
	copy(result, lines)

	var hunkStart, origOffset int
	inHunk := false

	for _, dl := range diffLines {
		if strings.HasPrefix(dl, "@@") {
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
			idx := hunkStart + origOffset
			if idx < len(result) {
				result = append(result[:idx], result[idx+1:]...)
			}
		case strings.HasPrefix(dl, "+"):
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
