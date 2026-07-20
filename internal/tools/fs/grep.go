package fs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

const defaultGrepMaxResults = 100

// GrepTool returns the grep built-in: recursive content search that uses a
// system ripgrep when available and the built-in Go engine otherwise.
func GrepTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "grep",
			Description: "Search file contents recursively with regular expressions. " +
				"Uses system ripgrep when available and a built-in cross-platform search engine otherwise. " +
				"Returns path:line:content records.",
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
						"description": "File glob filter, including ** patterns (e.g. '*.go' or '**/*.ts')",
					},
					"case_sensitive": map[string]interface{}{
						"type":        "boolean",
						"description": "Enable case-sensitive matching (default: false)",
					},
					"max_results": map[string]interface{}{
						"type":        "integer",
						"description": "Maximum total number of matching lines (default: 100)",
					},
				},
				"required": []string{"pattern"},
			},
		},
		Execute: executeGrep,
	}
}

type grepArgs struct {
	Pattern       string `json:"pattern"`
	Path          string `json:"path"`
	Glob          string `json:"glob"`
	CaseSensitive bool   `json:"case_sensitive"`
	MaxResults    int    `json:"max_results"`
}

type grepRunner struct {
	lookPath func(string) (string, error)
	run      func(context.Context, string, []string) (output string, exitCode int, err error)
}

func defaultGrepRunner() grepRunner {
	return grepRunner{lookPath: exec.LookPath, run: runSystemRipgrep}
}

func executeGrep(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
	return executeGrepWithRunner(ctx, argsJSON, env, defaultGrepRunner())
}

func executeGrepWithRunner(ctx context.Context, argsJSON string, env *tooling.Env, runner grepRunner) (string, error) {
	args, err := tooling.ParseArgs[grepArgs](argsJSON)
	if err != nil {
		return "", err
	}

	searchPath := env.CWD
	if strings.TrimSpace(args.Path) != "" {
		searchPath = ResolvePath(args.Path, env.CWD)
	}
	if _, err := os.Stat(searchPath); err != nil {
		return "", fmt.Errorf("grep: %w", err)
	}
	if err := validateSearchGlob(args.Glob); err != nil {
		return "", fmt.Errorf("grep: invalid glob: %w", err)
	}
	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = defaultGrepMaxResults
	}
	storeRoot := sessionStoreRoot(env.SessionDir)

	// System ripgrep receives the pattern untouched: its regex engine is a
	// superset of what the built-in engine validates, so pre-validating here
	// would reject patterns ripgrep handles fine.
	if runner.lookPath != nil && runner.run != nil {
		if rgPath, lookupErr := runner.lookPath("rg"); lookupErr == nil {
			output, exitCode, runErr := runner.run(ctx, rgPath, systemRGArgs(args, searchPath, maxResults))
			switch {
			case runErr != nil:
				// A binary can disappear between LookPath and execution. Use the
				// built-in implementation rather than making search unavailable.
			case exitCode == 0:
				// --max-count caps per file; enforce the documented total here.
				output = dropStoreLines(output, storeRoot)
				return grepResultOrEmpty(limitSearchLines(output, maxResults)), nil
			case exitCode == 1:
				return "no matches found", nil
			default:
				return "", fmt.Errorf("grep: system ripgrep exited with code %d: %s", exitCode, strings.TrimSpace(output))
			}
		}
	}

	matcher, err := compileGrepMatcher(args.Pattern, args.CaseSensitive)
	if err != nil {
		return "", fmt.Errorf("grep: invalid regular expression: %w", err)
	}
	output, err := nativeGrepSearch(ctx, searchPath, args.Glob, storeRoot, matcher, maxResults)
	if err != nil {
		return "", fmt.Errorf("grep: %w", err)
	}
	return grepResultOrEmpty(output), nil
}

// compileGrepMatcher compiles the pattern for the built-in engine. Go RE2
// syntax covers the ripgrep constructs models actually emit (\d, \w, \b,
// classes, alternation), so the two backends stay interchangeable for
// common patterns.
func compileGrepMatcher(pattern string, caseSensitive bool) (*regexp.Regexp, error) {
	if caseSensitive {
		return regexp.Compile(pattern)
	}
	return regexp.Compile("(?i:" + pattern + ")")
}

func systemRGArgs(args grepArgs, searchPath string, maxResults int) []string {
	rgArgs := []string{
		"--line-number",
		"--no-heading",
		"--color=never",
		"--max-count=" + strconv.Itoa(maxResults),
	}
	if !args.CaseSensitive {
		rgArgs = append(rgArgs, "--ignore-case")
	}
	if strings.TrimSpace(args.Glob) != "" {
		rgArgs = append(rgArgs, "--glob", args.Glob)
	}
	return append(rgArgs, "--", args.Pattern, searchPath)
}

func runSystemRipgrep(ctx context.Context, executable string, args []string) (string, int, error) {
	cmd := exec.CommandContext(ctx, executable, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stderr.String(), exitErr.ExitCode(), nil
		}
		return "", -1, err
	}
	return stdout.String(), 0, nil
}

// grepResultOrEmpty normalizes empty/whitespace-only grep output to the canonical
// "no matches found" sentinel.
func grepResultOrEmpty(result string) string {
	if strings.TrimSpace(result) == "" {
		return "no matches found"
	}
	return result
}
