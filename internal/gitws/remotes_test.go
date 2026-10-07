package gitws

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// addRemote creates a bare copy of dir's history and registers it as the
// remote name, fetched once, so the repository starts with fresh refs from it.
func addRemote(t *testing.T, dir, name string) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), name+".git")
	mustGit(t, dir, "clone", "--bare", dir, bare)
	mustGit(t, dir, "remote", "add", name, bare)
	mustGit(t, dir, "fetch", name)
	return bare
}

// publishBranch pushes a new commit on branch to the remote at url from a
// clone of its own, the way a colleague publishes a branch after the
// repository's last fetch. It returns the pushed commit.
func publishBranch(t *testing.T, url, branch string) string {
	t.Helper()
	publisher := filepath.Join(t.TempDir(), "publisher")
	mustGit(t, t.TempDir(), "clone", url, publisher)
	if mustGit(t, publisher, "branch", "--list", branch) == "" {
		mustGit(t, publisher, "checkout", "-b", branch)
	} else {
		mustGit(t, publisher, "checkout", branch)
	}
	if err := os.WriteFile(filepath.Join(publisher, BranchDirName(branch)+".txt"), []byte(branch+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, publisher, "add", ".")
	mustGit(t, publisher, "-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "-m", "publish "+branch)
	mustGit(t, publisher, "push", "origin", branch)
	return mustGit(t, publisher, "rev-parse", "HEAD")
}

// localState is everything a refresh of remote refs must leave alone: the
// checked-out branch and commit, the index, the working tree (tracked edits,
// staged and untracked files) and every local branch and tag.
type localState struct {
	head, branch, status, diff, staged, heads, tags, file string
}

func captureLocalState(t *testing.T, dir string) localState {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(dir, "work.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return localState{
		head:   mustGit(t, dir, "rev-parse", "HEAD"),
		branch: mustGit(t, dir, "symbolic-ref", "--short", "HEAD"),
		status: mustGit(t, dir, "status", "--porcelain", "--untracked-files=all"),
		diff:   mustGit(t, dir, "diff"),
		staged: mustGit(t, dir, "diff", "--cached"),
		heads:  mustGit(t, dir, "for-each-ref", "--format=%(refname) %(objectname)", "refs/heads"),
		tags:   mustGit(t, dir, "for-each-ref", "--format=%(refname) %(objectname)", "refs/tags"),
		file:   string(body),
	}
}

// dirtyWorkingCopy leaves dir with a tracked edit, a staged new file and an
// untracked file on top of a committed work.txt.
func dirtyWorkingCopy(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "work.txt")
	mustGit(t, dir, "-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "-m", "work")
	mustGit(t, dir, "tag", "v-local")
	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("edited, not staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, dir, "add", "staged.txt")
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFetchRemotesRefreshesEveryRemoteAndTouchesNothingLocal(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := initRepo(t)
		upstream := addRemote(t, dir, "upstream")
		origin := mustGit(t, dir, "remote", "get-url", "origin")
		dirtyWorkingCopy(t, dir)
		fresh := publishBranch(t, origin, "feature/fresh")
		publishBranch(t, upstream, "fix/upstream-only")
		// A new commit on a branch the repository has locally: the fetch moves
		// the remote-tracking ref, never the local branch.
		publishBranch(t, origin, "feature/login")

		if got := RemoteBranches(dir); slices.Contains(got, "feature/fresh") {
			t.Fatalf("the branch is listed before any fetch: %v", got)
		}
		before := captureLocalState(t, dir)

		remotes, err := FetchRemotes(context.Background(), dir)
		if err != nil {
			t.Fatalf("FetchRemotes: %v", err)
		}
		if !slices.Equal(remotes, []string{"origin", "upstream"}) {
			t.Fatalf("remotes = %v, want origin and upstream", remotes)
		}
		got := RemoteBranches(dir)
		for _, want := range []string{"feature/fresh", "fix/upstream-only"} {
			if !slices.Contains(got, want) {
				t.Fatalf("remote branches = %v, want %s", got, want)
			}
		}
		if got := mustGit(t, dir, "rev-parse", "refs/remotes/origin/feature/fresh"); got != fresh {
			t.Fatalf("origin/feature/fresh = %s, want the published %s", got, fresh)
		}
		if after := captureLocalState(t, dir); after != before {
			t.Fatalf("the fetch changed local state:\nbefore %+v\nafter  %+v", before, after)
		}
	})
}

func TestRemoteBranchesListsOnlyBranchesWithoutALocalOne(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := initRepo(t)
		origin := mustGit(t, dir, "remote", "get-url", "origin")
		publishBranch(t, origin, "feature/remote-only")
		mustGit(t, dir, "fetch", "origin")

		got := RemoteBranches(dir)
		if !slices.Equal(got, []string{"feature/remote-only"}) {
			t.Fatalf("remote branches = %v, want only feature/remote-only (main, feature/login and HEAD have a local name)", got)
		}
	})
}

// Remote branches are listed by the names a local branch would carry, once
// however many remotes have them. A remote whose name holds a slash still
// splits at the remote: "team/core/fix/x" is branch fix/x of remote team/core.
func TestRemoteBranchesListsBranchNamesOnce(t *testing.T) {
	refs := []string{
		"refs/heads/main",
		"refs/remotes/team/HEAD",
		"refs/remotes/team/main",
		"refs/remotes/team/core/fix/x",
		"refs/remotes/team/feature",
		"refs/remotes/upstream/feature",
		"refs/remotes/gone/stale",
	}
	got := remoteBranchesFrom([]string{"team", "team/core", "upstream"}, refs)
	want := []string{"feature", "fix/x"}
	if !slices.Equal(got, want) {
		t.Fatalf("remote branches = %v, want %v", got, want)
	}
}

func TestFetchRemotesPrunesBranchesDeletedOnTheRemote(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := initRepo(t)
		origin := mustGit(t, dir, "remote", "get-url", "origin")
		publishBranch(t, origin, "feature/gone")
		if _, err := FetchRemotes(context.Background(), dir); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(RemoteBranches(dir), "feature/gone") {
			t.Fatalf("remote branches = %v, want feature/gone", RemoteBranches(dir))
		}
		mustGit(t, origin, "branch", "-D", "feature/gone")

		if _, err := FetchRemotes(context.Background(), dir); err != nil {
			t.Fatal(err)
		}
		if got := RemoteBranches(dir); slices.Contains(got, "feature/gone") {
			t.Fatalf("a branch deleted on the remote is still listed: %v", got)
		}
	})
}

func TestFetchRemotesWithoutRemotesDoesNothing(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := t.TempDir()
		mustGit(t, dir, "init", "-b", "main")
		mustGit(t, dir, "-c", "user.email=coddy@test", "-c", "user.name=coddy", "commit", "--allow-empty", "-m", "init")

		remotes, err := FetchRemotes(context.Background(), dir)
		if err != nil || len(remotes) != 0 {
			t.Fatalf("FetchRemotes = %v, %v; want nothing fetched and no error", remotes, err)
		}
	})
}

func TestFetchRemotesReportsAFailingRemoteAndFetchesTheRest(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := initRepo(t)
		origin := mustGit(t, dir, "remote", "get-url", "origin")
		mustGit(t, dir, "remote", "add", "broken", filepath.Join(t.TempDir(), "missing.git"))
		publishBranch(t, origin, "feature/fresh")

		remotes, err := FetchRemotes(context.Background(), dir)
		if err == nil {
			t.Fatal("a remote that cannot be reached must be reported")
		}
		if !strings.Contains(err.Error(), "broken") {
			t.Fatalf("error = %v, want it to name the remote broken", err)
		}
		if !slices.Equal(remotes, []string{"broken", "origin"}) {
			t.Fatalf("remotes = %v, want both asked", remotes)
		}
		if !slices.Contains(RemoteBranches(dir), "feature/fresh") {
			t.Fatalf("origin was not fetched past the broken remote: %v", RemoteBranches(dir))
		}
	})
}

// A remote whose fetch refspec writes into refs/heads would move or, with
// --prune, delete local branches. It is left out and reported, so the caller
// never presents its branches as refreshed.
func TestFetchRemotesNeverWritesOutsideRemoteTrackingRefs(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := initRepo(t)
		origin := mustGit(t, dir, "remote", "get-url", "origin")
		mustGit(t, dir, "remote", "add", "mirror", origin)
		mustGit(t, dir, "config", "--replace-all", "remote.mirror.fetch", "+refs/heads/*:refs/heads/*")
		mustGit(t, dir, "branch", "doomed")
		pushed := publishBranch(t, origin, "feature/login")
		localLogin := mustGit(t, dir, "rev-parse", "refs/heads/feature/login")

		remotes, err := FetchRemotes(context.Background(), dir)
		if err == nil || !strings.Contains(err.Error(), "mirror") {
			t.Fatalf("error = %v, want the mirror remote reported", err)
		}
		if !slices.Equal(remotes, []string{"origin"}) {
			t.Fatalf("remotes = %v, want only origin fetched", remotes)
		}
		if got := mustGit(t, dir, "rev-parse", "refs/heads/feature/login"); got != localLogin {
			t.Fatalf("local feature/login moved to %s", got)
		}
		if mustGit(t, dir, "branch", "--list", "doomed") == "" {
			t.Fatal("a local branch absent from the remote was pruned")
		}
		if got := mustGit(t, dir, "rev-parse", "refs/remotes/origin/feature/login"); got != pushed {
			t.Fatalf("origin/feature/login = %s, want %s", got, pushed)
		}
	})
}

func TestFetchRemotesSkipsRemotesLeftOutOfFetchAll(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := initRepo(t)
		mustGit(t, dir, "remote", "add", "archive", filepath.Join(t.TempDir(), "missing.git"))
		mustGit(t, dir, "config", "remote.archive.skipFetchAll", "true")

		remotes, err := FetchRemotes(context.Background(), dir)
		if err != nil {
			t.Fatalf("FetchRemotes: %v", err)
		}
		if !slices.Equal(remotes, []string{"origin"}) {
			t.Fatalf("remotes = %v, want only origin", remotes)
		}
	})
}

// A remote that accepts the connection and never answers must not hold the
// caller past its deadline.
func TestFetchRemotesGivesUpAtTheDeadline(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		var mu sync.Mutex
		var conns []net.Conn
		t.Cleanup(func() {
			_ = ln.Close()
			mu.Lock()
			defer mu.Unlock()
			for _, c := range conns {
				_ = c.Close()
			}
		})
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				mu.Lock()
				conns = append(conns, conn)
				mu.Unlock()
			}
		}()
		dir := initRepo(t)
		mustGit(t, dir, "remote", "set-url", "origin", "http://"+ln.Addr().String()+"/silent.git")

		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		start := time.Now()
		_, err = FetchRemotes(ctx, dir)
		if err == nil {
			t.Fatal("a silent remote must end in an error")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("error = %v, want the deadline", err)
		}
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Fatalf("FetchRemotes returned after %s", elapsed)
		}
	})
}

func TestFetchRemotesErrorsDoNotCarryURLCredentials(t *testing.T) {
	got := redactURLCredentials("fatal: unable to access 'https://bot:s3cret-token@git.example.com/repo.git/': 403\nssh://git@host/x")
	if strings.Contains(got, "s3cret-token") || strings.Contains(got, "bot:") {
		t.Fatalf("credentials survived: %q", got)
	}
	if !strings.Contains(got, "git.example.com/repo.git") {
		t.Fatalf("the host was lost: %q", got)
	}
}

func TestResolveBranch(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := initRepo(t)
		upstream := addRemote(t, dir, "upstream")
		publishBranch(t, upstream, "fix/upstream-only")
		publishBranch(t, upstream, "feature/login")
		publishBranch(t, upstream, "feature/both")
		publishBranch(t, mustGit(t, dir, "remote", "get-url", "origin"), "feature/both")
		mustGit(t, dir, "fetch", "upstream")
		mustGit(t, dir, "fetch", "origin")

		cases := []struct {
			name, pick string
			want       BranchTarget
			conflict   bool
		}{
			{name: "local branch", pick: "feature/login", want: BranchTarget{Branch: "feature/login"}},
			{name: "unknown name", pick: "brand-new", want: BranchTarget{Branch: "brand-new"}},
			{name: "remote branch without a local one", pick: "upstream/fix/upstream-only",
				want: BranchTarget{Branch: "fix/upstream-only", Remote: "upstream"}},
			// The branch list names a remote-only branch as a local one would be named.
			{name: "branch name that exists only on a remote", pick: "fix/upstream-only",
				want: BranchTarget{Branch: "fix/upstream-only", Remote: "upstream"}},
			{name: "branch name on several remotes prefers origin", pick: "feature/both",
				want: BranchTarget{Branch: "feature/both", Remote: "origin"}},
			// feature/login exists locally and tracks nothing: picking the
			// upstream one must not silently check out the unrelated local tip.
			{name: "remote branch shadowed by an unrelated local one", pick: "upstream/feature/login", conflict: true},
		}
		for _, tc := range cases {
			got, err := ResolveBranch(dir, tc.pick)
			if tc.conflict {
				if !errors.Is(err, ErrBranchConflict) {
					t.Fatalf("%s: ResolveBranch(%q) = %+v, %v; want ErrBranchConflict", tc.name, tc.pick, got, err)
				}
				continue
			}
			if err != nil || got != tc.want {
				t.Fatalf("%s: ResolveBranch(%q) = %+v, %v; want %+v", tc.name, tc.pick, got, err, tc.want)
			}
		}

		// Once the local branch tracks the remote one, the remote name is
		// simply another spelling of it.
		mustGit(t, dir, "branch", "--set-upstream-to=upstream/feature/login", "feature/login")
		got, err := ResolveBranch(dir, "upstream/feature/login")
		if err != nil || got != (BranchTarget{Branch: "feature/login"}) {
			t.Fatalf("tracked: ResolveBranch = %+v, %v", got, err)
		}
	})
}

func TestCheckoutTrackingCreatesALocalBranchFromTheRemote(t *testing.T) {
	eachBackend(t, func(t *testing.T) {
		dir := initRepo(t)
		upstream := addRemote(t, dir, "upstream")
		pushed := publishBranch(t, upstream, "fix/upstream-only")
		mustGit(t, dir, "fetch", "upstream")

		target, err := ResolveBranch(dir, "fix/upstream-only")
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckoutTracking(dir, target); err != nil {
			t.Fatalf("CheckoutTracking: %v", err)
		}
		if got := Describe(dir).Branch; got != "fix/upstream-only" {
			t.Fatalf("branch = %q, want fix/upstream-only", got)
		}
		if got := mustGit(t, dir, "rev-parse", "HEAD"); got != pushed {
			t.Fatalf("HEAD = %s, want the remote tip %s", got, pushed)
		}
		if got := mustGit(t, dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); got != "upstream/fix/upstream-only" {
			t.Fatalf("upstream = %q, want upstream/fix/upstream-only", got)
		}
	})
}

func TestEnsureWorktreeOpensARemoteBranchOfAnyRemote(t *testing.T) {
	dir := initRepo(t)
	upstream := addRemote(t, dir, "upstream")
	pushed := publishBranch(t, upstream, "fix/upstream-only")
	mustGit(t, dir, "fetch", "upstream")

	path, created, err := EnsureWorktree(dir, "fix/upstream-only")
	if err != nil || !created {
		t.Fatalf("EnsureWorktree = %q, %t, %v", path, created, err)
	}
	if filepath.Base(path) != "fix-upstream-only" {
		t.Fatalf("worktree folder = %q, want fix-upstream-only", filepath.Base(path))
	}
	if got := mustGit(t, path, "rev-parse", "HEAD"); got != pushed {
		t.Fatalf("worktree HEAD = %s, want the upstream tip %s", got, pushed)
	}
	if got := mustGit(t, path, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); got != "upstream/fix/upstream-only" {
		t.Fatalf("worktree upstream = %q", got)
	}
	// Asked again by either spelling, the same worktree comes back: the name
	// the branch list shows, or the remote-tracking one.
	for _, pick := range []string{"upstream/fix/upstream-only", "fix/upstream-only"} {
		again, createdAgain, err := EnsureWorktree(dir, pick)
		if err != nil || createdAgain || normPath(t, again) != normPath(t, path) {
			t.Fatalf("EnsureWorktree(%q) = %q, %t, %v; want the existing worktree", pick, again, createdAgain, err)
		}
	}
}

// The default branch picked through another remote is still the default
// branch.
func TestEnsureWorktreeRefusesTheDefaultBranchOfAnotherRemote(t *testing.T) {
	dir := initRepo(t)
	addRemote(t, dir, "upstream")
	mustGit(t, dir, "checkout", "--detach")
	mustGit(t, dir, "branch", "-D", "main")
	if _, _, err := EnsureWorktree(dir, "upstream/main"); err == nil || !strings.Contains(err.Error(), "default branch") {
		t.Fatalf("error = %v, want the default branch refused", err)
	}
}

// Callers that arrive while a fetch of the same repository runs wait for it
// and share its answer instead of racing it for the ref locks.
func TestFetchRemotesSharesOneFetchPerRepository(t *testing.T) {
	dir := t.TempDir()
	release := make(chan struct{})
	started := make(chan struct{}, 8)
	var mu sync.Mutex
	runs := 0
	prev := runFetch
	runFetch = func(ctx context.Context, _ string) ([]string, error) {
		mu.Lock()
		runs++
		mu.Unlock()
		started <- struct{}{}
		<-release
		return []string{"origin"}, nil
	}
	t.Cleanup(func() { runFetch = prev })

	results := make(chan []string, 3)
	go func() {
		remotes, _ := FetchRemotes(context.Background(), dir)
		results <- remotes
	}()
	<-started
	for range 2 {
		go func() {
			remotes, _ := FetchRemotes(context.Background(), dir)
			results <- remotes
		}()
	}
	// The followers are parked on the running call before it is released.
	waitFor(t, func() bool {
		fetchMu.Lock()
		defer fetchMu.Unlock()
		call := fetchCalls[fetchKey(dir)]
		return call != nil && call.waiters == 2
	})
	close(release)
	for range 3 {
		if got := <-results; !slices.Equal(got, []string{"origin"}) {
			t.Fatalf("remotes = %v", got)
		}
	}
	if runs != 1 {
		t.Fatalf("the fetch ran %d times, want once", runs)
	}
}

// A caller that joined a fetch whose own caller gave up does not inherit
// that cancellation: it runs the fetch itself.
func TestFetchRemotesRetriesAFetchItsLeaderCancelled(t *testing.T) {
	dir := t.TempDir()
	leaderIn := make(chan struct{})
	var mu sync.Mutex
	runs := 0
	prev := runFetch
	runFetch = func(ctx context.Context, _ string) ([]string, error) {
		mu.Lock()
		runs++
		n := runs
		mu.Unlock()
		if n == 1 {
			close(leaderIn)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return []string{"origin"}, nil
	}
	t.Cleanup(func() { runFetch = prev })

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leaderDone := make(chan error, 1)
	go func() {
		_, err := FetchRemotes(leaderCtx, dir)
		leaderDone <- err
	}()
	<-leaderIn
	followerDone := make(chan []string, 1)
	go func() {
		remotes, err := FetchRemotes(context.Background(), dir)
		if err != nil {
			t.Errorf("follower: %v", err)
		}
		followerDone <- remotes
	}()
	time.Sleep(50 * time.Millisecond)
	cancelLeader()
	if err := <-leaderDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("leader error = %v, want its own cancellation", err)
	}
	if got := <-followerDone; !slices.Equal(got, []string{"origin"}) {
		t.Fatalf("follower remotes = %v, want its own fetch's answer", got)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A remote-tracking name the repository has not fetched yet - a branch pushed
// to origin since the last fetch, or one of another remote - is looked up
// again after EnsureWorktree fetches, never turned into a local branch called
// origin/<name> started at the default branch.
func TestEnsureWorktreeResolvesARemoteNameAfterItsFetch(t *testing.T) {
	dir := initRepo(t)
	origin := mustGit(t, dir, "remote", "get-url", "origin")
	upstream := addRemote(t, dir, "upstream")
	pushedOrigin := publishBranch(t, origin, "feature/new")
	pushedUpstream := publishBranch(t, upstream, "fix/late")

	for _, tc := range []struct{ pick, branch, upstream, tip string }{
		{"origin/feature/new", "feature/new", "origin/feature/new", pushedOrigin},
		{"upstream/fix/late", "fix/late", "upstream/fix/late", pushedUpstream},
	} {
		path, created, err := EnsureWorktree(dir, tc.pick)
		if err != nil || !created {
			t.Fatalf("EnsureWorktree(%q) = %q, %t, %v", tc.pick, path, created, err)
		}
		if got := mustGit(t, path, "rev-parse", "--abbrev-ref", "HEAD"); got != tc.branch {
			t.Fatalf("%s: worktree branch = %q, want %q", tc.pick, got, tc.branch)
		}
		if got := mustGit(t, path, "rev-parse", "HEAD"); got != tc.tip {
			t.Fatalf("%s: worktree HEAD = %s, want the pushed %s", tc.pick, got, tc.tip)
		}
		if got := mustGit(t, path, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}"); got != tc.upstream {
			t.Fatalf("%s: upstream = %q, want %q", tc.pick, got, tc.upstream)
		}
	}
	if heads := mustGit(t, dir, "for-each-ref", "--format=%(refname)", "refs/heads/origin", "refs/heads/upstream"); heads != "" {
		t.Fatalf("local branches named after a remote were created: %s", heads)
	}
}

func TestEnsureWorktreeRefusesARemoteNameTheRemoteDoesNotHave(t *testing.T) {
	dir := initRepo(t)
	if _, _, err := EnsureWorktree(dir, "origin/feature/missing"); err == nil || !strings.Contains(err.Error(), "no branch") {
		t.Fatalf("error = %v, want the missing remote branch named", err)
	}
	if heads := mustGit(t, dir, "for-each-ref", "--format=%(refname)", "refs/heads/origin"); heads != "" {
		t.Fatalf("a local branch named after the remote was created: %s", heads)
	}
	for _, wt := range listWorktrees(dir) {
		if !wt.Main {
			t.Fatalf("a worktree was created at %q", wt.Path)
		}
	}
}

// Opening the branch list must never ask for credentials: an askpass program
// the environment names (a desktop's SSH_ASKPASS, an editor's GIT_ASKPASS)
// would put a dialog on the server's screen. Credential helpers still work;
// a remote that wants a password just fails.
func TestFetchRemotesNeverRunsAnAskpassProgram(t *testing.T) {
	if gitBin == "" {
		t.Skip("git binary not available")
	}
	if runtime.GOOS == "windows" {
		t.Skip("the askpass stand-in is a shell script")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="git"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	asked := filepath.Join(t.TempDir(), "asked")
	askpass := filepath.Join(t.TempDir(), "askpass.sh")
	if err := os.WriteFile(askpass, []byte("#!/bin/sh\necho \"$1\" >> "+asked+"\necho secret\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_ASKPASS", askpass)
	t.Setenv("SSH_ASKPASS", askpass)
	dir := initRepo(t)
	mustGit(t, dir, "remote", "set-url", "origin", srv.URL+"/private.git")
	mustGit(t, dir, "config", "core.askPass", askpass)

	if _, err := FetchRemotes(context.Background(), dir); err == nil {
		t.Fatal("a remote that wants a password must fail without one")
	}
	if body, err := os.ReadFile(asked); err == nil {
		t.Fatalf("the askpass program was asked for: %s", body)
	}
}
