package fs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
	"github.com/bmatcuk/doublestar/v4"
)

const (
	defaultRGMaxResults = 100
	maxSearchLineBytes  = 10 * 1024 * 1024
)

// RGTool returns a portable recursive text-search tool. It uses a system
// ripgrep binary when available and otherwise falls back to the Go implementation.
func RGTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: "rg_tool",
			Description: "Search file contents recursively with POSIX extended regular expressions. " +
				"Uses system ripgrep when available and a built-in cross-platform search engine otherwise. " +
				"Returns path:line:content records.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"pattern": map[string]interface{}{
						"type":        "string",
						"description": "POSIX extended regular expression",
					},
					"path": map[string]interface{}{
						"type":        "string",
						"description": "Directory or file to search (default: working directory)",
					},
					"glob": map[string]interface{}{
						"type":        "string",
						"description": "Optional file glob, including ** patterns (for example **/*.go)",
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
		Execute: executeRGTool,
	}
}

type rgToolArgs struct {
	Pattern       string `json:"pattern"`
	Path          string `json:"path"`
	Glob          string `json:"glob"`
	CaseSensitive bool   `json:"case_sensitive"`
	MaxResults    int    `json:"max_results"`
}

type rgRunner struct {
	lookPath func(string) (string, error)
	run      func(context.Context, string, []string) (output string, exitCode int, err error)
}

func defaultRGRunner() rgRunner {
	return rgRunner{lookPath: exec.LookPath, run: runSystemRipgrep}
}

func executeRGTool(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
	return executeRGToolWithRunner(ctx, argsJSON, env, defaultRGRunner())
}

func executeRGToolWithRunner(ctx context.Context, argsJSON string, env *tooling.Env, runner rgRunner) (string, error) {
	args, err := tooling.ParseArgs[rgToolArgs](argsJSON)
	if err != nil {
		return "", err
	}
	matcher, err := compilePOSIXMatcher(args.Pattern, args.CaseSensitive)
	if err != nil {
		return "", fmt.Errorf("rg_tool: invalid POSIX regular expression: %w", err)
	}

	searchPath := env.CWD
	if strings.TrimSpace(args.Path) != "" {
		searchPath = ResolvePath(args.Path, env.CWD)
	}
	if _, err := os.Stat(searchPath); err != nil {
		return "", fmt.Errorf("rg_tool: %w", err)
	}
	if err := validateSearchGlob(args.Glob); err != nil {
		return "", fmt.Errorf("rg_tool: invalid glob: %w", err)
	}
	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = defaultRGMaxResults
	}
	storeRoot := sessionStoreRoot(env.SessionDir)

	if runner.lookPath != nil && runner.run != nil {
		if rgPath, lookupErr := runner.lookPath("rg"); lookupErr == nil {
			output, exitCode, runErr := runner.run(ctx, rgPath, systemRGArgs(args, searchPath, maxResults))
			switch {
			case runErr != nil:
				// A binary can disappear between LookPath and execution. Use the
				// built-in implementation rather than making search unavailable.
			case exitCode == 0:
				output = dropStoreLines(output, storeRoot)
				return grepResultOrEmpty(limitSearchLines(output, maxResults)), nil
			case exitCode == 1:
				return "no matches found", nil
			default:
				return "", fmt.Errorf("rg_tool: system ripgrep exited with code %d: %s", exitCode, strings.TrimSpace(output))
			}
		}
	}

	output, err := nativeRGSearch(ctx, searchPath, args.Glob, storeRoot, matcher, maxResults)
	if err != nil {
		return "", fmt.Errorf("rg_tool: %w", err)
	}
	return grepResultOrEmpty(output), nil
}

func compilePOSIXMatcher(pattern string, caseSensitive bool) (*regexp.Regexp, error) {
	validated, err := regexp.CompilePOSIX(pattern)
	if err != nil {
		return nil, err
	}
	if caseSensitive {
		return validated, nil
	}
	// CompilePOSIX does not accept inline flags. Validate the expression as
	// POSIX first, then add only case folding for matching.
	return regexp.Compile("(?i:" + pattern + ")")
}

func systemRGArgs(args rgToolArgs, searchPath string, maxResults int) []string {
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

func nativeRGSearch(ctx context.Context, root, glob, storeRoot string, matcher *regexp.Regexp, maxResults int) (string, error) {
	var output strings.Builder
	matchCount := 0
	errStopSearch := errors.New("search result limit reached")
	err := walkSearchFiles(ctx, root, glob, storeRoot, func(filePath string) error {
		file, err := os.Open(filePath)
		if err != nil {
			return err
		}
		return func() (returnErr error) {
			defer func() {
				returnErr = errors.Join(returnErr, file.Close())
			}()

			reader := bufio.NewReaderSize(file, 8192)
			probe, _ := reader.Peek(8192)
			if bytes.IndexByte(probe, 0) >= 0 {
				return nil
			}
			scanner := bufio.NewScanner(reader)
			scanner.Buffer(make([]byte, 64*1024), maxSearchLineBytes)
			lineNumber := 0
			for scanner.Scan() {
				if err := ctx.Err(); err != nil {
					return err
				}
				lineNumber++
				line := scanner.Text()
				if !matcher.MatchString(line) {
					continue
				}
				if output.Len() > 0 {
					output.WriteByte('\n')
				}
				fmt.Fprintf(&output, "%s:%d:%s", filePath, lineNumber, line)
				matchCount++
				if matchCount >= maxResults {
					return errStopSearch
				}
			}
			return scanner.Err()
		}()
	})
	if errors.Is(err, errStopSearch) {
		err = nil
	}
	return output.String(), err
}

func nativeGlob(ctx context.Context, root, pattern, storeRoot string) ([]string, error) {
	if err := validateSearchGlob(pattern); err != nil {
		return nil, err
	}
	var paths []string
	err := walkSearchFiles(ctx, root, pattern, storeRoot, func(filePath string) error {
		paths = append(paths, filePath)
		return nil
	})
	return paths, err
}

func walkSearchFiles(ctx context.Context, root, pattern, storeRoot string, visit func(string) error) error {
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		if isWithinDir(root, storeRoot) || !searchGlobMatches(pattern, filepath.Base(root)) {
			return nil
		}
		return visit(root)
	}

	return filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			if filePath == root {
				return walkErr
			}
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if filePath == root {
			return nil
		}
		if isWithinDir(filePath, storeRoot) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(entry.Name(), ".") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, filePath)
		if err != nil || !searchGlobMatches(pattern, filepath.ToSlash(rel)) {
			return nil
		}
		return visit(filePath)
	})
}

func validateSearchGlob(pattern string) error {
	if strings.TrimSpace(pattern) == "" {
		return nil
	}
	_, err := doublestar.Match(filepath.ToSlash(pattern), "candidate")
	return err
}

func searchGlobMatches(pattern, relativePath string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	relativePath = filepath.ToSlash(relativePath)
	if pattern == "" {
		return true
	}
	if !strings.Contains(pattern, "/") {
		matched, _ := doublestar.Match(pattern, filepath.Base(relativePath))
		return matched
	}
	matched, _ := doublestar.Match(pattern, relativePath)
	return matched
}

func limitSearchLines(output string, maxResults int) string {
	output = strings.TrimRight(output, "\r\n")
	if output == "" {
		return ""
	}
	lines := strings.Split(output, "\n")
	if len(lines) > maxResults {
		lines = lines[:maxResults]
	}
	return strings.Join(lines, "\n")
}

func grepResultOrEmpty(output string) string {
	if strings.TrimSpace(output) == "" {
		return "no matches found"
	}
	return output
}
