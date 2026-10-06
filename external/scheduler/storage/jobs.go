//go:build scheduler

package storage

// Job identity across the two roots, and the containment rules of project
// jobs.
//
// User jobs live in ${CODDY_HOME}/scheduler and project jobs in
// <workspace>/.coddy/scheduler. A project folder is repository content: a link
// committed at .coddy, at .coddy/scheduler or at a job file could point a
// write at the operator's own files or make a project job alias a user job,
// so links are refused there on read and on write, and a project folder that
// resolves into the coddy home has no project jobs at all.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Job scopes.
const (
	ScopeUser    = "user"
	ScopeProject = "project"
)

// ProjectDirName is the folder of a workspace's project jobs, relative to the
// workspace.
var ProjectDirName = filepath.Join(".coddy", "scheduler")

// ErrUnsafePath is the reason a project job or folder is refused: a link
// where a real file or folder must be, or a path that leaves the workspace.
var ErrUnsafePath = errors.New("unsafe scheduler path")

// JobRef names one job file: its scope, its workspace (project scope only),
// its id and where its file and its state sidecar are.
type JobRef struct {
	Scope     string
	Workspace string
	ID        string
	Path      string
	StatePath string
}

// IsProject reports whether the ref names a project job.
func (r JobRef) IsProject() bool { return r.Scope == ScopeProject }

// Key is the identity of a job inside one process: unique across scopes and
// workspaces.
func (r JobRef) Key() string {
	if r.IsProject() {
		return ScopeProject + "\x00" + r.Workspace + "\x00" + r.ID
	}
	return ScopeUser + "\x00" + r.ID
}

// JobSnapshot is one read of a job file. Trust is decided on its Digest and
// the same snapshot is what runs, so a file swapped after the decision is
// never launched under it.
type JobSnapshot struct {
	Ref    JobRef
	Raw    []byte
	Digest string
	FM     *JobFrontmatter
	Body   string
	// Err is why the file does not read as a job (no frontmatter, no
	// schedule, an unsafe cwd); Raw and Digest are still set when the bytes
	// were read.
	Err error
}

// Roots names the folders jobs are read from for one process.
type Roots struct {
	// Home is the coddy home; project folders inside it are refused.
	Home string
	// User is ${CODDY_HOME}/scheduler.
	User string
}

// UserRef is the ref of a user job.
func (rt Roots) UserRef(id string) JobRef {
	p := filepath.Join(rt.User, id+".md")
	return JobRef{Scope: ScopeUser, ID: id, Path: p, StatePath: StatePath(p)}
}

// ProjectDir is the project jobs folder of a workspace.
func ProjectDir(workspace string) string {
	return filepath.Join(workspace, ProjectDirName)
}

// projectStateDir keeps the state of a workspace's project jobs in the home,
// never in the checkout.
func (rt Roots) projectStateDir(workspace string) string {
	sum := sha256.Sum256([]byte(workspace))
	return filepath.Join(rt.User, ".projects", hex.EncodeToString(sum[:])[:16])
}

// ProjectRef is the ref of a project job of a canonical workspace. It does
// not check the folder; CheckProjectDir does.
func (rt Roots) ProjectRef(workspace, id string) JobRef {
	return JobRef{
		Scope:     ScopeProject,
		Workspace: workspace,
		ID:        id,
		Path:      filepath.Join(ProjectDir(workspace), id+".md"),
		StatePath: filepath.Join(rt.projectStateDir(workspace), id+".state"),
	}
}

// within reports whether path is dir or inside it, both cleaned.
func within(path, dir string) bool {
	if dir == "" {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func resolved(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	return filepath.Clean(p)
}

// CheckProjectDir reports whether a workspace may have project jobs, and
// whether its folder exists. A workspace whose project folder would be, or be
// inside, the coddy home (a daemon started in $HOME with the default home)
// has none; .coddy and .coddy/scheduler must not be links.
func (rt Roots) CheckProjectDir(workspace string) (exists bool, err error) {
	if strings.TrimSpace(workspace) == "" || !filepath.IsAbs(workspace) {
		return false, fmt.Errorf("%w: workspace %q is not an absolute path", ErrUnsafePath, workspace)
	}
	dir := ProjectDir(workspace)
	home := resolved(rt.Home)
	if rt.Home != "" && (within(filepath.Clean(dir), home) || within(filepath.Clean(dir), filepath.Clean(rt.Home))) {
		return false, fmt.Errorf("%w: %s is inside the coddy home; a workspace there has no project jobs", ErrUnsafePath, dir)
	}
	for _, p := range []string{filepath.Join(workspace, ".coddy"), dir} {
		fi, err := os.Lstat(p)
		if err != nil {
			if os.IsNotExist(err) {
				return false, nil
			}
			return false, err
		}
		if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
			return false, fmt.Errorf("%w: %s must be a folder, not a link", ErrUnsafePath, p)
		}
	}
	if real := resolved(dir); !within(real, resolved(workspace)) || within(real, home) {
		return false, fmt.Errorf("%w: %s resolves outside its workspace", ErrUnsafePath, dir)
	}
	return true, nil
}

// ListUser lists the user jobs, sorted by id.
func (rt Roots) ListUser() ([]JobRef, error) {
	names, err := listMarkdown(rt.User)
	if err != nil {
		return nil, err
	}
	out := make([]JobRef, 0, len(names))
	for _, n := range names {
		out = append(out, rt.UserRef(strings.TrimSuffix(n, filepath.Ext(n))))
	}
	return out, nil
}

// ListProject lists the project jobs of a workspace, sorted by id. A link in
// the folder is still listed, so the caller can show why it does not run;
// ReadSnapshot refuses it.
func (rt Roots) ListProject(workspace string) ([]JobRef, error) {
	exists, err := rt.CheckProjectDir(workspace)
	if err != nil || !exists {
		return nil, err
	}
	names, err := listMarkdown(ProjectDir(workspace))
	if err != nil {
		return nil, err
	}
	out := make([]JobRef, 0, len(names))
	for _, n := range names {
		out = append(out, rt.ProjectRef(workspace, strings.TrimSuffix(n, filepath.Ext(n))))
	}
	return out, nil
}

func listMarkdown(dir string) ([]string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, ent := range entries {
		name := ent.Name()
		if ent.IsDir() || strings.HasPrefix(name, ".") || !strings.EqualFold(filepath.Ext(name), ".md") {
			continue
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}

// ReadSnapshot reads a job file once. A project job must be a regular file,
// not a link, and must still be the file that was checked when it is opened.
// A file that does not exist is os.ErrNotExist; one that reads but is not a
// valid job comes back with Err set.
func ReadSnapshot(ref JobRef) (JobSnapshot, error) {
	snap := JobSnapshot{Ref: ref}
	var data []byte
	if ref.IsProject() {
		before, err := os.Lstat(ref.Path)
		if err != nil {
			return snap, err
		}
		if !before.Mode().IsRegular() {
			return snap, fmt.Errorf("%w: %s must be a regular file, not a link", ErrUnsafePath, ref.Path)
		}
		f, err := os.Open(ref.Path)
		if err != nil {
			return snap, err
		}
		defer func() { _ = f.Close() }()
		after, err := f.Stat()
		if err != nil {
			return snap, err
		}
		if !os.SameFile(before, after) {
			return snap, fmt.Errorf("%w: %s changed while it was opened", ErrUnsafePath, ref.Path)
		}
		data, err = io.ReadAll(f)
		if err != nil {
			return snap, err
		}
	} else {
		var err error
		data, err = os.ReadFile(ref.Path)
		if err != nil {
			return snap, err
		}
	}
	snap.Raw = data
	snap.Digest = Digest(data)
	fm, body, err := parseJobBytes(data)
	snap.FM, snap.Body = fm, body
	if err != nil {
		snap.Err = err
		return snap, nil
	}
	if ref.IsProject() {
		if _, err := ProjectCWD(ref.Workspace, fm.CWD); err != nil {
			snap.Err = err
		}
	}
	return snap, nil
}

// parseJobBytes is ParseJobFile over bytes already read.
func parseJobBytes(data []byte) (*JobFrontmatter, string, error) {
	body, fm := splitFrontmatter(data)
	if fm == nil {
		return nil, "", fmt.Errorf("missing YAML frontmatter")
	}
	if strings.TrimSpace(fm.Schedule) == "" {
		return fm, strings.TrimSpace(body), fmt.Errorf("schedule is required in frontmatter")
	}
	// A schedule that does not parse never fires; saying so here makes the
	// list, an approval and a manual run agree with the tick.
	if _, err := ParseCronUTC(fm.Schedule); err != nil {
		return fm, strings.TrimSpace(body), fmt.Errorf("schedule %q: %w", fm.Schedule, err)
	}
	return fm, strings.TrimSpace(body), nil
}

// ProjectCWD resolves the cwd: of a project job. Empty is the workspace; a
// relative path must stay inside it, lexically and once links are resolved.
// An absolute path is accepted only when it names the workspace or a folder
// inside it once links are resolved (a job written before project jobs
// existed carries one), since nobody approving the file would expect the job
// to run somewhere else; a rooted path that is not absolute is refused.
func ProjectCWD(workspace, raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return workspace, nil
	}
	if filepath.IsAbs(raw) {
		abs := filepath.Clean(raw)
		if !within(resolved(abs), resolved(workspace)) {
			return "", fmt.Errorf("%w: absolute cwd %q is outside the workspace", ErrUnsafePath, raw)
		}
		return abs, nil
	}
	if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, `\`) || filepath.VolumeName(raw) != "" {
		return "", fmt.Errorf("%w: cwd %q of a project job must be relative to its workspace", ErrUnsafePath, raw)
	}
	joined := filepath.Clean(filepath.Join(workspace, raw))
	if !within(joined, workspace) {
		return "", fmt.Errorf("%w: cwd %q leaves the workspace", ErrUnsafePath, raw)
	}
	if !within(resolved(joined), resolved(workspace)) {
		return "", fmt.Errorf("%w: cwd %q resolves outside the workspace", ErrUnsafePath, raw)
	}
	return joined, nil
}

// CreateJobFile writes a new job file; it fails when anything, a link
// included, is at the path already. A project job's folder is created when
// missing and checked first.
func (rt Roots) CreateJobFile(ref JobRef, data []byte) error {
	if ref.IsProject() {
		if err := rt.ensureProjectDir(ref.Workspace); err != nil {
			return err
		}
	} else if err := os.MkdirAll(filepath.Dir(ref.Path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(ref.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(ref.Path)
		return err
	}
	return f.Close()
}

// ReplaceJobFile overwrites an existing job. A project job is replaced
// through a temporary file and a rename, so the write never follows a link
// that appeared at the path; a user job keeps the plain write it always had.
func (rt Roots) ReplaceJobFile(ref JobRef, data []byte) error {
	if !ref.IsProject() {
		return os.WriteFile(ref.Path, data, 0o644)
	}
	if _, err := rt.CheckProjectDir(ref.Workspace); err != nil {
		return err
	}
	return replaceAtomically(ref.Path, data)
}

// RenameJobFile moves a job file to another ref of the same scope and
// workspace, refusing to replace anything at the target.
func (rt Roots) RenameJobFile(from, to JobRef) error {
	if from.IsProject() {
		if _, err := rt.CheckProjectDir(from.Workspace); err != nil {
			return err
		}
	}
	if _, err := os.Lstat(to.Path); err == nil {
		return os.ErrExist
	}
	return os.Rename(from.Path, to.Path)
}

func (rt Roots) ensureProjectDir(workspace string) error {
	if _, err := rt.CheckProjectDir(workspace); err != nil {
		return err
	}
	if err := os.MkdirAll(ProjectDir(workspace), 0o755); err != nil {
		return err
	}
	// Checked again once it exists: a link created in the meantime is caught
	// here rather than written through.
	_, err := rt.CheckProjectDir(workspace)
	return err
}

func replaceAtomically(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Chmod(tmpPath, 0o644); err != nil {
		cleanup()
		return err
	}
	if runtime.GOOS == "windows" {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			cleanup()
			return err
		}
	}
	if err := os.Rename(tmpPath, path); err != nil {
		cleanup()
		return err
	}
	return nil
}
