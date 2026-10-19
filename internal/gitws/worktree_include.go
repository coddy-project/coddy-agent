package gitws

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// WorktreeIncludeFile names, at the root of a main checkout and in .gitignore
// syntax, the ignored files a new worktree gets a copy of: what a branch
// needs to run but git does not carry - a .env, a local .coddy/mcp.json. The
// convention is Claude Code's.
const WorktreeIncludeFile = ".worktreeinclude"

// CopyWorktreeIncludes copies into the worktree at worktree the files of the
// main checkout at main that WorktreeIncludeFile names and git ignores, and
// returns their paths relative to the checkout, in slash form and sorted. A
// file git tracks is never copied (the branch has its own), nor an untracked
// file git does not ignore, a link, or a file the worktree already has. No
// include file, or no git binary, copies nothing.
func CopyWorktreeIncludes(main, worktree string) ([]string, error) {
	include := filepath.Join(main, WorktreeIncludeFile)
	if fi, err := os.Lstat(include); err != nil || !fi.Mode().IsRegular() {
		return nil, nil
	}
	if !GitAvailable() {
		return nil, nil
	}
	// The untracked files the include file names, then those of them git
	// ignores: both lists are git's own reading of the patterns.
	named, _, err := runGitStdin(main, nil, "ls-files", "--others", "--ignored", "-z", "--exclude-from="+include)
	if err != nil {
		return nil, fmt.Errorf("list the files %s names: %w", WorktreeIncludeFile, err)
	}
	var candidates []string
	for _, rel := range strings.Split(string(named), "\x00") {
		// A folder git did not enter (a repository of its own, another
		// worktree) is listed with a trailing slash: never copied whole.
		if rel == "" || strings.HasSuffix(rel, "/") || !safeRelative(rel) {
			continue
		}
		candidates = append(candidates, rel)
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	ignored, code, err := runGitStdin(main, []byte(strings.Join(candidates, "\x00")+"\x00"), "check-ignore", "--stdin", "-z")
	if err != nil && code != 1 { // 1: none of them is ignored
		return nil, fmt.Errorf("check which files git ignores: %w", err)
	}
	src, err := os.OpenRoot(main)
	if err != nil {
		return nil, err
	}
	defer func() { _ = src.Close() }()
	dst, err := os.OpenRoot(worktree)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dst.Close() }()
	var copied []string
	var errs []error
	for _, rel := range strings.Split(string(ignored), "\x00") {
		if rel == "" || !safeRelative(rel) {
			continue
		}
		ok, err := copyIncluded(src, dst, rel)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", rel, err))
			continue
		}
		if ok {
			copied = append(copied, rel)
		}
	}
	sort.Strings(copied)
	return copied, errors.Join(errs...)
}

// copyIncluded copies one regular file from src to dst under the same
// relative path, keeping its mode, and reports whether it did: a link, a
// folder or a file dst already has is left alone. Both roots keep every path
// inside its checkout.
func copyIncluded(src, dst *os.Root, rel string) (bool, error) {
	name := filepath.FromSlash(rel)
	fi, err := src.Lstat(name)
	if err != nil || !fi.Mode().IsRegular() {
		return false, nil
	}
	if _, err := dst.Lstat(name); err == nil {
		return false, nil
	}
	in, err := src.Open(name)
	if err != nil {
		return false, err
	}
	defer func() { _ = in.Close() }()
	if dir := filepath.Dir(name); dir != "." {
		if err := dst.MkdirAll(dir, 0o755); err != nil {
			return false, err
		}
	}
	out, err := dst.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fi.Mode().Perm())
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = dst.Remove(name)
		return false, err
	}
	return true, out.Close()
}

// safeRelative reports whether rel, as git printed it, names a path inside
// the checkout: relative, no "..", and not inside a .git folder.
func safeRelative(rel string) bool {
	if path.IsAbs(rel) || filepath.IsAbs(rel) {
		return false
	}
	clean := path.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return false
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".git" {
			return false
		}
	}
	return true
}

// runGitStdin runs git in dir with stdin and returns its standard output and
// exit code; a non-zero exit is an error that carries git's standard error.
func runGitStdin(dir string, stdin []byte, args ...string) ([]byte, int, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	platform.AdaptCommand(cmd)
	out, err := cmd.Output()
	if err != nil {
		code := -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		return out, code, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, 0, nil
}
