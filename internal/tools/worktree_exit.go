package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

const ToolWorktreeExit = "worktree_exit"

// WorktreeLeave is what LeaveWorktree did, as worktree_exit answers it.
type WorktreeLeave struct {
	// Path is the main checkout, where the session works now.
	Path string `json:"path"`
	// Left is the worktree the session came from, and Branch its branch.
	Left   string `json:"left"`
	Branch string `json:"branch,omitempty"`
	// Gone reports that the worktree's folder no longer existed: the session
	// came back to the main checkout that held it.
	Gone bool `json:"gone,omitempty"`
	// Removed reports that the worktree was removed on the way.
	Removed bool `json:"removed"`
	// Snapshot is the ref that keeps the uncommitted work a discard dropped
	// (git cherry-pick --no-commit <ref> brings it back onto the branch).
	Snapshot      string `json:"snapshot,omitempty"`
	BranchDeleted bool   `json:"branch_deleted,omitempty"`
	BranchKept    string `json:"branch_kept,omitempty"`
}

// LeaveWorktree moves the session from the linked worktree it works in back
// to the repository's main checkout, and with remove takes the worktree away
// too (gitws.RemoveWorktree: one Coddy opened, nothing uncommitted unless
// discard, no commit left on no branch, its branch only when merged).
// Everything that would refuse the removal is checked before the session
// moves - another live session in the worktree and a background task of any
// session still running there included - so a refusal leaves it where it was.
// A session whose worktree folder is gone (removed by hand, or by another
// chat) comes back to the main checkout that held it. It is worktree_exit and
// the /worktree command alike.
func LeaveWorktree(ctx context.Context, env *tooling.Env, remove, discard bool) (WorktreeLeave, error) {
	if env == nil || env.SwitchWorkspace == nil {
		return WorktreeLeave{}, fmt.Errorf("%s is unavailable without a session workspace", ToolWorktreeExit)
	}
	if _, err := os.Stat(env.CWD); os.IsNotExist(err) {
		main, ok := session.RecoverManagedWorktreeCWD(env.CWD)
		if !ok {
			return WorktreeLeave{}, fmt.Errorf("%w: %s no longer exists and is not a worktree of a checkout Coddy can find", gitws.ErrNotLinkedWorktree, env.CWD)
		}
		left := env.CWD
		if err := env.SwitchWorkspace(ctx, main); err != nil {
			return WorktreeLeave{}, err
		}
		env.CWD = main
		env.WorkspaceChanged = true
		// git still lists the worktree whose folder is gone, and would hand
		// its path back the next time the branch is opened: forget it.
		_ = gitws.PruneWorktrees(main)
		return WorktreeLeave{Path: main, Left: left, Gone: true}, nil
	}
	wt, err := gitws.WorktreeOf(env.CWD)
	if err != nil {
		return WorktreeLeave{}, err
	}
	if remove {
		if err := removalRefusal(env, wt, discard); err != nil {
			return WorktreeLeave{}, err
		}
	}
	if err := env.SwitchWorkspace(ctx, wt.MainCheckout); err != nil {
		return WorktreeLeave{}, err
	}
	env.CWD = wt.MainCheckout
	env.WorkspaceChanged = true
	out := WorktreeLeave{Path: wt.MainCheckout, Left: wt.Path, Branch: wt.Branch}
	if !remove {
		return out, nil
	}
	// The move reloads the session's workspace (MCP servers, hooks) and can
	// take seconds: whoever came into the worktree meanwhile is asked about
	// again, right before the removal.
	if err := removalRefusal(env, wt, discard); err != nil {
		return out, fmt.Errorf("the session is back in %s, but the worktree %s stays: %w", wt.MainCheckout, wt.Path, err)
	}
	removal, err := gitws.RemoveWorktree(wt, discard)
	if err != nil {
		return out, fmt.Errorf("the session is back in %s, but the worktree %s stays: %w", wt.MainCheckout, wt.Path, err)
	}
	out.Removed = true
	out.Snapshot = removal.Snapshot
	out.BranchDeleted = removal.BranchDeleted
	out.BranchKept = removal.BranchKept
	return out, nil
}

// removalRefusal says why removing wt now would lose work or strand someone,
// or nil: gitws.CheckRemovable, then the other live sessions and the running
// background tasks of any session in it.
func removalRefusal(env *tooling.Env, wt gitws.LinkedWorktree, discard bool) error {
	if err := gitws.CheckRemovable(wt, discard); err != nil {
		return err
	}
	if env.SessionsInWorkspace != nil {
		if others := env.SessionsInWorkspace(wt.Path); len(others) > 0 {
			return fmt.Errorf("%d other session(s) still work in %s (%s); move them out before removing the worktree",
				len(others), wt.Path, strings.Join(others, ", "))
		}
	}
	if n := runningTasksIn(env, wt.Path); n > 0 {
		return fmt.Errorf("%d background task(s) still run in %s; stop them before removing the worktree", n, wt.Path)
	}
	return nil
}

// WorktreeInspection is what leaving the session's worktree would touch: the
// web UI shows it before it asks whether to keep the worktree or remove it.
type WorktreeInspection struct {
	// InWorktree is false in the main checkout or outside git; the rest is
	// then empty.
	InWorktree   bool   `json:"in_worktree"`
	Path         string `json:"path,omitempty"`
	Branch       string `json:"branch,omitempty"`
	MainCheckout string `json:"main_checkout,omitempty"`
	Managed      bool   `json:"managed"`
	Uncommitted  int    `json:"uncommitted"`
	Unpushed     int    `json:"unpushed"`
	LooseCommit  string `json:"loose_commit,omitempty"`
	Locked       bool   `json:"locked"`
	Submodules   bool   `json:"submodules"`
	// OtherSessions names the other live sessions that work in the worktree;
	// RunningTasks counts the background tasks of any session running there.
	OtherSessions []string `json:"other_sessions,omitempty"`
	RunningTasks  int      `json:"running_tasks"`
	// Removable says whether the worktree can go now, NeedsDiscard whether only
	// by discarding its uncommitted work (kept as a snapshot), and Refusal why
	// it cannot (initialised submodules among the reasons).
	Removable    bool   `json:"removable"`
	NeedsDiscard bool   `json:"needs_discard"`
	Refusal      string `json:"refusal,omitempty"`
}

// InspectSessionWorktree reads what leaving the session's worktree would
// touch, with the refusals worktree_exit would give. It changes nothing.
func InspectSessionWorktree(env *tooling.Env) (WorktreeInspection, error) {
	wt, err := gitws.WorktreeOf(env.CWD)
	if err != nil {
		if errors.Is(err, gitws.ErrNotLinkedWorktree) {
			return WorktreeInspection{}, nil
		}
		return WorktreeInspection{}, err
	}
	out := WorktreeInspection{InWorktree: true, Path: wt.Path, Branch: wt.Branch, MainCheckout: wt.MainCheckout, Managed: wt.Managed}
	st, err := gitws.InspectWorktree(wt)
	if err != nil {
		return WorktreeInspection{}, err
	}
	out.Uncommitted, out.Unpushed, out.LooseCommit = st.Uncommitted, st.Unpushed, st.LooseCommit
	out.Locked, out.Submodules = st.Locked, st.Submodules
	if env.SessionsInWorkspace != nil {
		out.OtherSessions = env.SessionsInWorkspace(wt.Path)
	}
	out.RunningTasks = runningTasksIn(env, wt.Path)
	if err := removalRefusal(env, wt, true); err != nil {
		out.Refusal = err.Error()
		return out, nil
	}
	out.Removable = true
	out.NeedsDiscard = st.Uncommitted > 0
	return out, nil
}

// runningTasksIn counts the background tasks still at work in dir or below
// it, of this session and of every other one the pool runs: a command, a
// server or a subagent whose folder is about to be removed.
func runningTasksIn(env *tooling.Env, dir string) int {
	if env.Background == nil {
		return 0
	}
	sessions := map[string]bool{env.SessionID: true}
	for id := range env.Background.RunningCountsBySession() {
		sessions[id] = true
	}
	n := 0
	for id := range sessions {
		for _, task := range env.Background.List(id) {
			if task.Status != bgtask.StatusRunning && task.Status != bgtask.StatusQueued {
				continue
			}
			if task.CWD != "" && within(dir, task.CWD) {
				n++
			}
		}
	}
	return n
}

// within reports whether path is root or below it, both resolved through
// symlinks when they exist.
func within(root, path string) bool {
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	rel, err := filepath.Rel(resolve(root), resolve(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// WorktreeExitTool brings the session back from its worktree to the main
// checkout, keeping the worktree or removing it.
func WorktreeExitTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolWorktreeExit,
			Description: "Move this session from its git worktree back to the main checkout of the repository; the next tool call runs there. " +
				"action \"keep\" (the default) leaves the worktree and its branch for later. \"remove\" also removes the worktree: only one under .coddy/worktrees of the repository, where worktree_open creates them, " +
				"refused while it has uncommitted changes unless discard_changes is true (they are kept as a snapshot), and always for commits no branch, remote branch or tag holds, initialised submodules, a locked worktree, or one another session or a background task works in. " +
				"Its branch is deleted only when git counts it as merged; a branch with unmerged commits stays and the answer says so.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"action": map[string]interface{}{
						"type":        "string",
						"enum":        []interface{}{"keep", "remove"},
						"description": "keep (default) or remove the worktree",
					},
					"discard_changes": map[string]interface{}{
						"type":        "boolean",
						"description": "With remove: drop the worktree's uncommitted changes instead of refusing",
					},
				},
			},
		},
		RequiresPermission: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			var args struct {
				Action         string `json:"action"`
				DiscardChanges bool   `json:"discard_changes"`
			}
			if strings.TrimSpace(argsJSON) != "" {
				if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
					return "", fmt.Errorf("invalid arguments: %w", err)
				}
			}
			remove := false
			switch strings.TrimSpace(args.Action) {
			case "", "keep":
			case "remove":
				remove = true
			default:
				return "", fmt.Errorf("unknown action %q: want keep or remove", args.Action)
			}
			if args.DiscardChanges && !remove {
				return "", fmt.Errorf("discard_changes applies to action \"remove\" only")
			}
			left, err := LeaveWorktree(ctx, env, remove, args.DiscardChanges)
			if err != nil {
				return "", err
			}
			out, err := json.Marshal(left)
			if err != nil {
				return "", err
			}
			return string(out), nil
		},
	}
}
