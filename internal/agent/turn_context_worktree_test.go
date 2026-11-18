package agent

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws/gitwstest"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestPromptCacheWorktreeContextFollowsSessionWorkspace(t *testing.T) {
	main, worktree := gitwstest.ProjectWithWorktree(t)
	state := &session.State{CWD: main}
	a := &Agent{state: state}
	if got := a.gitWorkspaceSection(); strings.Contains(got, "Keep implementation work in this worktree") {
		t.Fatalf("main checkout has worktree instruction: %s", got)
	}
	state.SetCWD(worktree)
	if got := a.gitWorkspaceSection(); !strings.Contains(got, "Keep implementation work in this worktree") {
		t.Fatalf("linked worktree lacks instruction: %s", got)
	}
	state.SetCWD(main)
	if got := a.gitWorkspaceSection(); strings.Contains(got, "Keep implementation work in this worktree") {
		t.Fatalf("instruction remains after leaving worktree: %s", got)
	}
}
