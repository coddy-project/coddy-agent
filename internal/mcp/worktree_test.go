package mcp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/gitws/gitwstest"
)

// projectWithWorktree builds a repository and a linked worktree of it the way
// worktree_open does (under <main>/.coddy/worktrees/), and returns both roots.
func projectWithWorktree(t *testing.T) (main, worktree string) {
	return gitwstest.ProjectWithWorktree(t)
}

// A folder of a worktree is the same folder of the project as in the main
// checkout; any other folder is itself.
func TestTrustWorkspaceOfAWorktreeIsTheSameFolderOfItsMainCheckout(t *testing.T) {
	main, wt := projectWithWorktree(t)
	other := t.TempDir()
	for dir, want := range map[string]string{
		wt:                         main,
		filepath.Join(wt, "pkg"):   filepath.Join(main, "pkg"),
		main:                       main,
		filepath.Join(main, "pkg"): filepath.Join(main, "pkg"),
		other:                      other,
	} {
		if got := TrustWorkspace(dir); got != CanonicalWorkspace(want) {
			t.Errorf("TrustWorkspace(%s) = %q, want %q", dir, got, CanonicalWorkspace(want))
		}
	}
	if TrustWorkspace("") != "" {
		t.Error("an empty workspace must stay empty")
	}
}

// What the main checkout approved is approved in its worktrees, a declaration
// a worktree changed asks again, and a revoke anywhere in the project revokes
// it for the project.
func TestTrustStoreSharesApprovalsAcrossTheWorktreesOfAProject(t *testing.T) {
	main, wt := projectWithWorktree(t)
	store := NewTrustStore(t.TempDir())
	srv := projectServer("demo", "run-me")
	if err := store.Approve(main, config.MCPJSONPath(main), srv); err != nil {
		t.Fatal(err)
	}
	if !store.Approved(wt, srv) {
		t.Fatal("a declaration the main checkout approved must be approved in its worktree")
	}
	if store.Approved(wt, projectServer("demo", "run-something-else")) {
		t.Fatal("a declaration the worktree changed must ask again")
	}
	if store.Approved(t.TempDir(), srv) {
		t.Fatal("approval must not carry to another project")
	}
	// Approved in the worktree, a new server is approved for the project:
	// merged with the same declaration, it does not ask again.
	added := projectServer("added", "run-new")
	if err := store.Approve(wt, config.MCPJSONPath(wt), added); err != nil {
		t.Fatal(err)
	}
	if !store.Approved(main, added) {
		t.Fatal("a declaration approved in a worktree must be approved for the project")
	}
	if len(store.Records(wt)) != 2 || len(store.Records(main)) != 2 {
		t.Fatalf("records: worktree %d, main %d; want the project's 2 in both", len(store.Records(wt)), len(store.Records(main)))
	}
	if removed, err := store.Revoke(wt, "demo"); err != nil || !removed {
		t.Fatalf("Revoke from the worktree = %t, %v", removed, err)
	}
	if store.Approved(main, srv) {
		t.Fatal("a revoke in a worktree must revoke for the project")
	}
}

// A project server the operator switched off is off in every checkout of the
// project; deleting it from a worktree's file leaves the main checkout's
// switches alone, since the main checkout still declares it.
func TestProjectSwitchesHoldInEveryWorktreeOfTheProject(t *testing.T) {
	cfg, _, _ := writeTestConfig(t)
	main, wt := projectWithWorktree(t)
	for _, dir := range []string{main, wt} {
		if err := config.UpsertMCPJSONServer(config.MCPJSONPath(dir), "proj-srv", config.MCPJSONServer{Command: "proj-mcp"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := SetServerDisabled(cfg, main, "proj-srv", true); err != nil {
		t.Fatal(err)
	}
	disabledIn := func(dir string) bool {
		t.Helper()
		servers, err := ListManagedServers(cfg, dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range servers {
			if s.Config.Name == "proj-srv" {
				return s.Config.Disabled
			}
		}
		t.Fatalf("proj-srv not listed in %s", dir)
		return false
	}
	if !disabledIn(wt) {
		t.Fatal("a server switched off in the main checkout must be off in its worktree")
	}
	if err := DeleteServer(cfg, wt, "proj-srv"); err != nil {
		t.Fatal(err)
	}
	if !disabledIn(main) {
		t.Fatal("deleting the server from a worktree must not switch it back on in the main checkout")
	}
}

// A project server whose declaration does not name ${CWD} resolves to the
// same process in every checkout of the project, so the main checkout and its
// worktrees share it; one that names ${CWD} sees each checkout's own files
// and runs once per checkout.
func TestPoolKeyOfAProjectServerFollowsWhatTheDeclarationSees(t *testing.T) {
	main, wt := projectWithWorktree(t)
	plain := projectManaged(projectServer("demo", "run-me"))
	bound := projectManaged(config.MCPServerConfig{Name: "files", Command: "serve-files", Args: []string{"${CWD}"}})
	if PoolKey(plain, main) != PoolKey(plain, wt) {
		t.Error("a project server that does not name ${CWD} must share one key across the project's checkouts")
	}
	if PoolKey(plain, main) == PoolKey(plain, t.TempDir()) {
		t.Error("a project server must not share its key with another project")
	}
	if PoolKey(bound, main) == PoolKey(bound, wt) {
		t.Error("a project server that names ${CWD} must keep one key per checkout")
	}
}

// The main checkout and a worktree of it share one process of a project
// server, while a server that names ${CWD} runs in each.
func TestPoolRunsAProjectServerOnceForTheMainCheckoutAndItsWorktrees(t *testing.T) {
	main, wt := projectWithWorktree(t)
	dir := t.TempDir()
	plainRecord, boundRecord := filepath.Join(dir, "plain"), filepath.Join(dir, "bound")
	pool := newTestPool(t)
	plain := projectManaged(poolStub("plain", plainRecord))
	bound := projectManaged(poolStub("bound", boundRecord, config.EnvVarConfig{Name: "WORKSPACE", Value: "${CWD}"}))
	for _, ws := range []string{main, wt, wt} {
		for _, srv := range []ManagedServer{plain, bound} {
			c := acquire(t, pool, srv, ws)
			t.Cleanup(func() { _ = c.Close() })
		}
	}
	for record, want := range map[string]int{plainRecord: 1, boundRecord: 2} {
		if got := recorded(record, "started"); got != want {
			t.Fatalf("%s started %d times for a main checkout and its worktree, want %d", filepath.Base(record), got, want)
		}
	}
}

// A branch that changes a declaration and approves it in its worktree leaves
// the main checkout's approval of its own declaration in place: each checkout
// keeps its receipt, and the project approves both digests. Approving again
// in one checkout replaces only that checkout's receipt; an explicit revoke
// still takes the name from the whole project.
func TestApprovingAChangedDeclarationInAWorktreeLeavesTheMainCheckoutsApproval(t *testing.T) {
	main, wt := projectWithWorktree(t)
	store := NewTrustStore(t.TempDir())
	inMain, inBranch := projectServer("tools", "run-main"), projectServer("tools", "run-branch")
	if err := store.Approve(main, config.MCPJSONPath(main), inMain); err != nil {
		t.Fatal(err)
	}
	if err := store.Approve(wt, config.MCPJSONPath(wt), inBranch); err != nil {
		t.Fatal(err)
	}
	if !store.Approved(main, inMain) || !store.Approved(wt, inBranch) {
		t.Fatalf("main approved %t, branch approved %t; want both", store.Approved(main, inMain), store.Approved(wt, inBranch))
	}
	again := projectServer("tools", "run-main-v2")
	if err := store.Approve(main, config.MCPJSONPath(main), again); err != nil {
		t.Fatal(err)
	}
	if store.Approved(main, inMain) || !store.Approved(main, again) || !store.Approved(wt, inBranch) {
		t.Fatal("approving again in the main checkout must replace its own receipt and keep the worktree's")
	}
	if _, err := store.Revoke(wt, "tools"); err != nil {
		t.Fatal(err)
	}
	if store.Approved(main, again) || store.Approved(wt, inBranch) {
		t.Fatal("an explicit revoke must take the name from the whole project")
	}
}

// A tree that brings its own repository storage and a link to another project
// gets none of that project's approvals.
func TestAnArchiveWithItsOwnStorageBorrowsNoApproval(t *testing.T) {
	victim := t.TempDir()
	store := NewTrustStore(t.TempDir())
	srv := projectServer("tools", "node ${CWD}/tools/mcp.js")
	if err := store.Approve(victim, config.MCPJSONPath(victim), srv); err != nil {
		t.Fatal(err)
	}
	evil := t.TempDir()
	for rel, body := range map[string]string{
		".git":                         "gitdir: v/.git/worktrees/x\n",
		"v/.git/worktrees/x/commondir": "../..\n",
		"v/.git/worktrees/x/gitdir":    "../../../../.git\n",
		"v/.git/HEAD":                  "ref: refs/heads/main\n",
	} {
		path := filepath.Join(evil, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(victim, filepath.Join(evil, "v", "pkg")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(evil, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if store.Approved(filepath.Join(evil, "pkg"), srv) {
		t.Fatal("a folder of an archive borrowed another project's approval")
	}
}
