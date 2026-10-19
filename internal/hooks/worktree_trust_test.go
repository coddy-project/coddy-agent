package hooks_test

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws/gitwstest"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
)

// A worktree is the same project: a hook file the main checkout approved, at
// the same place in the project with the same bytes, is approved in its
// worktrees; a file the worktree changed asks again.
func TestTrustStoreSharesReceiptsAcrossTheWorktreesOfAProject(t *testing.T) {
	main, wt := gitwstest.ProjectWithWorktree(t)
	store := hooks.NewTrustStore(t.TempDir())
	src := &hooks.Source{Path: main + "/.coddy/hooks.json", Display: ".coddy/hooks.json", Scope: hooks.ScopeProject, Digest: "sha256:one"}
	if err := store.Approve(hooks.CanonicalWorkspace(main), src); err != nil {
		t.Fatal(err)
	}
	if !store.Approved(hooks.CanonicalWorkspace(wt), ".coddy/hooks.json", "sha256:one") {
		t.Fatal("a hook file the main checkout approved must be approved in its worktree")
	}
	if store.Approved(hooks.CanonicalWorkspace(wt), ".coddy/hooks.json", "sha256:two") {
		t.Fatal("a hook file the worktree changed must ask again")
	}
}

// A worktree approving its changed hook file keeps the main checkout's
// approval of its own: each checkout keeps its receipt.
func TestApprovingAChangedHookFileInAWorktreeKeepsTheMainCheckouts(t *testing.T) {
	main, wt := gitwstest.ProjectWithWorktree(t)
	store := hooks.NewTrustStore(t.TempDir())
	for dir, digest := range map[string]string{main: "sha256:main", wt: "sha256:branch"} {
		src := &hooks.Source{Path: dir + "/.coddy/hooks.json", Display: ".coddy/hooks.json", Scope: hooks.ScopeProject, Digest: digest}
		if err := store.Approve(hooks.CanonicalWorkspace(dir), src); err != nil {
			t.Fatal(err)
		}
	}
	if !store.Approved(hooks.CanonicalWorkspace(main), ".coddy/hooks.json", "sha256:main") || !store.Approved(hooks.CanonicalWorkspace(wt), ".coddy/hooks.json", "sha256:branch") {
		t.Fatal("each checkout must keep its own receipt")
	}
}
