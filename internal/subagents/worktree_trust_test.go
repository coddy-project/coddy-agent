package subagents

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws/gitwstest"
)

// A worktree is the same project: a definition the main checkout approved is
// approved in its worktrees, while one the worktree changed asks again.
func TestTrustStoreSharesReceiptsAcrossTheWorktreesOfAProject(t *testing.T) {
	main, wt := gitwstest.ProjectWithWorktree(t)
	store := NewTrustStore(t.TempDir())
	def := &Definition{Name: "reviewer", Digest: "digest-1", Path: main + "/.coddy/agents/reviewer.md"}
	if err := store.Approve(CanonicalWorkspace(main), def); err != nil {
		t.Fatal(err)
	}
	if !store.Approved(CanonicalWorkspace(wt), "reviewer", "digest-1") {
		t.Fatal("a definition the main checkout approved must be approved in its worktree")
	}
	if store.Approved(CanonicalWorkspace(wt), "reviewer", "digest-2") {
		t.Fatal("a definition the worktree changed must ask again")
	}
	if got := Decide(&Definition{Name: "reviewer", Digest: "digest-1", Scope: ScopeProject}, "ask", CanonicalWorkspace(wt), store); got != TrustTrusted {
		t.Fatalf("Decide in the worktree = %v, want trusted", got)
	}
}
