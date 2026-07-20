package fs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

const maxSearchLineBytes = 10 * 1024 * 1024

// nativeGrepSearch is the built-in content-search engine used when no system
// ripgrep is available. It walks regular files under root, skips binaries, and
// emits path:line:content records up to maxResults.
func nativeGrepSearch(ctx context.Context, root, glob, storeRoot string, matcher *regexp.Regexp, maxResults int) (string, error) {
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

// nativeGlob is the built-in file-listing engine used when no system ripgrep
// is available.
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

// walkSearchFiles visits regular files under root that match pattern, skipping
// dotfiles, symlinks, and the Coddy session store. Unlike ripgrep it does not
// honor .gitignore, so fallback results may include vendored trees.
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

// limitSearchLines caps output to maxResults lines; ripgrep's --max-count is
// per file while the tool contract documents a total maximum.
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
