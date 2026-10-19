package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// childWorktree is the git worktree a child with isolation worktree works in,
// and, once its run ended, what became of it. Fields past Branch are written
// by settleChildWorktree under the run's lock.
type childWorktree struct {
	// Path is the worktree's root, Branch its branch.
	Path   string
	Branch string
	// Removed reports that the run's end removed the worktree, the child
	// having left nothing there; BranchKept says why its branch stayed all
	// the same. Kept says why the worktree stays.
	Removed    bool
	BranchKept string
	Kept       string
}

// subagentBranch names the branch of a child's worktree: the definition and
// the end of the child session id, so a resumed run finds it again and two
// children never share one.
func subagentBranch(name, childID string) string {
	id := strings.TrimPrefix(childID, "sess_")
	if len(id) > 8 {
		id = id[len(id)-8:]
	}
	return "subagent/" + name + "-" + id
}

// openChildWorktree opens the worktree a child works in: a new branch at the
// commit cwd's checkout is on (gitws.EnsureLocalWorktree: no remote asked),
// or, for a resumed run that had one, its own branch again, found where it
// was or checked out anew when the run's end removed it. It returns the
// worktree, the folder the child works in (where cwd stands in its checkout,
// or the folder the resumed run worked in) and a line for the task log.
func openChildWorktree(cwd, name, childID string, resumed *resumeTarget) (*childWorktree, string, string, error) {
	branch := subagentBranch(name, childID)
	if resumed != nil && resumed.branch != "" {
		branch = resumed.branch
	}
	opened, err := gitws.EnsureLocalWorktree(cwd, branch)
	if err != nil {
		return nil, "", "", fmt.Errorf("subagent %q works in a git worktree of its own (isolation worktree), and none could be opened from %s: %w", name, cwd, err)
	}
	folder := gitws.FolderIn(cwd, opened.Path)
	if resumed != nil && resumed.cwd != "" && insideFolder(opened.Path, resumed.cwd) {
		folder = resumed.cwd
	}
	verb := "works in the existing worktree"
	if opened.Created {
		verb = "works in a new worktree"
	}
	note := fmt.Sprintf("%s %s on branch %s", verb, opened.Path, branch)
	if len(opened.Included) > 0 {
		note += fmt.Sprintf(" (%s copied from the main checkout by .worktreeinclude)", strings.Join(opened.Included, ", "))
	}
	if opened.IncludeErr != nil {
		note += fmt.Sprintf("; some .worktreeinclude files were not copied: %v", opened.IncludeErr)
	}
	return &childWorktree{Path: opened.Path, Branch: branch}, folder, note, nil
}

// settleChildWorktree decides, once the child's run ended and it is no longer
// live, whether its worktree goes: it does when the child left nothing of its
// own there (gitws.OwnWork) and no other session works in it; otherwise it
// stays for the parent, with the reason. Nothing it decides fails the run.
func settleChildWorktree(rt SubagentRuntime, run *subagentRun) {
	run.mu.Lock()
	w := *run.worktree
	run.mu.Unlock()
	settle := func() {
		wt, err := gitws.WorktreeOf(w.Path)
		if err != nil {
			w.Kept = err.Error()
			return
		}
		if others, ok := rt.(workspaceSessions); ok {
			if in := others.SessionsInWorkspace(wt.Path, ""); len(in) > 0 {
				w.Kept = fmt.Sprintf("other sessions work in it (%s)", strings.Join(in, ", "))
				return
			}
		}
		own, err := gitws.OwnWork(wt)
		if err != nil {
			w.Kept = err.Error()
			return
		}
		if own != "" {
			w.Kept = "the child left " + own
			return
		}
		removal, err := gitws.RemoveWorktree(wt, false)
		if err != nil {
			w.Kept = err.Error()
			return
		}
		w.Removed = true
		w.BranchKept = removal.BranchKept
	}
	settle()
	run.mu.Lock()
	*run.worktree = w
	run.mu.Unlock()
}

// reportLine says where a child with a worktree of its own worked and what
// became of the worktree, for the report the parent reads; "" without one.
// Callers hold the run's lock.
func (w *childWorktree) reportLine() string {
	switch {
	case w == nil:
		return ""
	case w.Removed && w.BranchKept != "":
		return fmt.Sprintf("worktree: the child worked in %s, which is removed as it left nothing there; %s.", w.Path, w.BranchKept)
	case w.Removed:
		return fmt.Sprintf("worktree: the child worked in %s on branch %s; both are removed, as it left nothing there.", w.Path, w.Branch)
	case w.Kept != "":
		return fmt.Sprintf("worktree: the child worked in %s on branch %s, which stays: %s. Its changes are there, not in your folder: review or merge the branch, or open it with worktree_open.", w.Path, w.Branch, w.Kept)
	default:
		return fmt.Sprintf("worktree: the child works in %s on branch %s.", w.Path, w.Branch)
	}
}

// repositoryOf names the main checkout of the repository dir belongs to, a
// worktree whose folder is gone included, or "".
func repositoryOf(dir string) string {
	if main := gitws.MainCheckoutRoot(dir); main != "" {
		return main
	}
	if main, ok := session.RecoverManagedWorktreeCWD(dir); ok {
		return main
	}
	return ""
}

// insideFolder reports whether path is root or below it, both compared in
// their canonical form.
func insideFolder(root, path string) bool {
	rel, err := filepath.Rel(session.CanonicalWorkspacePath(root), session.CanonicalWorkspacePath(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
