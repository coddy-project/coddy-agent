package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// searchFilesTool returns the search_files built-in tool.
func searchFilesTool() *Tool {
	return &Tool{
		Definition: llm.ToolDefinition{
			Name:        "search_files",
			Description: "Search for a pattern in files using ripgrep. Returns matching lines with file paths and line numbers.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{
						"type":        "string",
						"description": "Search pattern (regex or literal string)",
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Directory or file to search (default: working directory)",
					},
					"glob": map[string]interface{}{
						"type":        "string",
						"description": "File glob filter, e.g. '*.go' or '**/*.ts'",
					},
					"case_sensitive": map[string]interface{}{
						"type":        "boolean",
						"description": "Enable case-sensitive matching (default: false)",
					},
					"max_results": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum number of results to return (default: 100)",
					},
				},
				"required": []string{"pattern"},
			},
		},
		AllowedInPlanMode: true,
		Execute:           executeSearchFiles,
	}
}

type searchFilesArgs struct {
	Pattern       string `json:"pattern"`
	Path          string `json:"path"`
	Glob          string `json:"glob"`
	CaseSensitive bool   `json:"case_sensitive"`
	MaxResults    int    `json:"max_results"`
}

func executeSearchFiles(ctx context.Context, argsJSON string, env *Env) (string, error) {
	args, err := parseArgs[searchFilesArgs](argsJSON)
	if err != nil {
		return "", err
	}

	searchPath := env.CWD
	if args.Path != "" {
		searchPath = resolvePath(args.Path, env.CWD)
	}
	if env.RestrictToCWD {
		if err := checkInsideCWD(searchPath, env.CWD); err != nil {
			return "", err
		}
	}

	maxResults := 100
	if args.MaxResults > 0 {
		maxResults = args.MaxResults
	}

	rgArgs := []string{
		"--line-number",
		"--no-heading",
		"--color=never",
		fmt.Sprintf("--max-count=%d", maxResults),
	}

	if !args.CaseSensitive {
		rgArgs = append(rgArgs, "--ignore-case")
	}

	if args.Glob != "" {
		rgArgs = append(rgArgs, "--glob", args.Glob)
	}

	rgArgs = append(rgArgs, args.Pattern, searchPath)

	cmd := exec.CommandContext(ctx, "rg", rgArgs...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Exit code 1 means no matches, which is not an error.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "no matches found", nil
		}
		// rg not found - fall back to grep.
		if strings.Contains(err.Error(), "executable file not found") {
			return searchWithGrep(ctx, args, searchPath, env)
		}
		return "", fmt.Errorf("search_files rg: %s", stderr.String())
	}

	result := stdout.String()
	if result == "" {
		return "no matches found", nil
	}
	return result, nil
}

// searchWithGrep falls back to grep when rg is not available.
func searchWithGrep(ctx context.Context, args searchFilesArgs, searchPath string, _ *Env) (string, error) {
	grepArgs := []string{"-rn", "--include=" + args.Glob}
	if !args.CaseSensitive {
		grepArgs = append(grepArgs, "-i")
	}
	grepArgs = append(grepArgs, args.Pattern, searchPath)

	cmd := exec.CommandContext(ctx, "grep", grepArgs...)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "no matches found", nil
		}
		return "", fmt.Errorf("search_files grep: %w", err)
	}

	return stdout.String(), nil
}
