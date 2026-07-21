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
			Description: "Apply a patch to a file while preserving its line endings and final newline. Supports validated unified diff (diff -u / git diff) and Codex/V4A format (*** Begin Patch ... @@ hunks with +/- lines).",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type":        "string",
						"description": "File path to patch",
					},
					"patch": map[string]interface{}{
						"type":        "string",
						"description": "Unified diff content (output of diff -u or git diff)",
					},
				},
				"required": []string{"path", "patch"},
			},
		},
		Execute: executeApplyPatch,
	}
}

type applyPatchArgs struct {
	Path  string `json:"path"`
	Patch string `json:"patch"`
	Diff  string `json:"diff"` // legacy alias
}

func executeApplyPatch(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[applyPatchArgs](argsJSON)
	if err != nil {
		return "", err
	}
	patchBody := args.Patch
	if strings.TrimSpace(patchBody) == "" {
		patchBody = args.Diff
	}
	if strings.TrimSpace(patchBody) == "" {
		return "", fmt.Errorf("apply_patch: patch is required")
	}

	path := ResolvePath(args.Path, env.CWD)

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

// applyUnifiedDiff applies standard --- / +++ / @@ hunks and validates their source lines.
func applyUnifiedDiff(original, diff string) (string, error) {
	normalizedOriginal := normalizeLineEndings(original)
	normalizedDiff := normalizeLineEndings(diff)
	lines := splitFileLines(normalizedOriginal)
	diffLines := strings.Split(normalizedDiff, "\n")
	result := make([]string, 0, len(lines))
	sourceIndex := 0
	sawHunk := false

	for i := 0; i < len(diffLines); {
		line := diffLines[i]
		if !strings.HasPrefix(line, "@@") {
			if !sawHunk && isUnifiedDiffMetadata(line) {
				i++
				continue
			}
			if line == "" {
				i++
				continue
			}
			return "", fmt.Errorf("patch line %q outside a hunk", line)
		}

		header, err := parseUnifiedHunkHeader(line)
		if err != nil {
			return "", err
		}
		hunkStart := hunkOrigIndex(header.oldStart)
		if hunkStart < sourceIndex {
			return "", fmt.Errorf("patch hunk at line %d overlaps a previous hunk", header.oldStart)
		}
		if hunkStart > len(lines) {
			return "", fmt.Errorf("patch hunk starts at line %d out of range (file has %d lines)", header.oldStart, len(lines))
		}
		result = append(result, lines[sourceIndex:hunkStart]...)
		sourceIndex = hunkStart
		sawHunk = true
		i++

		oldSeen := 0
		newSeen := 0
		for i < len(diffLines) && !strings.HasPrefix(diffLines[i], "@@") {
			hunkLine := diffLines[i]
			if hunkLine == "" {
				i++
				continue
			}
			if strings.HasPrefix(hunkLine, `\`) {
				i++
				continue
			}

			text := hunkLine[1:]
			switch hunkLine[0] {
			case ' ':
				if err := verifyUnifiedSourceLine(lines, sourceIndex, text, "context"); err != nil {
					return "", err
				}
				result = append(result, text)
				sourceIndex++
				oldSeen++
				newSeen++
			case '-':
				if err := verifyUnifiedSourceLine(lines, sourceIndex, text, "delete"); err != nil {
					return "", err
				}
				sourceIndex++
				oldSeen++
			case '+':
				result = append(result, text)
				newSeen++
			default:
				return "", fmt.Errorf("patch hunk line %q: expected leading ' ', '+', or '-'", hunkLine)
			}
			i++
		}

		if oldSeen != header.oldCount || newSeen != header.newCount {
			return "", fmt.Errorf(
				"patch hunk count mismatch: header expects -%d/+%d lines, got -%d/+%d",
				header.oldCount,
				header.newCount,
				oldSeen,
				newSeen,
			)
		}
	}

	if !sawHunk {
		return "", fmt.Errorf("patch contains no hunks")
	}
	result = append(result, lines[sourceIndex:]...)
	return restoreLineEndings(strings.Join(result, "\n"), original), nil
}

func isUnifiedDiffMetadata(line string) bool {
	return line == "" ||
		strings.HasPrefix(line, "diff ") ||
		strings.HasPrefix(line, "index ") ||
		strings.HasPrefix(line, "--- ") ||
		strings.HasPrefix(line, "+++ ")
}

func verifyUnifiedSourceLine(lines []string, index int, expected, kind string) error {
	if index >= len(lines) {
		return fmt.Errorf("patch %s mismatch at line %d: expected %q, found end of file", kind, index+1, expected)
	}
	if lines[index] != expected {
		return fmt.Errorf("patch %s mismatch at line %d: expected %q, found %q", kind, index+1, expected, lines[index])
	}
	return nil
}

// hunkOrigIndex maps unified-diff 1-based origin line (0 = before first line) to a 0-based slice index.
func hunkOrigIndex(origStart int) int {
	if origStart <= 0 {
		return 0
	}
	return origStart - 1
}

type unifiedHunkHeader struct {
	oldStart int
	oldCount int
	newCount int
}

func parseUnifiedHunkHeader(line string) (unifiedHunkHeader, error) {
	inner := strings.TrimPrefix(line, "@@")
	end := strings.Index(inner, "@@")
	if end < 0 {
		return unifiedHunkHeader{}, fmt.Errorf("invalid hunk header %q", line)
	}
	fields := strings.Fields(inner[:end])
	if len(fields) < 2 {
		return unifiedHunkHeader{}, fmt.Errorf("invalid hunk header %q", line)
	}
	oldStart, oldCount, err := parseUnifiedRange(fields[0], '-')
	if err != nil {
		return unifiedHunkHeader{}, fmt.Errorf("invalid hunk header %q: %w", line, err)
	}
	_, newCount, err := parseUnifiedRange(fields[1], '+')
	if err != nil {
		return unifiedHunkHeader{}, fmt.Errorf("invalid hunk header %q: %w", line, err)
	}
	return unifiedHunkHeader{oldStart: oldStart, oldCount: oldCount, newCount: newCount}, nil
}

func parseUnifiedRange(value string, prefix byte) (start, count int, err error) {
	if len(value) < 2 || value[0] != prefix {
		return 0, 0, fmt.Errorf("expected %q range, got %q", prefix, value)
	}
	rangeText := value[1:]
	startText, countText, hasCount := strings.Cut(rangeText, ",")
	start, err = strconv.Atoi(startText)
	if err != nil {
		return 0, 0, err
	}
	count = 1
	if hasCount {
		count, err = strconv.Atoi(countText)
		if err != nil {
			return 0, 0, err
		}
	}
	if start < 0 || count < 0 {
		return 0, 0, fmt.Errorf("negative range %q", value)
	}
	return start, count, nil
}
