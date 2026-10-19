package agent

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// childWorktree is the git worktree a child with isolation worktree works in,
// and, once its run ended, what became of it. Fields past Start are written
// by settleChildWorktree under the run's lock.
type childWorktree struct {
	// Path is the worktree's root, Branch its branch.
	Path   string
	Branch string
	// Created reports that this launch made the worktree; one that was there
	// already belongs to an earlier run of the child.
	Created bool
	// Start is the commit the worktree was on when the run began: a run whose
	// HEAD moved off it committed something, wherever those commits are held.
	Start string
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
// commit cwd's checkout is on (gitws.EnsureLocalWorktree: no remote asked).
// A resumed run that had one goes on in it while it is there, on whatever
// branch the child left it, and otherwise gets its branch checked out anew.
// It returns the worktree, the folder the child works in (where cwd stands in
// its checkout, or the folder the resumed run worked in) and a line for the
// task log.
func openChildWorktree(cwd, name, childID string, resumed *resumeTarget) (*childWorktree, string, string, error) {
	branch := subagentBranch(name, childID)
	var opened gitws.WorktreeEnsured
	reused := false
	if resumed != nil {
		if resumed.branch != "" {
			branch = resumed.branch
		}
		if wt, err := gitws.WorktreeOf(resumed.worktree); err == nil && session.SameWorkspacePath(wt.Path, resumed.worktree) &&
			session.SameWorkspacePath(wt.MainCheckout, repositoryOf(cwd)) {
			opened, reused = gitws.WorktreeEnsured{Path: wt.Path}, true
			if wt.Branch != "" {
				branch = wt.Branch
			}
		}
	}
	if !reused {
		var err error
		if opened, err = gitws.EnsureLocalWorktree(cwd, branch); err != nil {
			return nil, "", "", fmt.Errorf("subagent %q works in a git worktree of its own (isolation worktree), and none could be opened from %s: %w", name, cwd, err)
		}
	}
	start, err := gitws.HeadOf(opened.Path)
	if err != nil {
		return nil, "", "", fmt.Errorf("subagent %q: read the commit of its worktree %s: %w", name, opened.Path, err)
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
	return &childWorktree{Path: opened.Path, Branch: branch, Created: opened.Created, Start: start}, folder, note, nil
}

// settleChildWorktree decides, once the child's run ended, its own tasks
// stopped and it is no longer live, whether its worktree goes. It does when
// the child left nothing there: nothing gitws.OwnWork counts, and HEAD still
// on the commit the run started at, so no commit of the run is left out of a
// resume however it is held. Another session or a background task of any
// session working in it keeps it too, asked again right before the removal.
// Otherwise it stays for the parent, with the reason. Nothing it decides
// fails the run.
func settleChildWorktree(rt SubagentRuntime, pool *bgtask.Pool, run *subagentRun) {
	run.mu.Lock()
	w := *run.worktree
	taskID := run.taskID
	run.mu.Unlock()
	busy := func(dir string) string {
		if others, ok := rt.(workspaceSessions); ok {
			if in := others.SessionsInWorkspace(dir, ""); len(in) > 0 {
				return fmt.Sprintf("other sessions work in it (%s)", strings.Join(in, ", "))
			}
		}
		if n := runningTasksInFolder(pool, dir, taskID); n > 0 {
			return fmt.Sprintf("%d background task(s) still run in it", n)
		}
		return ""
	}
	settle := func() {
		wt, err := gitws.WorktreeOf(w.Path)
		if err != nil {
			w.Kept = err.Error()
			return
		}
		created := w.Branch
		if wt.Branch != "" {
			// The child may have switched branches: the report names the one
			// the worktree is on.
			w.Branch = wt.Branch
		}
		if why := busy(wt.Path); why != "" {
			w.Kept = why
			return
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
		head, err := gitws.HeadOf(wt.Path)
		if err != nil {
			w.Kept = err.Error()
			return
		}
		if head != w.Start {
			w.Kept = fmt.Sprintf("the child committed on it (now at %s)", shortCommit(head))
			return
		}
		if why := busy(wt.Path); why != "" {
			w.Kept = why
			return
		}
		removal, err := gitws.RemoveWorktree(wt, false)
		if err != nil {
			w.Kept = err.Error()
			return
		}
		w.Removed = true
		w.BranchKept = removal.BranchKept
		// The branch the run was given goes too when the child left it for
		// another one and nothing holds it alone.
		if created != "" && created != wt.Branch {
			_, _ = gitws.DeleteBranchIfHeld(wt.MainCheckout, created)
		}
	}
	settle()
	run.mu.Lock()
	*run.worktree = w
	run.mu.Unlock()
}

// runningTasksInFolder counts the background tasks of any session the pool
// runs that are still at work in dir or below it, except the task named
// except (the child's own agent task, still running while its run ends).
func runningTasksInFolder(pool *bgtask.Pool, dir, except string) int {
	if pool == nil {
		return 0
	}
	n := 0
	for id := range pool.RunningCountsBySession() {
		for _, task := range pool.List(id) {
			if task.ID == except || (task.Status != bgtask.StatusRunning && task.Status != bgtask.StatusQueued) {
				continue
			}
			if task.CWD != "" && insideFolder(dir, task.CWD) {
				n++
			}
		}
	}
	return n
}

func shortCommit(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
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
