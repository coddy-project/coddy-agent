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

// Each checkout of a project keeps its own receipt: a worktree approving its
// changed job, deleting it or renaming it leaves the main checkout's job
// approved under its name, while an explicit revoke takes it everywhere.
func TestAWorktreeChangesOnlyItsOwnReceipts(t *testing.T) {
	main, wt := gitwstest.ProjectWithWorktree(t)
	store := NewTrustStore(t.TempDir())
	m, w := mcp.CanonicalWorkspace(main), mcp.CanonicalWorkspace(wt)
	if err := store.Approve(m, "nightly", "sha256:main"); err != nil {
		t.Fatal(err)
	}
	if err := store.Approve(w, "nightly", "sha256:branch"); err != nil {
		t.Fatal(err)
	}
	if !store.Approved(m, "nightly", "sha256:main") || !store.Approved(w, "nightly", "sha256:branch") {
		t.Fatal("each checkout must keep its own receipt")
	}
	if err := store.Rename(w, "nightly", "nightly-v2"); err != nil {
		t.Fatal(err)
	}
	if !store.Approved(m, "nightly", "sha256:main") || !store.Approved(w, "nightly-v2", "sha256:branch") {
		t.Fatal("a rename in the worktree must move only the worktree's receipt")
	}
	if _, err := store.RevokeOwn(w, "nightly-v2"); err != nil {
		t.Fatal(err)
	}
	if store.Approved(w, "nightly-v2", "sha256:branch") || !store.Approved(m, "nightly", "sha256:main") {
		t.Fatal("a delete in the worktree must take only the worktree's receipt")
	}
	if err := store.Approve(w, "nightly", "sha256:branch"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Revoke(w, "nightly"); err != nil {
		t.Fatal(err)
	}
	if store.Approved(m, "nightly", "sha256:main") || store.Approved(w, "nightly", "sha256:branch") {
		t.Fatal("an explicit revoke must take the job's receipts from the whole project")
	}
}
