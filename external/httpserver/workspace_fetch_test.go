//go:build http

package httpserver

// Edge cases of POST /coddy/workspace/fetch and of picking a remote branch;
// the happy path is features/workspace_switching.feature. The tests drive the
// same harness as the feature, through its steps.

import (
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/gitws"
)

func newWorkspaceFetchState(t *testing.T) *wsFeatureState {
	t.Helper()
	if !gitws.GitAvailable() {
		t.Skip("git binary not available")
	}
	s := &wsFeatureState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.close)
	if err := s.startServer(); err != nil {
		t.Fatal(err)
	}
	return s
}

func mustStep(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (s *wsFeatureState) fetchBlock(t *testing.T) map[string]interface{} {
	t.Helper()
	if s.status != http.StatusOK {
		t.Fatalf("refresh returned %d: %v", s.status, s.body)
	}
	fetch, _ := s.body["fetch"].(map[string]interface{})
	if fetch == nil {
		t.Fatalf("no fetch block: %v", s.body)
	}
	return fetch
}

// A remote that cannot be reached leaves the workspace usable: the answer is
// still 200 with the branches as they stand, and says the list is not fresh.
func TestWorkspaceFetchReportsAFailedRemoteAndKeepsTheList(t *testing.T) {
	s := newWorkspaceFetchState(t)
	mustStep(t, s.gitRepo("repo", "main, feature/login"))
	mustStep(t, s.branchPushedAfterLastFetch("feature/seen", "origin", "repo"))
	mustStep(t, bddGit(s.folders["repo"], "fetch", "origin"))
	mustStep(t, bddGit(s.folders["repo"], "remote", "add", "broken", filepath.Join(s.root, "missing.git")))
	mustStep(t, s.sessionRootedAt("repo"))

	mustStep(t, s.refreshRemoteBranches())
	fetch := s.fetchBlock(t)
	if fetch["status"] != "failed" {
		t.Fatalf("status = %v, want failed: %v", fetch["status"], fetch)
	}
	if msg, _ := fetch["error"].(string); !strings.Contains(msg, "broken") {
		t.Fatalf("error = %q, want it to name the remote", msg)
	}
	if _, ok := fetch["fetched_at"]; ok {
		t.Fatalf("a failed refresh must not carry a fetch time: %v", fetch)
	}
	branches, _ := s.body["branches"].([]interface{})
	if len(branches) != 2 {
		t.Fatalf("branches = %v, want the local list still there", branches)
	}
	got, err := s.lastRemoteBranches()
	if err != nil || !slices.Contains(got, "feature/seen") {
		t.Fatalf("remote branches = %v, %v; want the cached ones kept", got, err)
	}
}

func TestWorkspaceFetchReportsARemoteThatDoesNotAnswer(t *testing.T) {
	s := newWorkspaceFetchState(t)
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
	prev := workspaceFetchTimeout
	workspaceFetchTimeout = 500 * time.Millisecond
	t.Cleanup(func() { workspaceFetchTimeout = prev })

	mustStep(t, s.gitRepo("repo", "main"))
	mustStep(t, bddGit(s.folders["repo"], "remote", "set-url", "origin", "http://"+ln.Addr().String()+"/silent.git"))
	mustStep(t, s.sessionRootedAt("repo"))

	start := time.Now()
	mustStep(t, s.refreshRemoteBranches())
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("the refresh took %s", elapsed)
	}
	fetch := s.fetchBlock(t)
	if fetch["status"] != "failed" {
		t.Fatalf("status = %v, want failed", fetch)
	}
	if msg, _ := fetch["error"].(string); !strings.Contains(msg, "did not answer within") {
		t.Fatalf("error = %q, want the deadline named", msg)
	}
}

func TestWorkspaceFetchSkipsARepositoryWithoutRemotes(t *testing.T) {
	s := newWorkspaceFetchState(t)
	mustStep(t, s.gitRepo("repo", "main"))
	mustStep(t, bddGit(s.folders["repo"], "remote", "remove", "origin"))
	mustStep(t, s.sessionRootedAt("repo"))

	mustStep(t, s.refreshRemoteBranches())
	fetch := s.fetchBlock(t)
	if fetch["status"] != "skipped" || fetch["reason"] != "no remotes to fetch" {
		t.Fatalf("fetch = %v, want skipped for no remotes", fetch)
	}
}

func TestWorkspaceFetchSkipsAFolderWithoutGit(t *testing.T) {
	s := newWorkspaceFetchState(t)
	mustStep(t, s.plainFolder("plain"))
	mustStep(t, s.postFetch("path="+url.QueryEscape(s.folders["plain"])))
	fetch := s.fetchBlock(t)
	if fetch["status"] != "skipped" || fetch["reason"] != "not a git repository" {
		t.Fatalf("fetch = %v, want skipped outside git", fetch)
	}
	if _, ok := s.body["remote_branches"]; ok {
		t.Fatalf("a folder without git carries remote branches: %v", s.body)
	}
}

func TestWorkspaceFetchRejectsAMissingFolder(t *testing.T) {
	s := newWorkspaceFetchState(t)
	mustStep(t, s.postFetch("path="+url.QueryEscape(filepath.Join(s.root, "missing"))))
	if s.status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", s.status)
	}
}

// Picking upstream/<name> while an unrelated local <name> exists would put the
// session on the local history instead of the one picked: refused with 409,
// nothing checked out.
func TestPickingARemoteBranchShadowedByAnUnrelatedLocalOneIsRefused(t *testing.T) {
	s := newWorkspaceFetchState(t)
	mustStep(t, s.gitRepo("repo", "main, feature/login"))
	mustStep(t, s.repoHasSecondRemote("repo", "upstream"))
	mustStep(t, s.sessionRootedAt("repo"))

	mustStep(t, s.switchToBranch("upstream/feature/login"))
	if s.status != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %v", s.status, s.body)
	}
	mustStep(t, s.contextOnBranch("main"))
}
