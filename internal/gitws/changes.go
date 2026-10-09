package gitws

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// WorkChange is one tracked file that differs between HEAD and the working
// copy. It deliberately mirrors the shape the session change set uses, so the
// HTTP layer can render every scope of the diff viewer through one DTO, but it
// stays a gitws type: nothing below this package may depend on internal/session.
type WorkChange struct {
	Path   string // workspace-relative, OS separators
	Status string // "added" | "modified" | "deleted"
	Before []byte // nil when the file is new since HEAD
	After  []byte // nil when the file is gone from the working copy
}

// runGitRaw returns a command's stdout verbatim.
//
// The package's runGit trims its output and folds stderr in, which is right for
// parsing porcelain but destroys file content: a source file that begins or ends
// with a blank line would come back altered. Anything reading blobs uses this.
func runGitRaw(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Same contract as runGit: these calls run inside request handlers and
	// agent turns, so a credential prompt must fail instead of hanging them.
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	platform.AdaptCommand(cmd)
	return cmd.Output()
}

// UncommittedChanges lists the tracked files of dir that differ from HEAD, and
// counts the untracked ones without reading them.
//
// Untracked files are counted rather than listed on purpose: a workspace with a
// build directory or a virtualenv holds thousands of them, and pulling those
// into a review would bury the edits the user came to read. The caller surfaces
// the count instead.
//
// A folder that is not a repository yields an empty result rather than an
// error, so the viewer can offer the view everywhere and simply show nothing.
// In a repository with no commit yet, every file of the index is an addition.
func UncommittedChanges(dir string) ([]WorkChange, int, error) {
	if !GitAvailable() {
		return builtinUncommittedChanges(dir)
	}
	if !Describe(dir).IsGitRepo {
		return nil, 0, nil
	}

	changes, err := trackedChanges(dir)
	if err != nil {
		return nil, 0, err
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, countUntracked(dir), nil
}

const (
	// maxUntrackedFiles bounds how many brand-new files the all-files scope
	// reads. A workspace that just produced hundreds of them is not something
	// anyone reviews file by file, so past this the scope reports how much it
	// left out instead of reading the lot.
	maxUntrackedFiles = 500

	// maxUntrackedBytes skips a single new file too large to diff usefully.
	maxUntrackedBytes = 2 << 20
)

// WorktreeChanges reports everything the working copy holds that HEAD does not:
// the tracked edits UncommittedChanges finds, plus the untracked files it
// deliberately leaves out.
//
// skipped counts untracked files not included, either because the cap was hit
// or because one was too large to be worth reading. Files git ignores are never
// considered, so a build directory or a virtualenv stays out on its own.
func WorktreeChanges(dir string) (changes []WorkChange, skipped int, err error) {
	tracked, _, err := UncommittedChanges(dir)
	if err != nil {
		return nil, 0, err
	}
	changes = tracked

	for _, rel := range untrackedPaths(dir) {
		if len(changes)-len(tracked) >= maxUntrackedFiles {
			skipped++
			continue
		}
		change, ok := readUntracked(dir, rel)
		if !ok {
			skipped++
			continue
		}
		changes = append(changes, change)
	}

	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, skipped, nil
}

// WorktreeChangeFor resolves one file of the all-files scope, reading only it.
//
// An untracked path is only read once git has named it: the path comes from a
// request, and membership of the untracked list is what keeps this from turning
// into a way to read any file on the machine.
func WorktreeChangeFor(dir, path string) (*WorkChange, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	tracked, err := UncommittedChangeFor(dir, path)
	if err != nil || tracked != nil {
		return tracked, err
	}
	if !Describe(dir).IsGitRepo {
		return nil, nil
	}
	want := filepath.FromSlash(path)
	for _, rel := range untrackedPaths(dir) {
		if filepath.FromSlash(rel) != want {
			continue
		}
		change, ok := readUntracked(dir, rel)
		if !ok {
			return nil, nil
		}
		return &change, nil
	}
	return nil, nil
}

// untrackedPaths lists the files git would add, honouring .gitignore.
func untrackedPaths(dir string) []string {
	if !GitAvailable() {
		return builtinUntrackedPaths(dir)
	}
	out, err := runGitRaw(dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil
	}
	return splitNUL(out)
}

// readUntracked loads a new file as an addition. It reports false for anything
// it declines to read, which the caller counts as skipped.
func readUntracked(dir, rel string) (WorkChange, bool) {
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	info, err := os.Lstat(abs)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxUntrackedBytes {
		return WorkChange{}, false
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return WorkChange{}, false
	}
	defer func() { _ = root.Close() }()
	content, err := root.ReadFile(filepath.FromSlash(rel))
	if err != nil {
		return WorkChange{}, false
	}
	return WorkChange{
		Path:   filepath.FromSlash(rel),
		Status: "added",
		After:  normalizeEOL(content),
	}, true
}

// UncommittedChangeFor reports one tracked file, or nil when the working copy
// did not touch it.
//
// Separate from UncommittedChanges because the review window loads patches a
// file at a time: reading the whole change set per request would spawn a git
// process per changed file on every one of those requests, which on a large
// working copy is the difference between a moment and half a minute. Listing
// the changed paths is cheap; reading blobs is not, so only the wanted one is
// read.
func UncommittedChangeFor(dir, path string) (*WorkChange, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	if !GitAvailable() {
		return builtinUncommittedChangeFor(dir, path)
	}
	if !Describe(dir).IsGitRepo {
		return nil, nil
	}
	records, err := listedRecords(dir)
	if err != nil {
		return nil, err
	}
	want := filepath.FromSlash(path)
	for _, rec := range records {
		if filepath.FromSlash(rec.newPath) != want || indexOnly(dir, rec) {
			continue
		}
		change, err := buildWorkChange(dir, rec.code, rec.oldPath, rec.newPath, gitShowHEAD(dir))
		if err != nil {
			return nil, err
		}
		if isLineEndingChurn(rec.code, change) {
			return nil, nil
		}
		return &change, nil
	}
	return nil, nil
}

// changedRecord is one entry of `git diff HEAD --name-status`, before any blob
// is read.
type changedRecord struct {
	code             byte
	oldPath, newPath string
}

// trackedChanges parses `git diff HEAD --name-status -z`.
//
// The NUL-separated form is the only one safe against paths with spaces or
// quotes; -M asks git to detect renames so a moved file reads as one change
// instead of a delete plus an add.
func trackedChanges(dir string) ([]WorkChange, error) {
	records, err := listedRecords(dir)
	if err != nil {
		return nil, err
	}
	return assembleChanges(dir, records, gitShowHEAD(dir))
}

// hasHead reports whether the repository holding dir has a commit to compare
// with: a fresh `git init` has none.
func hasHead(dir string) bool {
	_, err := runGit(dir, "rev-parse", "--verify", "--quiet", "HEAD")
	return err == nil
}

// listedRecords is what the working copy holds that HEAD does not: the
// changed records against HEAD, or, in a repository with no commit yet, every
// file of the index as an addition.
func listedRecords(dir string) ([]changedRecord, error) {
	if hasHead(dir) {
		return changedRecords(dir)
	}
	return indexRecords(dir)
}

// indexRecords lists every file of the index under dir as an addition, for a
// repository whose HEAD is not born yet.
func indexRecords(dir string) ([]changedRecord, error) {
	out, err := runGitRaw(dir, "ls-files", "-z", "--")
	if err != nil {
		return nil, err
	}
	var records []changedRecord
	for _, p := range splitNUL(out) {
		records = append(records, changedRecord{code: 'A', oldPath: p, newPath: p})
	}
	return records, nil
}

// stagedRecords lists what the index holds that HEAD does not, file by file
// and without rename pairing: `git diff HEAD` compares HEAD with the disk and
// misses a staged edit written back, or a staged new file deleted since, both
// of which a discard still has to clear from the index.
func stagedRecords(dir string) ([]changedRecord, error) {
	out, err := runGitRaw(dir, "diff", "--cached", "--name-status", "-z", "--no-renames", "--relative", "HEAD", "--")
	if err != nil {
		return nil, err
	}
	fields := splitNUL(out)
	var records []changedRecord
	for i := 0; i+1 < len(fields); i += 2 {
		code := fields[i][0]
		if code != 'A' {
			// HEAD holds it: putting it back restores the index entry too.
			code = 'M'
		}
		records = append(records, changedRecord{code: code, oldPath: fields[i+1], newPath: fields[i+1]})
	}
	return records, nil
}

// indexOnly reports an addition that exists only in the index: added, then
// deleted from the disk. There is nothing to show for it, but a discard still
// drops it from the index.
func indexOnly(dir string, rec changedRecord) bool {
	if rec.code != 'A' {
		return false
	}
	_, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(rec.newPath)))
	return err != nil
}

// assembleChanges loads both sides of every record; how the HEAD side is read
// is the one thing the two backends do differently, so the caller supplies it.
func assembleChanges(dir string, records []changedRecord, head headBlobReader) ([]WorkChange, error) {
	changes := make([]WorkChange, 0, len(records))
	for _, rec := range records {
		if indexOnly(dir, rec) {
			continue
		}
		change, err := buildWorkChange(dir, rec.code, rec.oldPath, rec.newPath, head)
		if err != nil {
			return nil, err
		}
		if isLineEndingChurn(rec.code, change) {
			continue
		}
		changes = append(changes, change)
	}
	return changes, nil
}

// isLineEndingChurn reports a plain modification whose two sides read the same
// once CRLF is levelled to LF: git lists such a file when the repository has no
// text attribute and core.autocrlf is off (a default Linux checkout), and the
// viewer would render an empty diff for it, so it is not a change at all. A
// rename or copy keeps its record even with unchanged content - the move
// itself is the change.
func isLineEndingChurn(code byte, change WorkChange) bool {
	return code == 'M' &&
		change.Before != nil && change.After != nil &&
		bytes.Equal(change.Before, change.After)
}

// changedRecords lists what changed without reading any file content.
func changedRecords(dir string) ([]changedRecord, error) {
	out, err := runGitRaw(dir, "diff", "HEAD", "--name-status", "-z", "-M", "--relative", "--")
	if err != nil {
		return nil, err
	}
	fields := splitNUL(out)

	var records []changedRecord
	for i := 0; i < len(fields); {
		code := fields[i]
		i++
		if code == "" {
			continue
		}
		// A rename or copy is followed by two paths: the source and the target.
		var oldPath, newPath string
		switch code[0] {
		case 'R', 'C':
			if i+1 >= len(fields) {
				return records, nil // truncated record; nothing sane to report
			}
			oldPath, newPath = fields[i], fields[i+1]
			i += 2
		default:
			if i >= len(fields) {
				return records, nil
			}
			oldPath, newPath = fields[i], fields[i]
			i++
		}
		records = append(records, changedRecord{code: code[0], oldPath: oldPath, newPath: newPath})
	}
	return records, nil
}

// headBlobReader reads one dir-relative path out of HEAD; the git backend
// shells out to `git show`, the built-in one walks HEAD's tree.
type headBlobReader func(rel string) ([]byte, error)

// gitShowHEAD reads a blob with `git show HEAD:./path`, run in dir so the
// dir-relative record path resolves like the diff that produced it.
func gitShowHEAD(dir string) headBlobReader {
	return func(rel string) ([]byte, error) {
		return runGitRaw(dir, "show", "HEAD:./"+rel)
	}
}

// buildWorkChange loads both sides of one changed file. The before side comes
// from HEAD's blob and the after side from the working copy, so a file staged
// and then edited again reports what is on disk now.
func buildWorkChange(dir string, code byte, oldPath, newPath string, head headBlobReader) (WorkChange, error) {
	change := WorkChange{Path: filepath.FromSlash(newPath), Status: statusForCode(code)}

	if code != 'A' && code != 'C' {
		before, err := head(oldPath)
		if err != nil {
			// The blob is unreadable (a submodule entry, say). Treat the file as
			// new rather than failing the whole scope.
			change.Status = "added"
		} else {
			change.Before = normalizeEOL(before)
		}
	}
	if code != 'D' {
		after, err := readWorktreeFile(dir, filepath.FromSlash(newPath))
		if err == nil {
			change.After = normalizeEOL(after)
		} else if !os.IsNotExist(err) {
			return change, err
		} else {
			// Recorded as changed but gone from disk: it is a deletion.
			change.Status = "deleted"
		}
	}
	return change, nil
}

// A tracked symlink is its link text, like git's blob, rather than the content
// of its target. Root also confines reads through replaced parent directories.
func readWorktreeFile(dir, rel string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	info, err := root.Lstat(rel)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := root.Readlink(rel)
		return []byte(target), err
	}
	return root.ReadFile(rel)
}

// normalizeEOL strips CR from CRLF pairs.
//
// The two sides come from different places: HEAD's blob is whatever git stores
// (LF, under the usual autocrlf setup) while the working file is what the OS
// wrote (CRLF on Windows). git applies its line-ending filter when it diffs, so
// it reports the one line that really changed; comparing the raw bytes does not,
// and a one-line edit would render as a whole-file rewrite. Both sides are
// levelled here so the diff describes content rather than platform.
func normalizeEOL(b []byte) []byte {
	if !bytes.Contains(b, crlf) {
		return b
	}
	return bytes.ReplaceAll(b, crlf, lf)
}

var (
	crlf = []byte("\r\n")
	lf   = []byte("\n")
)

func statusForCode(code byte) string {
	switch code {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	default:
		// M, R, C, T, U: the file exists on both sides, so it reads as an edit.
		return "modified"
	}
}

// countUntracked counts files git would add, honouring .gitignore.
func countUntracked(dir string) int {
	return len(untrackedPaths(dir))
}

// splitNUL splits NUL-separated git output, dropping the trailing empty field.
func splitNUL(out []byte) []string {
	var fields []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			fields = append(fields, f)
		}
	}
	return fields
}
