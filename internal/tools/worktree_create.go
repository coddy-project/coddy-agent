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

// WorktreeCreateTool opens a feature branch in a linked worktree and moves the
// session there. The session runtime owns the move and its state reload.
func WorktreeCreateTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolWorktreeCreate,
			Description: "Fetch origin, create or reuse a worktree for a feature branch based on origin's default branch, and move this session into it. " +
				"The next tool call runs inside that worktree without changing directories manually. The default branch and branches tracking it are refused.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"branch": map[string]interface{}{
						"type": "string", "description": "Feature branch to create or reuse, for example feature/login",
					},
				},
				"required": []interface{}{"branch"},
			},
		},
		RequiresPermission: true,
		Execute: func(ctx context.Context, argsJSON string, env *tooling.Env) (string, error) {
			if env == nil || env.SwitchWorkspace == nil {
				return "", fmt.Errorf("%s is unavailable without a session workspace", ToolWorktreeCreate)
			}
			var args struct {
				Branch string `json:"branch"`
			}
			if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			branch := strings.TrimSpace(args.Branch)
			path, created, err := gitws.EnsureWorktree(env.CWD, branch)
			if err != nil {
				return "", err
			}
			if err := env.SwitchWorkspace(ctx, path); err != nil {
				return "", err
			}
			env.CWD = path
			env.WorkspaceChanged = true
			info := gitws.Describe(path)
			// The worktree's own branch: a remote branch asked for by its
			// remote-tracking name (origin/feature) opens as feature.
			if info.Branch != "" {
				branch = info.Branch
			}
			out, err := json.Marshal(map[string]interface{}{
				"path": path, "branch": branch, "created": created,
				"main_checkout": info.RepoRoot, "base_branch": info.BaseBranch,
			})
			if err != nil {
				return "", err
			}
			return string(out), nil
		},
	}
}
