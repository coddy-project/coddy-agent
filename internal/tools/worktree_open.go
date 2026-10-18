package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

const ToolWorktreeOpen = "worktree_open"

// WorktreeTarget names the worktree OpenWorktree moves the session into:
// Branch, the worktree that holds it or a new one for it (a new branch
// starting where Base says), or Path, a worktree that already exists.
type WorktreeTarget struct {
	Branch string
	Path   string
	Base   gitws.WorktreeBase
}

// WorktreeOpen is what OpenWorktree did, as worktree_open answers it.
type WorktreeOpen struct {
	Path         string `json:"path"`
	Branch       string `json:"branch,omitempty"`
	Created      bool   `json:"created"`
	MainCheckout string `json:"main_checkout"`
	BaseBranch   string `json:"base_branch,omitempty"`
}

// OpenWorktree moves the session into a worktree of its repository. By branch
// it opens the worktree that holds the branch, or creates one under the main
// checkout's worktrees folder (gitws.EnsureWorktreeFrom: origin fetched, a new
// branch starting where target.Base says). By path it enters a worktree that
// already exists - the folder absolute or relative to the main checkout -
// creating and fetching nothing, so it reaches a worktree on a detached HEAD or
// one another tool opened. The main checkout is worktree_exit's. It is
// worktree_open and the /worktree command alike.
func OpenWorktree(ctx context.Context, env *tooling.Env, target WorktreeTarget) (WorktreeOpen, error) {
	if env == nil || env.SwitchWorkspace == nil {
		return WorktreeOpen{}, fmt.Errorf("%s is unavailable without a session workspace", ToolWorktreeOpen)
	}
	branch, path := strings.TrimSpace(target.Branch), strings.TrimSpace(target.Path)
	switch {
	case (branch == "") == (path == ""):
		return WorktreeOpen{}, fmt.Errorf("name the worktree by exactly one of branch and path")
	case path != "" && target.Base != "":
		return WorktreeOpen{}, fmt.Errorf("base goes with a branch: a worktree named by its path already has its commit")
	case path != "":
		return enterWorktree(ctx, env, path)
	}
	dir, created, err := gitws.EnsureWorktreeFrom(env.CWD, branch, target.Base)
	if err != nil {
		return WorktreeOpen{}, err
	}
	if err := env.SwitchWorkspace(ctx, dir); err != nil {
		return WorktreeOpen{}, err
	}
	env.CWD = dir
	env.WorkspaceChanged = true
	info := gitws.Describe(dir)
	// The worktree's own branch: a remote branch asked for by its
	// remote-tracking name (origin/feature) opens as feature.
	if info.Branch != "" {
		branch = info.Branch
	}
	return WorktreeOpen{
		Path: dir, Branch: branch, Created: created,
		MainCheckout: info.RepoRoot, BaseBranch: info.BaseBranch,
	}, nil
}

// enterWorktree moves the session into the existing worktree at path.
func enterWorktree(ctx context.Context, env *tooling.Env, path string) (WorktreeOpen, error) {
	mainCheckout, base, list, err := gitws.ListWorktreeStatus(env.CWD)
	if err != nil {
		return WorktreeOpen{}, err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(mainCheckout, path)
	}
	for _, st := range list {
		if !within(st.Path, path) || !within(path, st.Path) {
			continue
		}
		if st.Main {
			return WorktreeOpen{}, fmt.Errorf("%s is the main checkout; worktree_exit goes back to it", st.Path)
		}
		if st.Missing && st.Locked {
			return WorktreeOpen{}, fmt.Errorf("the folder of the worktree %s is gone, and git keeps it locked (on removable storage, perhaps): bring the folder back, or git worktree unlock and git worktree prune forget it", st.Path)
		}
		if st.Missing {
			return WorktreeOpen{}, fmt.Errorf("the folder of the worktree %s is gone (git worktree prune forgets it)", st.Path)
		}
		if err := env.SwitchWorkspace(ctx, st.Path); err != nil {
			return WorktreeOpen{}, err
		}
		env.CWD = st.Path
		env.WorkspaceChanged = true
		return WorktreeOpen{Path: st.Path, Branch: st.Branch, MainCheckout: mainCheckout, BaseBranch: base}, nil
	}
	return WorktreeOpen{}, fmt.Errorf("no worktree of %s at %s; worktree_list names them", mainCheckout, path)
}

// WorktreeOpenTool moves the session into a worktree, opening one for a
// branch when there is none. The session runtime owns the move and its state
// reload.
func WorktreeOpenTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolWorktreeOpen,
			Description: "Move this session into a git worktree of its repository; the next tool call runs there without changing directories manually. " +
				"With branch: the worktree that holds the branch, or a new one for it under the main checkout's .coddy/worktrees (origin is fetched; a new branch starts at " +
				"origin's default branch, or at the commit this session's checkout is on with base \"head\"; the default branch and branches tracking it are refused). " +
				"With path: a worktree that already exists, its folder absolute or relative to the main checkout, nothing created or fetched (a detached HEAD too). " +
				"worktree_list names the worktrees, worktree_exit goes back to the main checkout.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"branch": map[string]interface{}{
						"type": "string", "description": "Feature branch to open or create, for example feature/login",
					},
					"path": map[string]interface{}{
						"type": "string", "description": "Folder of an existing worktree, absolute or relative to the main checkout (instead of branch)",
					},
					"base": map[string]interface{}{
						"type": "string",
						"enum": []interface{}{string(gitws.BaseDefault), string(gitws.BaseHead)},
						"description": "With branch, where a new branch starts: \"default\" (origin's default branch, fetched; the default) " +
							"or \"head\" (the commit this session's checkout is on)",
					},
				},
			},
		},
		RequiresPermission: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			var args struct {
				Branch string `json:"branch"`
				Path   string `json:"path"`
				Base   string `json:"base"`
			}
			if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			opened, err := OpenWorktree(ctx, env, WorktreeTarget{
				Branch: args.Branch, Path: args.Path, Base: gitws.WorktreeBase(strings.TrimSpace(args.Base)),
			})
			if err != nil {
				return "", err
			}
			out, err := json.Marshal(opened)
			if err != nil {
				return "", err
			}
			return string(out), nil
		},
	}
}
