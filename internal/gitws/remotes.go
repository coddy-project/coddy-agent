package gitws

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// ErrBranchConflict reports a remote branch picked by its remote-tracking name
// (upstream/feature) whose local name is already taken by a branch that does
// not track it: checking that one out would silently put the session on a
// different history than the one picked.
var ErrBranchConflict = errors.New("branch name already taken locally")

// BranchTarget is what a picked branch name stands for in a repository.
type BranchTarget struct {
	// Branch is the local branch to check out.
	Branch string
	// Remote is set when Branch does not exist yet: it is created at
	// refs/remotes/<Remote>/<Branch> and tracks it.
	Remote string
}

// upstreamRef is the remote-tracking ref a new local branch starts from.
func (t BranchTarget) upstreamRef() string {
	return "refs/remotes/" + t.Remote + "/" + t.Branch
}

// ResolveBranch maps a picked name to the local branch it means:
//   - a local branch of that name, as it is;
//   - a branch name that exists only on remotes (fix/x, as RemoteBranches
//     lists it): a new local fix/x tracking the remote one - origin's when
//     origin has it, else that of the first remote by name that does;
//   - a remote-tracking name (upstream/fix/x): the local fix/x, the existing
//     one when it already tracks upstream/fix/x, a new one tracking it when
//     there is none, and ErrBranchConflict when an unrelated fix/x is in the
//     way.
//
// Any other name is returned as it is, for git to accept or refuse.
func ResolveBranch(dir, name string) (BranchTarget, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return BranchTarget{}, fmt.Errorf("empty branch name")
	}
	target := BranchTarget{Branch: name}
	if strings.HasPrefix(name, "-") || name == "HEAD" {
		return target, nil
	}
	refs := listRefs(dir)
	if slices.Contains(refs, "refs/heads/"+name) {
		return target, nil
	}
	remotes := listRemotes(dir)
	if remote := remoteHolding(remotes, refs, name); remote != "" {
		return BranchTarget{Branch: name, Remote: remote}, nil
	}
	if !slices.Contains(refs, "refs/remotes/"+name) {
		return target, nil
	}
	remote, branch, ok := splitRemoteName(remotes, name)
	if !ok || branch == "HEAD" || strings.HasPrefix(branch, "-") {
		return target, nil
	}
	if !slices.Contains(refs, "refs/heads/"+branch) {
		return BranchTarget{Branch: branch, Remote: remote}, nil
	}
	if upstream := branchUpstream(dir, branch); upstream != "refs/remotes/"+name {
		return BranchTarget{}, fmt.Errorf("%w: local branch %q does not track %s; pick it from the local branches or rename it first", ErrBranchConflict, branch, name)
	}
	return BranchTarget{Branch: branch}, nil
}

// remoteHolding names the remote a branch that exists only on remotes is
// taken from: origin when it has the branch, else the first remote by name
// that does; empty when none has it.
func remoteHolding(remotes, refs []string, branch string) string {
	have := map[string]bool{}
	for _, ref := range refs {
		have[ref] = true
	}
	holds := func(remote string) bool {
		if !have["refs/remotes/"+remote+"/"+branch] {
			return false
		}
		// With nested remote names (team, team/core) the ref must belong to
		// this remote, not be another remote's branch read the wrong way.
		r, b, ok := splitRemoteName(remotes, remote+"/"+branch)
		return ok && r == remote && b == branch
	}
	if slices.Contains(remotes, "origin") && holds("origin") {
		return "origin"
	}
	for _, remote := range remotes {
		if holds(remote) {
			return remote
		}
	}
	return ""
}

// CheckoutTracking switches the working copy at dir to target.Branch,
// creating it at the remote-tracking branch it names and tracking it. A
// target without a remote is an ordinary Checkout.
func CheckoutTracking(dir string, target BranchTarget) error {
	if target.Remote == "" {
		return Checkout(dir, target.Branch)
	}
	if strings.TrimSpace(target.Branch) == "" || strings.HasPrefix(target.Branch, "-") {
		return fmt.Errorf("invalid branch name %q", target.Branch)
	}
	if !GitAvailable() {
		return builtinCheckoutTracking(dir, target)
	}
	_, err := runGit(dir, "checkout", "--track", "-b", target.Branch, target.upstreamRef(), "--")
	return err
}

// RemoteBranches lists the branches that exist on a configured remote and
// not locally, by their branch names as a local branch would carry them
// (feature/x, not origin/feature/x), each once however many remotes have it,
// sorted. A branch that exists locally is offered under its local name
// already; the remotes' HEAD pointers and refs of remotes no longer
// configured are left out. ResolveBranch turns such a name into the local
// branch tracking it. It reads the refs the last fetch left: FetchRemotes
// refreshes them.
func RemoteBranches(dir string) []string {
	return remoteBranchesFrom(listRemotes(dir), listRefs(dir))
}

// remoteBranchesFrom is RemoteBranches over full ref names.
func remoteBranchesFrom(remotes, refs []string) []string {
	taken := map[string]bool{}
	for _, ref := range refs {
		if name, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			taken[name] = true
		}
	}
	out := []string{}
	for _, ref := range refs {
		name, ok := strings.CutPrefix(ref, "refs/remotes/")
		if !ok {
			continue
		}
		_, branch, ok := splitRemoteName(remotes, name)
		if !ok || branch == "HEAD" || taken[branch] {
			continue
		}
		taken[branch] = true
		out = append(out, branch)
	}
	sort.Strings(out)
	return out
}

// splitRemoteName splits <remote>/<branch> at the longest configured remote
// name it starts with: remote names may hold slashes themselves.
func splitRemoteName(remotes []string, name string) (remote, branch string, ok bool) {
	for _, r := range remotes {
		rest, found := strings.CutPrefix(name, r+"/")
		if !found || rest == "" || len(r) <= len(remote) {
			continue
		}
		remote, branch, ok = r, rest, true
	}
	return remote, branch, ok
}

// listRemotes names the configured remotes, sorted.
func listRemotes(dir string) []string {
	if !GitAvailable() {
		return builtinListRemotes(dir)
	}
	out, err := runGit(dir, "remote")
	if err != nil || out == "" {
		return nil
	}
	names := strings.Split(out, "\n")
	sort.Strings(names)
	return names
}

// listRefs returns the full names of the local and remote-tracking branches.
func listRefs(dir string) []string {
	if !GitAvailable() {
		return builtinListRefs(dir)
	}
	out, err := runGit(dir, "for-each-ref", "--format=%(refname)", "refs/heads", "refs/remotes")
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// branchUpstream returns the remote-tracking ref a local branch tracks, empty
// when it tracks none.
func branchUpstream(dir, branch string) string {
	if !GitAvailable() {
		return builtinBranchUpstream(dir, branch)
	}
	out, _ := runGit(dir, "for-each-ref", "--format=%(upstream)", "refs/heads/"+branch)
	return out
}

// FetchRemotes refreshes the remote-tracking branches of every configured
// remote, the way `git fetch --all --prune` does, and reports which remotes
// it asked. It never touches the working tree, the index, HEAD, local
// branches or tags: tags are not fetched, and a remote whose fetch refspec
// writes anywhere but refs/remotes/ is left out and reported, since --prune
// through it could delete local branches. Remotes marked skipFetchAll are
// left out silently, as `git fetch --all` does. A repository without remotes
// yields no remotes and no error.
//
// One remote failing does not stop the others; the error names every remote
// that was not refreshed, with credentials in URLs masked. ctx bounds the
// whole fetch, and git with every process it started (ssh, a remote helper)
// is stopped when it ends. Concurrent calls for one repository share a single
// fetch: two git fetches racing for the same ref locks would fail each other.
func FetchRemotes(ctx context.Context, dir string) ([]string, error) {
	key := fetchKey(dir)
	for {
		fetchMu.Lock()
		call, running := fetchCalls[key]
		if !running {
			call = &fetchCall{done: make(chan struct{})}
			fetchCalls[key] = call
			fetchMu.Unlock()
			call.remotes, call.err = runFetch(ctx, dir)
			fetchMu.Lock()
			delete(fetchCalls, key)
			fetchMu.Unlock()
			close(call.done)
			return call.remotes, call.err
		}
		call.waiters++
		fetchMu.Unlock()
		select {
		case <-call.done:
		case <-ctx.Done():
			return nil, fmt.Errorf("fetch remotes: %w", ctx.Err())
		}
		// The fetch joined was given up by its own caller, not by the remotes:
		// this caller still wants an answer, so it fetches again itself.
		if errors.Is(call.err, context.Canceled) && ctx.Err() == nil {
			continue
		}
		return call.remotes, call.err
	}
}

type fetchCall struct {
	done    chan struct{}
	waiters int // callers that joined it, guarded by fetchMu
	remotes []string
	err     error
}

var (
	fetchMu    sync.Mutex
	fetchCalls = map[string]*fetchCall{}
	// runFetch is the fetch a leading caller runs; tests replace it.
	runFetch = fetchRemotes
)

// fetchKey names the repository dir belongs to by its common git dir, so
// every worktree of one repository shares a fetch.
func fetchKey(dir string) string {
	key := dir
	if _, common, ok := gitDirs(dir); ok {
		key = common
	}
	if resolved, err := filepath.EvalSymlinks(key); err == nil {
		key = resolved
	}
	return filepath.Clean(key)
}

// remoteSettings is what FetchRemotes needs to know about one remote.
type remoteSettings struct {
	skipFetchAll bool
	refspecs     []string
}

// fetchPlan sorts the remotes into the ones to fetch and the problems that
// keep the others out.
func fetchPlan(remotes []string, settings map[string]remoteSettings) (fetch []string, problems []string) {
	for _, name := range remotes {
		s := settings[name]
		if s.skipFetchAll {
			continue
		}
		if dst := unsafeRefspecDestination(s.refspecs); dst != "" {
			problems = append(problems, fmt.Sprintf("remote %s: not refreshed, its fetch refspec writes to %s outside refs/remotes/", name, dst))
			continue
		}
		fetch = append(fetch, name)
	}
	return fetch, problems
}

// unsafeRefspecDestination returns the first destination of a fetch refspec
// that is not a remote-tracking ref, empty when every one of them is. A
// refspec without a destination (or a negative one) writes no ref at all.
func unsafeRefspecDestination(refspecs []string) string {
	for _, spec := range refspecs {
		spec = strings.TrimSpace(spec)
		if spec == "" || strings.HasPrefix(spec, "^") {
			continue
		}
		_, dst, found := strings.Cut(strings.TrimPrefix(spec, "+"), ":")
		if !found || dst == "" {
			continue
		}
		if !strings.HasPrefix(dst, "refs/remotes/") {
			return dst
		}
	}
	return ""
}

func fetchRemotes(ctx context.Context, dir string) ([]string, error) {
	if !GitAvailable() {
		return builtinFetchRemotes(ctx, dir)
	}
	remotes, problems := fetchPlan(listRemotes(dir), gitRemoteSettings(dir))
	// One fetch per remote, in turn, as `git fetch --all` runs them itself:
	// a failure is then git's own words about one named remote, not the
	// progress lines --multiple interleaves with them.
	for _, name := range remotes {
		err := runGitContext(ctx, dir,
			// The same guard Clone has: a remote URL of the ext:: transport
			// runs an arbitrary command.
			"-c", "protocol.ext.allow=never",
			"fetch", "--prune", "--no-prune-tags", "--no-tags", "--recurse-submodules=no",
			"--", name)
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return remotes, fmt.Errorf("fetch remotes: %w", ctx.Err())
		}
		problems = append(problems, fmt.Sprintf("remote %s: %v", name, err))
	}
	if len(problems) > 0 {
		return remotes, errors.New(redactURLCredentials(strings.Join(problems, "; ")))
	}
	return remotes, nil
}

// gitRemoteSettings reads remote.<name>.fetch and remote.<name>.skipFetchAll
// of every remote in one call.
func gitRemoteSettings(dir string) map[string]remoteSettings {
	settings := map[string]remoteSettings{}
	out, err := runGit(dir, "config", "--null", "--get-regexp", `^remote\.`)
	if err != nil {
		return settings
	}
	for entry := range strings.SplitSeq(out, "\x00") {
		key, value, _ := strings.Cut(entry, "\n")
		rest, ok := strings.CutPrefix(key, "remote.")
		if !ok {
			continue
		}
		dot := strings.LastIndex(rest, ".")
		if dot <= 0 {
			continue
		}
		name, option := rest[:dot], rest[dot+1:]
		s := settings[name]
		switch strings.ToLower(option) {
		case "fetch":
			s.refspecs = append(s.refspecs, value)
		case "skipfetchall":
			s.skipFetchAll = gitBool(value)
		}
		settings[name] = s
	}
	return settings
}

// gitBool reads a git config boolean; a key written without a value is true.
func gitBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "true", "yes", "on", "1":
		return true
	}
	return false
}

// runGitContext runs git under ctx for an operation that talks to the
// network, and fails with what git printed. It never asks for anything: no
// controlling terminal, so ssh cannot stop for a passphrase or a host key on
// the operator's terminal, and no askpass program. It runs in a process
// group of its own, so the end of ctx stops ssh and the remote helper along
// with git instead of leaving them behind.
func runGitContext(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_TERMINAL_PROMPT=0",
		// An empty GIT_ASKPASS ends git's askpass chain before core.askPass
		// and SSH_ASKPASS: a desktop's or an editor's askpass would otherwise
		// put a password dialog on the server's screen. Credential helpers
		// still answer.
		"GIT_ASKPASS=",
		// ssh's own askpass (OpenSSH 8.4+), for a passphrase or a host key.
		"SSH_ASKPASS_REQUIRE=never",
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	platform.DetachFromTerminal(cmd)
	cmd.Cancel = func() error { return platform.TerminateProcessGroup(cmd, time.Second) }
	cmd.WaitDelay = 2 * time.Second
	platform.AdaptCommand(cmd)
	if err := cmd.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("git %s: %w", strings.Join(args, " "), ctxErr)
		}
		// git's own words are the reason; the exit status adds nothing.
		if msg := strings.TrimSpace(out.String()); msg != "" {
			return errors.New(msg)
		}
		return err
	}
	return nil
}

// urlCredentials matches the user-info part of a URL (scheme://user:pass@).
var urlCredentials = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/@\s'"]+@`)

// redactURLCredentials masks the user-info of every URL in a git message: a
// remote URL may carry a token, and the message is shown to whoever opened
// the branch list.
func redactURLCredentials(msg string) string {
	return urlCredentials.ReplaceAllString(msg, "${1}***@")
}
