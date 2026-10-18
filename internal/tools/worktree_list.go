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

const ToolWorktreeList = "worktree_list"

// WorktreeListing is what worktree_list answers: every worktree of the
// session's repository, the main checkout first, and where each stands.
type WorktreeListing struct {
	MainCheckout string              `json:"main_checkout"`
	BaseBranch   string              `json:"base_branch,omitempty"`
	Worktrees    []WorktreeListEntry `json:"worktrees"`
}

// WorktreeListEntry is one worktree of a listing.
type WorktreeListEntry struct {
	gitws.WorktreeStatus
	// Current marks the worktree this session works in.
	Current bool `json:"current,omitempty"`
	// Sessions names the other live sessions that work in it.
	Sessions []string `json:"sessions,omitempty"`
}

// ListWorktrees lists the worktrees of the repository the session works in,
// with where each stands (gitws.ListWorktreeStatus), which one is the
// session's, and which other live sessions work in each. A session works in
// the deepest worktree that holds its folder: the main checkout does not
// count the sessions of the worktrees nested in it.
func ListWorktrees(env *tooling.Env) (WorktreeListing, error) {
	if env == nil || strings.TrimSpace(env.CWD) == "" {
		return WorktreeListing{}, fmt.Errorf("%s is unavailable without a session workspace", ToolWorktreeList)
	}
	mainCheckout, base, list, err := gitws.ListWorktreeStatus(env.CWD)
	if err != nil {
		return WorktreeListing{}, err
	}
	out := WorktreeListing{MainCheckout: mainCheckout, BaseBranch: base, Worktrees: make([]WorktreeListEntry, len(list))}
	current, currentLen := -1, -1
	owner := map[string]int{}
	for i, st := range list {
		out.Worktrees[i] = WorktreeListEntry{WorktreeStatus: st}
		if st.Missing {
			continue
		}
		if within(st.Path, env.CWD) && len(st.Path) > currentLen {
			current, currentLen = i, len(st.Path)
		}
		if env.SessionsInWorkspace == nil {
			continue
		}
		for _, id := range env.SessionsInWorkspace(st.Path) {
			if prev, ok := owner[id]; !ok || len(st.Path) > len(list[prev].Path) {
				owner[id] = i
			}
		}
	}
	if current >= 0 {
		out.Worktrees[current].Current = true
	}
	for id, i := range owner {
		out.Worktrees[i].Sessions = append(out.Worktrees[i].Sessions, id)
	}
	return out, nil
}

// WorktreeListTool lists the worktrees of the session's repository and where
// each stands, without moving the session.
func WorktreeListTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolWorktreeList,
			Description: "List the worktrees of this session's git repository, the main checkout first, with where each stands: its branch, last commit, " +
				"uncommitted and unpushed changes, commits ahead of and behind origin's default branch, whether it is locked or its folder is gone, " +
				"which one this session works in and which other sessions work in each. Read-only and moves nothing: look closer at one with run_command " +
				"and its cwd, move into one with worktree_enter.",
			InputSchema: map[string]interface{}{"type": "object", "properties": map[string]interface{}{}},
		},
		Execute: func(_ context.Context, _ string, env *tooling.Env) (string, error) {
			listing, err := ListWorktrees(env)
			if err != nil {
				return "", err
			}
			out, err := json.Marshal(listing)
			if err != nil {
				return "", err
			}
			return string(out), nil
		},
	}
}
