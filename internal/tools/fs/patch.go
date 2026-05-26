package fs

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// ApplyPatchTool returns the apply_patch built-in (unified diff on one file).
func ApplyPatchTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name:        "apply_patch",
			Description: "Apply a patch to a file. Supports unified diff (diff -u / git diff) and Codex/V4A format (*** Begin Patch ... @@ hunks with +/- lines).",
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

	patched, err := applyPatch(string(data), patchBody)
	if err != nil {
		return "", fmt.Errorf("apply_patch: %w", err)
	}

	if err := os.WriteFile(path, []byte(patched), 0o644); err != nil {
		return "", fmt.Errorf("apply_patch write: %w", err)
	}

	return fmt.Sprintf("patch applied successfully to %s", path), nil
}

func applyPatch(original, diff string) (string, error) {
	if isV4APatch(diff) {
		return applyV4APatch(original, diff)
	}
	return applyUnifiedDiff(original, diff)
}

// applyUnifiedDiff is a simple unified diff applicator for standard --- / +++ / @@ hunks.
func applyUnifiedDiff(original, diff string) (string, error) {
	lines := splitFileLines(original)
	diffLines := strings.Split(diff, "\n")

	result := make([]string, len(lines))
	copy(result, lines)

	var hunkStart, origOffset int
	inHunk := false

	for _, dl := range diffLines {
		if strings.HasPrefix(dl, "@@") {
			origStart, err := parseHunkOrigStart(dl)
			if err != nil {
				return "", err
			}
			hunkStart = hunkOrigIndex(origStart)
			origOffset = 0
			inHunk = true
			continue
		}

		if !inHunk {
			continue
		}

		if strings.HasPrefix(dl, `\`) {
			continue
		}

		switch {
		case strings.HasPrefix(dl, "---") || strings.HasPrefix(dl, "+++"):
			continue
		case strings.HasPrefix(dl, "-"):
			idx := hunkStart + origOffset
			if idx < 0 || idx >= len(result) {
				return "", fmt.Errorf("patch delete at line %d out of range (file has %d lines)", idx+1, len(result))
			}
			result = append(result[:idx], result[idx+1:]...)
		case strings.HasPrefix(dl, "+"):
			idx := hunkStart + origOffset
			if idx < 0 {
				idx = 0
			}
			if idx > len(result) {
				idx = len(result)
			}
			newLine := dl[1:]
			result = append(result[:idx], append([]string{newLine}, result[idx:]...)...)
			origOffset++
		case strings.HasPrefix(dl, " "):
			origOffset++
		case dl == "":
			continue
		default:
			return "", fmt.Errorf("patch hunk line %q: expected leading ' ', '+', or '-'", dl)
		}
	}

	return strings.Join(result, "\n"), nil
}

// hunkOrigIndex maps unified-diff 1-based origin line (0 = before first line) to a 0-based slice index.
func hunkOrigIndex(origStart int) int {
	if origStart <= 0 {
		return 0
	}
	return origStart - 1
}

func parseHunkOrigStart(line string) (int, error) {
	inner := strings.TrimSpace(strings.TrimPrefix(line, "@@"))
	inner = strings.TrimSpace(strings.TrimSuffix(inner, "@@"))
	if inner == "" {
		return 0, fmt.Errorf("invalid hunk header %q", line)
	}
	oldPart, _, _ := strings.Cut(inner, " ")
	oldPart = strings.TrimPrefix(oldPart, "-")
	if oldPart == "" {
		return 0, fmt.Errorf("invalid hunk header %q", line)
	}
	startStr, _, _ := strings.Cut(oldPart, ",")
	origStart, err := strconv.Atoi(startStr)
	if err != nil {
		return 0, fmt.Errorf("invalid hunk header %q: %w", line, err)
	}
	return origStart, nil
}
