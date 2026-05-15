package fs

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// ApplyPatchTool returns the apply_patch built-in (unified diff on one file).
func ApplyPatchTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "apply_patch",
			Description: "Apply a unified diff/patch to a file. Use for targeted edits without rewriting the whole file.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"filePath": map[string]interface{}{
						"type":        "string",
						"description": "File path to patch",
					},
					"patch": map[string]interface{}{
						"type":        "string",
						"description": "Unified diff content (output of diff -u or git diff)",
					},
				},
				"required": []string{"filePath", "patch"},
			},
		},
		Execute: executeApplyPatch,
	}
}

type applyPatchArgs struct {
	FilePath string `json:"filePath"`
	Patch    string `json:"patch"`
	Diff     string `json:"diff"` // legacy alias
}

func executeApplyPatch(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[applyPatchArgs](argsJSON)
	if err != nil {
		return "", err
	}
	patchBody := strings.TrimSpace(args.Patch)
	if patchBody == "" {
		patchBody = strings.TrimSpace(args.Diff)
	}
	if patchBody == "" {
		return "", fmt.Errorf("apply_patch: patch is required")
	}

	path := ResolvePath(args.FilePath, env.CWD)
	if env.RestrictToCWD {
		if err := CheckInsideCWD(path, env.CWD); err != nil {
			return "", err
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("apply_patch read: %w", err)
	}

	patched, err := applyUnifiedDiff(string(data), patchBody)
	if err != nil {
		return "", fmt.Errorf("apply_patch: %w", err)
	}

	if err := os.WriteFile(path, []byte(patched), 0o644); err != nil {
		return "", fmt.Errorf("apply_patch write: %w", err)
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
			_, _ = fmt.Sscanf(dl, "@@ -%d", &origStart)            //nolint:errcheck // hunk header shape varies
			_, _ = fmt.Sscanf(dl, "@@ -%*d,%*d +%d", &newStart) //nolint:errcheck // hunk header shape varies
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
