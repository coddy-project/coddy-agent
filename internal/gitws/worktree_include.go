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
	files, err := includedFiles(main, filepath.Join(main, WorktreeIncludeFile))
	if err != nil || len(files) == 0 {
		return nil, err
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
	for _, rel := range files {
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
	// The mode OpenFile was given went through the umask: set it as it is.
	_, err = io.Copy(out, in)
	if err == nil {
		err = out.Chmod(fi.Mode().Perm())
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = dst.Remove(name)
		return false, err
	}
	return true, nil
}

// includedFiles lists, relative to dir in slash form and sorted, the files of
// the checkout at dir that the include file (in .gitignore syntax) names and
// that git ignores there: untracked regular paths, never a folder git did not
// enter. No include file, or no git binary, lists nothing.
func includedFiles(dir, include string) ([]string, error) {
	if fi, err := os.Lstat(include); err != nil || !fi.Mode().IsRegular() {
		return nil, nil
	}
	if !GitAvailable() {
		return nil, nil
	}
	// The untracked files the include file names, then those of them git
	// ignores: both lists are git's own reading of the patterns.
	named, _, err := runGitStdin(dir, nil, "ls-files", "--others", "--ignored", "-z", "--exclude-from="+include)
	if err != nil {
		return nil, fmt.Errorf("list the files %s names: %w", WorktreeIncludeFile, err)
	}
	var candidates []string
	for _, rel := range strings.Split(string(named), "\x00") {
		// A folder git did not enter (a repository of its own, another
		// worktree) is listed with a trailing slash: never taken whole.
		if rel == "" || strings.HasSuffix(rel, "/") || !safeRelative(rel) {
			continue
		}
		candidates = append(candidates, rel)
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	ignored, code, err := runGitStdin(dir, []byte(strings.Join(candidates, "\x00")+"\x00"), "check-ignore", "--stdin", "-z")
	if err != nil && code != 1 { // 1: none of them is ignored
		return nil, fmt.Errorf("check which files git ignores: %w", err)
	}
	var files []string
	for _, rel := range strings.Split(string(ignored), "\x00") {
		if rel != "" && safeRelative(rel) {
			files = append(files, rel)
		}
	}
	sort.Strings(files)
	return files, nil
}

// ChangedIncludes lists the files of the worktree at worktree that the main
// checkout's .worktreeinclude names, that git ignores there, and that differ
// from the main checkout's file of the same name or have none there: a copied
// .env edited in the worktree, a local file written there. Removing the
// worktree would lose them, though git sees nothing. Links count as changed
// when the main checkout has no link of the same target.
func ChangedIncludes(main, worktree string) ([]string, error) {
	files, err := includedFiles(worktree, filepath.Join(main, WorktreeIncludeFile))
	if err != nil || len(files) == 0 {
		return nil, err
	}
	src, err := os.OpenRoot(main)
	if err != nil {
		return nil, err
	}
	defer func() { _ = src.Close() }()
	wt, err := os.OpenRoot(worktree)
	if err != nil {
		return nil, err
	}
	defer func() { _ = wt.Close() }()
	var changed []string
	for _, rel := range files {
		if !sameIncluded(src, wt, filepath.FromSlash(rel)) {
			changed = append(changed, rel)
		}
	}
	return changed, nil
}

// sameIncluded reports whether name is the same in both roots: two regular
// files with the same bytes, or two links with the same target.
func sameIncluded(a, b *os.Root, name string) bool {
	fa, err := a.Lstat(name)
	if err != nil {
		return false
	}
	fb, err := b.Lstat(name)
	if err != nil || fa.Mode().Type() != fb.Mode().Type() {
		return false
	}
	if fa.Mode()&os.ModeSymlink != 0 {
		ta, errA := a.Readlink(name)
		tb, errB := b.Readlink(name)
		return errA == nil && errB == nil && ta == tb
	}
	if !fa.Mode().IsRegular() || fa.Size() != fb.Size() {
		return false
	}
	ra, err := a.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = ra.Close() }()
	rb, err := b.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = rb.Close() }()
	bufA, bufB := make([]byte, 32*1024), make([]byte, 32*1024)
	for {
		na, errA := io.ReadFull(ra, bufA)
		nb, errB := io.ReadFull(rb, bufB)
		if na != nb || !bytes.Equal(bufA[:na], bufB[:nb]) {
			return false
		}
		if errA != nil || errB != nil {
			return (errA == io.EOF || errA == io.ErrUnexpectedEOF) && (errB == io.EOF || errB == io.ErrUnexpectedEOF)
		}
	}
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
