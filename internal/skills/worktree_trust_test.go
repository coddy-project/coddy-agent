package skills

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws/gitwstest"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

// A worktree is the same project: a marketplace entry the main checkout
// approved is approved in its worktrees, while an entry the worktree changed
// asks again.
func TestTrustStoreSharesReceiptsAcrossTheWorktreesOfAProject(t *testing.T) {
	main, wt := gitwstest.ProjectWithWorktree(t)
	store := NewTrustStore(t.TempDir())
	entry := Declaration{Kind: "marketplace", Name: "team", Source: "owner/plugins"}
	if err := store.Approve(mcp.CanonicalWorkspace(main), entry); err != nil {
		t.Fatal(err)
	}
	if !store.Approved(mcp.CanonicalWorkspace(wt), declarationDigest(entry.Kind, entry.Name, entry.Source)) {
		t.Fatal("an entry the main checkout approved must be approved in its worktree")
	}
	if store.Approved(mcp.CanonicalWorkspace(wt), declarationDigest(entry.Kind, entry.Name, "owner/other")) {
		t.Fatal("an entry the worktree changed must ask again")
	}
}
