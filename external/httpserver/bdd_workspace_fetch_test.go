//go:build http

package httpserver

// Steps of features/workspace_switching.feature that drive the refresh of
// remote branches behind the branch list (POST /coddy/workspace/fetch) and
// picking a branch that exists only on a remote.

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cucumber/godog"
)

func registerWorkspaceFetchSteps(sc *godog.ScenarioContext, s *wsFeatureState) {
	sc.Step(`^repository "([^"]+)" has a second remote "([^"]+)"$`, s.repoHasSecondRemote)
	sc.Step(`^branch "([^"]+)" was pushed to remote "([^"]+)" of repository "([^"]+)" after its last fetch$`, s.branchPushedAfterLastFetch)
	sc.Step(`^branch "([^"]+)" is deleted from remote "([^"]+)" of repository "([^"]+)"$`, s.branchDeletedOnRemote)
	sc.Step(`^repository "([^"]+)" has uncommitted changes$`, s.repoHasUncommittedChanges)
	sc.Step(`^I refresh the remote branches$`, s.refreshRemoteBranches)
	sc.Step(`^the session refreshed the remote branches$`, s.sessionRefreshedRemoteBranches)
	sc.Step(`^I refresh the remote branches of folder "([^"]+)" before a session exists$`, s.refreshRemoteBranchesOfFolder)
	sc.Step(`^the refresh reports status "([^"]+)" for remotes "([^"]+)"$`, s.refreshReports)
	sc.Step(`^the context lists the remote-only branches "([^"]+)"$`, s.contextListsRemoteBranches)
	sc.Step(`^the context lists no remote-only branch "([^"]+)"$`, s.contextListsNoRemoteBranch)
	sc.Step(`^repository "([^"]+)" still has its uncommitted changes$`, s.repoStillHasUncommittedChanges)
	sc.Step(`^branch "([^"]+)" of repository "([^"]+)" tracks "([^"]+)"$`, s.branchTracks)
}

// remoteURL returns the bare repository behind remote of folder name.
func (s *wsFeatureState) remoteURL(name, remote string) (string, error) {
	dir, ok := s.folders[name]
	if !ok {
		return "", fmt.Errorf("unknown folder %q", name)
	}
	out, err := bddGitCmd(dir, "remote", "get-url", remote).Output()
	if err != nil {
		return "", fmt.Errorf("remote %q of %q: %w", remote, name, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *wsFeatureState) repoHasSecondRemote(name, remote string) error {
	dir, ok := s.folders[name]
	if !ok {
		return fmt.Errorf("unknown folder %q", name)
	}
	bare := filepath.Join(s.root, name+"-"+remote+".git")
	if err := bddGit(dir, "clone", "--bare", dir, bare); err != nil {
		return err
	}
	if err := bddGit(dir, "remote", "add", remote, bare); err != nil {
		return err
	}
	return bddGit(dir, "fetch", remote)
}

// branchPushedAfterLastFetch publishes a commit on branch to the remote from
// a clone of its own, so the repository under test has not seen it yet.
func (s *wsFeatureState) branchPushedAfterLastFetch(branch, remote, name string) error {
	bare, err := s.remoteURL(name, remote)
	if err != nil {
		return err
	}
	publisher, err := os.MkdirTemp(s.root, "publisher-*")
	if err != nil {
		return err
	}
	if err := bddGit(s.root, "clone", bare, publisher); err != nil {
		return err
	}
	if err := bddGit(publisher, "checkout", "-b", branch); err != nil {
		return err
	}
	if err := bddGit(publisher, "-c", "user.email=coddy@test", "-c", "user.name=coddy",
		"commit", "--allow-empty", "-m", "publish "+branch); err != nil {
		return err
	}
	return bddGit(publisher, "push", "origin", branch)
}

// branchDeletedOnRemote deletes branch in the bare repository behind remote,
// as a colleague's merge-and-delete would.
func (s *wsFeatureState) branchDeletedOnRemote(branch, remote, name string) error {
	bare, err := s.remoteURL(name, remote)
	if err != nil {
		return err
	}
	return bddGit(bare, "branch", "-D", branch)
}

// uncommittedState is what opening the branch list must leave alone.
func uncommittedState(dir string) (string, error) {
	var parts []string
	for _, args := range [][]string{
		{"rev-parse", "HEAD"},
		{"symbolic-ref", "--short", "HEAD"},
		{"status", "--porcelain", "--untracked-files=all"},
		{"diff", "--cached"},
		{"for-each-ref", "--format=%(refname) %(objectname)", "refs/heads"},
	} {
		out, err := bddGitCmd(dir, args...).Output()
		if err != nil {
			return "", fmt.Errorf("git %v: %w", args, err)
		}
		parts = append(parts, string(out))
	}
	for _, file := range []string{"draft.txt", "staged.txt"} {
		body, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			return "", err
		}
		parts = append(parts, string(body))
	}
	return strings.Join(parts, "\x00"), nil
}

func (s *wsFeatureState) repoHasUncommittedChanges(name string) error {
	dir, ok := s.folders[name]
	if !ok {
		return fmt.Errorf("unknown folder %q", name)
	}
	if err := os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("staged\n"), 0o644); err != nil {
		return err
	}
	if err := bddGit(dir, "add", "staged.txt"); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "draft.txt"), []byte("draft\n"), 0o644); err != nil {
		return err
	}
	state, err := uncommittedState(dir)
	if err != nil {
		return err
	}
	s.localState = state
	return nil
}

func (s *wsFeatureState) repoStillHasUncommittedChanges(name string) error {
	dir, ok := s.folders[name]
	if !ok {
		return fmt.Errorf("unknown folder %q", name)
	}
	state, err := uncommittedState(dir)
	if err != nil {
		return err
	}
	if state != s.localState {
		return fmt.Errorf("the local state changed:\nbefore %q\nafter  %q", s.localState, state)
	}
	return nil
}

func (s *wsFeatureState) postFetch(query string) error {
	target := s.ts.URL + "/coddy/workspace/fetch"
	if query != "" {
		target += "?" + query
	}
	req, err := http.NewRequest(http.MethodPost, target, nil)
	if err != nil {
		return err
	}
	return s.do(req)
}

func (s *wsFeatureState) refreshRemoteBranches() error {
	if s.sessionID == "" {
		return fmt.Errorf("no session created")
	}
	return s.postFetch("")
}

func (s *wsFeatureState) sessionRefreshedRemoteBranches() error {
	if err := s.refreshRemoteBranches(); err != nil {
		return err
	}
	return s.refreshReports("ok", "")
}

func (s *wsFeatureState) refreshRemoteBranchesOfFolder(name string) error {
	dir, ok := s.folders[name]
	if !ok {
		return fmt.Errorf("unknown folder %q", name)
	}
	if s.sessionID != "" {
		return fmt.Errorf("a session already exists")
	}
	return s.postFetch("path=" + url.QueryEscape(dir))
}

// refreshReports checks the fetch block of the last answer; an empty remote
// list only checks the status.
func (s *wsFeatureState) refreshReports(status, remotes string) error {
	if s.status != http.StatusOK {
		return fmt.Errorf("refresh returned %d: %v", s.status, s.body)
	}
	fetch, _ := s.body["fetch"].(map[string]interface{})
	if fetch == nil {
		return fmt.Errorf("the answer carries no fetch block: %v", s.body)
	}
	if got, _ := fetch["status"].(string); got != status {
		return fmt.Errorf("fetch status = %q, want %q: %v", got, status, fetch)
	}
	if remotes == "" {
		return nil
	}
	var got []string
	raw, _ := fetch["remotes"].([]interface{})
	for _, r := range raw {
		if v, ok := r.(string); ok {
			got = append(got, v)
		}
	}
	if want := bddSplitList(remotes); !slices.Equal(got, want) {
		return fmt.Errorf("fetched remotes = %v, want %v", got, want)
	}
	return nil
}

// lastRemoteBranches reads remote_branches from the last answer: the
// context the branch list was rendered from, not a fresh one.
func (s *wsFeatureState) lastRemoteBranches() ([]string, error) {
	if s.body == nil {
		return nil, fmt.Errorf("no answer to read")
	}
	raw, ok := s.body["remote_branches"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("the answer carries no remote_branches: %v", s.body)
	}
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if v, ok := r.(string); ok {
			out = append(out, v)
		}
	}
	return out, nil
}

func (s *wsFeatureState) contextListsRemoteBranches(list string) error {
	got, err := s.lastRemoteBranches()
	if err != nil {
		return err
	}
	if want := bddSplitList(list); !slices.Equal(got, want) {
		return fmt.Errorf("remote branches = %v, want %v", got, want)
	}
	return nil
}

func (s *wsFeatureState) contextListsNoRemoteBranch(name string) error {
	got, err := s.lastRemoteBranches()
	if err != nil {
		return err
	}
	if slices.Contains(got, name) {
		return fmt.Errorf("remote branches %v already list %q", got, name)
	}
	return nil
}

func (s *wsFeatureState) branchTracks(branch, name, upstream string) error {
	dir, ok := s.folders[name]
	if !ok {
		return fmt.Errorf("unknown folder %q", name)
	}
	out, err := bddGitCmd(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", branch+"@{upstream}").Output()
	if err != nil {
		return fmt.Errorf("upstream of %q: %w", branch, err)
	}
	if got := strings.TrimSpace(string(out)); got != upstream {
		return fmt.Errorf("upstream of %q = %q, want %q", branch, got, upstream)
	}
	return nil
}
