package main

import (
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws/gitwstest"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

// The lists of coddy mcp, agents and hooks say, in a worktree, that its
// approvals are the main checkout's; elsewhere they say nothing more.
func TestApprovalsLineNamesTheMainCheckoutOfAWorktree(t *testing.T) {
	main, wt := gitwstest.ProjectWithWorktree(t)
	if got := approvalsLine(wt); !strings.Contains(got, mcp.CanonicalWorkspace(main)) {
		t.Fatalf("approvalsLine(worktree) = %q, want the main checkout named", got)
	}
	if got := approvalsLine(main); got != "" {
		t.Fatalf("approvalsLine(main checkout) = %q, want nothing", got)
	}
}
