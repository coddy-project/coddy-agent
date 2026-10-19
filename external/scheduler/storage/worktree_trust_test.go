//go:build scheduler

package storage

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws/gitwstest"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

// A worktree is the same project: a job the main checkout approved is
// approved in its worktrees, a job approved from a worktree is kept for the
// project, and the workspaces holding receipts - which the daemon scans - are
// projects, never worktrees.
func TestTrustStoreSharesReceiptsAcrossTheWorktreesOfAProject(t *testing.T) {
	main, wt := gitwstest.ProjectWithWorktree(t)
	store := NewTrustStore(t.TempDir())
	if err := store.Approve(mcp.CanonicalWorkspace(main), "nightly", "sha256:one"); err != nil {
		t.Fatal(err)
	}
	if !store.Approved(mcp.CanonicalWorkspace(wt), "nightly", "sha256:one") {
		t.Fatal("a job the main checkout approved must be approved in its worktree")
	}
	if store.Approved(mcp.CanonicalWorkspace(wt), "nightly", "sha256:two") {
		t.Fatal("a job the worktree changed must ask again")
	}
	if err := store.Approve(mcp.CanonicalWorkspace(wt), "weekly", "sha256:three"); err != nil {
		t.Fatal(err)
	}
	if got := store.Workspaces(); len(got) != 1 || got[0] != mcp.CanonicalWorkspace(main) {
		t.Fatalf("Workspaces = %v, want the main checkout alone", got)
	}
}
