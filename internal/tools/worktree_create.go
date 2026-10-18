package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

const ToolWorktreeCreate = "worktree_create"

// WorktreeOpen is what OpenWorktree did, as worktree_create answers it.
type WorktreeOpen struct {
	Path         string `json:"path"`
	Branch       string `json:"branch"`
	Created      bool   `json:"created"`
	MainCheckout string `json:"main_checkout"`
	BaseBranch   string `json:"base_branch"`
}

// OpenWorktree opens a worktree for branch - created under the main
// checkout's worktrees folder, a new branch starting where from says, or the
// one that already holds the branch - and moves the session there. It is
// worktree_create and the /worktree command alike.
func OpenWorktree(ctx context.Context, env *tooling.Env, branch string, from gitws.WorktreeBase) (WorktreeOpen, error) {
	if env == nil || env.SwitchWorkspace == nil {
		return WorktreeOpen{}, fmt.Errorf("%s is unavailable without a session workspace", ToolWorktreeCreate)
	}
	branch = strings.TrimSpace(branch)
	path, created, err := gitws.EnsureWorktreeFrom(env.CWD, branch, from)
	if err != nil {
		return WorktreeOpen{}, err
	}
	if err := env.SwitchWorkspace(ctx, path); err != nil {
		return WorktreeOpen{}, err
	}
	env.CWD = path
	env.WorkspaceChanged = true
	info := gitws.Describe(path)
	// The worktree's own branch: a remote branch asked for by its
	// remote-tracking name (origin/feature) opens as feature.
	if info.Branch != "" {
		branch = info.Branch
	}
	return WorktreeOpen{
		Path: path, Branch: branch, Created: created,
		MainCheckout: info.RepoRoot, BaseBranch: info.BaseBranch,
	}, nil
}

// WorktreeCreateTool opens a feature branch in a linked worktree and moves the
// session there. The session runtime owns the move and its state reload.
func WorktreeCreateTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolWorktreeCreate,
			Description: "Fetch origin, create or reuse a worktree for a feature branch, and move this session into it. " +
				"A new branch starts at origin's default branch, or at the commit this session's checkout is on with base \"head\" (a branch stacked on the current work); " +
				"an existing branch keeps its own tip. The next tool call runs inside that worktree without changing directories manually; " +
				"worktree_exit brings the session back. The default branch and branches tracking it are refused.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"branch": map[string]interface{}{
						"type": "string", "description": "Feature branch to create or reuse, for example feature/login",
					},
					"base": map[string]interface{}{
						"type": "string",
						"enum": []interface{}{string(gitws.BaseDefault), string(gitws.BaseHead)},
						"description": "Where a new branch starts: \"default\" (origin's default branch, fetched; the default) " +
							"or \"head\" (the commit this session's checkout is on)",
					},
				},
				"required": []interface{}{"branch"},
			},
		},
		RequiresPermission: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			var args struct {
				Branch string `json:"branch"`
				Base   string `json:"base"`
			}
			if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			opened, err := OpenWorktree(ctx, env, args.Branch, gitws.WorktreeBase(strings.TrimSpace(args.Base)))
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
