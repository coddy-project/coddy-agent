package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/bgtask"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
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
	// Removed reports that the worktree was removed on the way.
	Removed       bool   `json:"removed"`
	BranchDeleted bool   `json:"branch_deleted,omitempty"`
	BranchKept    string `json:"branch_kept,omitempty"`
}

// LeaveWorktree moves the session from the linked worktree it works in back
// to the repository's main checkout, and with remove takes the worktree away
// too (gitws.RemoveWorktree: one Coddy opened, nothing uncommitted unless
// discard, its branch only when merged). Everything that would refuse the
// removal is checked before the session moves, so a refusal leaves it where
// it was. It is worktree_exit and the /worktree command alike.
func LeaveWorktree(ctx context.Context, env *tooling.Env, remove, discard bool) (WorktreeLeave, error) {
	if env == nil || env.SwitchWorkspace == nil {
		return WorktreeLeave{}, fmt.Errorf("%s is unavailable without a session workspace", ToolWorktreeExit)
	}
	wt, err := gitws.WorktreeOf(env.CWD)
	if err != nil {
		return WorktreeLeave{}, err
	}
	if remove {
		if err := gitws.CheckRemovable(wt, discard); err != nil {
			return WorktreeLeave{}, err
		}
		if n := runningTasksIn(env, wt.Path); n > 0 {
			return WorktreeLeave{}, fmt.Errorf("%d background task(s) of this session still run in %s; stop them before removing the worktree", n, wt.Path)
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
	removal, err := gitws.RemoveWorktree(wt, discard)
	if err != nil {
		return out, fmt.Errorf("the session is back in %s, but the worktree %s stays: %w", wt.MainCheckout, wt.Path, err)
	}
	out.Removed = true
	out.BranchDeleted = removal.BranchDeleted
	out.BranchKept = removal.BranchKept
	return out, nil
}

// runningTasksIn counts the session's background tasks still at work in dir
// or below it: a command or a server whose folder is about to be removed.
func runningTasksIn(env *tooling.Env, dir string) int {
	if env.Background == nil {
		return 0
	}
	n := 0
	for _, task := range env.Background.List(env.SessionID) {
		if task.Status != bgtask.StatusRunning && task.Status != bgtask.StatusQueued {
			continue
		}
		if task.CWD != "" && within(dir, task.CWD) {
			n++
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
				"action \"keep\" (the default) leaves the worktree and its branch for later. \"remove\" also removes the worktree: only one opened by worktree_create, " +
				"refused while it has uncommitted changes unless discard_changes is true, or while a background task of this session runs in it. " +
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
