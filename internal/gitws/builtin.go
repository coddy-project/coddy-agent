package gitws

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/protocol/packp"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/client"
	"github.com/go-git/go-git/v5/plumbing/transport/server"
)

// This file is the built-in backend used when the git binary is not on PATH:
// the same answers, computed with go-git instead of child processes.

// openRepo opens the repository holding dir, following a `.git` file into a
// linked worktree and the commondir behind it the way git itself does.
func openRepo(dir string) (*git.Repository, error) {
	return git.PlainOpenWithOptions(dir, &git.PlainOpenOptions{
		DetectDotGit:          true,
		EnableDotGitCommonDir: true,
	})
}

// gitDirs resolves the git dir and the common dir governing dir without the
// git binary: it walks up to the first `.git`, reads a `.git` file's `gitdir:`
// pointer when it is one, and follows `<git dir>/commondir` to the storage
// shared by every linked worktree.
func gitDirs(dir string) (gitDir, commonDir string, ok bool) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", "", false
	}
	for d := abs; ; {
		entry := filepath.Join(d, ".git")
		// Stat, not Lstat: a .git that is a link to a directory is one, as
		// git reads it.
		fi, err := os.Stat(entry)
		switch {
		case err != nil:
			// Nothing here; keep walking up.
		case fi.IsDir():
			gitDir = entry
		default:
			body, err := os.ReadFile(entry)
			if err != nil {
				return "", "", false
			}
			target, found := strings.CutPrefix(strings.TrimSpace(string(body)), "gitdir:")
			if !found {
				return "", "", false
			}
			gitDir = strings.TrimSpace(target)
			if !filepath.IsAbs(gitDir) {
				gitDir = filepath.Join(d, gitDir)
			}
		}
		if gitDir != "" {
			break
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "", "", false
		}
		d = parent
	}
	commonDir = gitDir
	if body, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		commonDir = strings.TrimSpace(string(body))
		if !filepath.IsAbs(commonDir) {
			commonDir = filepath.Join(gitDir, commonDir)
		}
	}
	return gitDir, commonDir, true
}

// headRefBranch reads a git dir's HEAD file and reports the branch it names,
// empty when HEAD is detached or unreadable.
func headRefBranch(gitDir string) string {
	body, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil {
		return ""
	}
	target, found := strings.CutPrefix(strings.TrimSpace(string(body)), "ref:")
	if !found {
		return ""
	}
	ref := strings.TrimSpace(target)
	if !strings.HasPrefix(ref, "refs/heads/") {
		return ""
	}
	return strings.TrimPrefix(ref, "refs/heads/")
}

// builtinWorktrees lists the checkouts of a repository by hand: the main one
// is the parent of a common dir named .git, the linked ones are the folders
// under <common>/worktrees, ordered by name the way ReadDir already returns
// them.
func builtinWorktrees(commonDir string) []Worktree {
	var list []Worktree
	if filepath.Base(commonDir) == ".git" {
		list = append(list, Worktree{
			Path:   filepath.Dir(commonDir),
			Branch: headRefBranch(commonDir),
			Main:   true,
		})
	}
	admin := filepath.Join(commonDir, "worktrees")
	entries, err := os.ReadDir(admin)
	if err != nil {
		return list
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		wtDir := filepath.Join(admin, e.Name())
		// gitdir names the .git file inside the checkout, so its parent is
		// the worktree's own root. Unreadable entries are skipped: a pruned
		// or half-written worktree must not blank the whole listing.
		body, err := os.ReadFile(filepath.Join(wtDir, "gitdir"))
		if err != nil {
			continue
		}
		dotGit := strings.TrimSpace(string(body))
		if !filepath.IsAbs(dotGit) {
			dotGit = filepath.Join(wtDir, dotGit)
		}
		list = append(list, Worktree{
			Path:   filepath.Dir(dotGit),
			Branch: headRefBranch(wtDir),
		})
	}
	return list
}

// builtinBaseBranch reads origin/HEAD without contacting the remote, like the
// git path's `symbolic-ref` call.
func builtinBaseBranch(repo *git.Repository) string {
	ref, err := repo.Reference(plumbing.NewRemoteHEADReferenceName("origin"), true)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(ref.Name().String(), "refs/remotes/origin/")
}

// builtinDescribe is Describe without the git binary: the worktree root comes
// from go-git, the worktree list from the filesystem layout gitDirs found.
func builtinDescribe(info Info) Info {
	repo, err := openRepo(info.Path)
	if err != nil {
		return info
	}
	// A bare repository has no working copy, and `git rev-parse
	// --show-toplevel` refuses it: neither is it a workspace here.
	wt, err := repo.Worktree()
	if err != nil || wt == nil {
		return info
	}
	info.IsGitRepo = true
	toplevel := wt.Filesystem.Root()
	if head, err := repo.Head(); err == nil && head.Name().IsBranch() {
		info.Branch = head.Name().Short()
	}
	if iter, err := repo.Branches(); err == nil {
		_ = iter.ForEach(func(ref *plumbing.Reference) error {
			info.Branches = append(info.Branches, ref.Name().Short())
			return nil
		})
		sort.Strings(info.Branches)
	}
	if _, common, ok := gitDirs(info.Path); ok {
		info.Worktrees = builtinWorktrees(common)
	}
	info.BaseBranch = builtinBaseBranch(repo)
	if len(info.Worktrees) > 0 {
		info.RepoRoot = info.Worktrees[0].Path
		info.IsWorktree = !samePath(toplevel, info.RepoRoot)
	} else {
		info.RepoRoot = toplevel
	}
	return info
}

// builtinMainCheckoutRoot identifies the main checkout through the common dir
// the way `git rev-parse --git-common-dir` does.
func builtinMainCheckoutRoot(dir string) string {
	_, common, ok := gitDirs(dir)
	if !ok || filepath.Base(common) != ".git" {
		return ""
	}
	return filepath.Dir(common)
}

// builtinCheckout switches branches in place. Unlike `git checkout` it refuses
// whenever tracked files carry uncommitted changes, so nothing a checkout would
// discard is ever at risk; untracked files do not count.
func builtinCheckout(dir, branch string) error {
	repo, err := openRepo(dir)
	if err != nil {
		return err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	if err := refuseUncommitted(wt); err != nil {
		return err
	}
	return wt.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName(branch),
	})
}

// fileTransportOnce installs go-git's in-process server for the file
// protocol. The stock file transport runs git-upload-pack, which is the very
// binary this backend replaces, so local paths and file:// URLs need the
// in-process one.
var fileTransportOnce sync.Once

func installFileTransport() {
	fileTransportOnce.Do(func() {
		client.InstallProtocol("file", knownHavesTransport{server.NewClient(fileLoader{})})
	})
}

// knownHavesTransport fixes one gap of go-git's in-process server: it walks
// every commit the client says it has, and a commit only the client has - a
// local commit not pushed yet - ends the fetch with "object not found".
// git-upload-pack ignores such a have; this wrapper drops it before the
// server sees it.
type knownHavesTransport struct {
	transport.Transport
}

func (t knownHavesTransport) NewUploadPackSession(ep *transport.Endpoint, auth transport.AuthMethod) (transport.UploadPackSession, error) {
	session, err := t.Transport.NewUploadPackSession(ep, auth)
	if err != nil {
		return nil, err
	}
	sto, err := fileLoader{}.Load(ep)
	if err != nil {
		return nil, err
	}
	return knownHavesSession{UploadPackSession: session, storer: sto}, nil
}

type knownHavesSession struct {
	transport.UploadPackSession
	storer storer.Storer
}

func (s knownHavesSession) UploadPack(ctx context.Context, req *packp.UploadPackRequest) (*packp.UploadPackResponse, error) {
	known := req.Haves[:0:0]
	for _, h := range req.Haves {
		if s.storer.HasEncodedObject(h) == nil {
			known = append(known, h)
		}
	}
	req.Haves = known
	return s.UploadPackSession.UploadPack(ctx, req)
}

// fileLoader serves repositories to the in-process file transport. The
// package's own filesystem loader hands the worktree root to a storage that
// expects the .git dir itself, so a non-bare checkout advertises no refs
// through it; opening the repository directly serves both kinds.
type fileLoader struct{}

func (fileLoader) Load(ep *transport.Endpoint) (storer.Storer, error) {
	repo, err := git.PlainOpen(ep.Path)
	if err != nil {
		return nil, transport.ErrRepositoryNotFound
	}
	return repo.Storer, nil
}

// builtinClone is Clone without the git binary: a shallow clone, with the ref
// tried as a branch first and as a tag when no such branch exists.
//
// Like git, it refuses a destination that already holds something, and a
// failed attempt only clears what it wrote itself: an empty folder that was
// there before stays, and nothing else is ever removed.
func builtinClone(url, ref, dest string) error {
	preexisting := false
	if entries, err := os.ReadDir(dest); err == nil {
		if len(entries) > 0 {
			return fmt.Errorf("destination path %s already exists and is not an empty directory", dest)
		}
		preexisting = true
	} else if _, statErr := os.Lstat(dest); statErr == nil {
		return fmt.Errorf("destination path %s already exists and is not a directory", dest)
	}
	cleanup := func() {
		if !preexisting {
			_ = os.RemoveAll(dest)
			return
		}
		entries, _ := os.ReadDir(dest)
		for _, e := range entries {
			_ = os.RemoveAll(filepath.Join(dest, e.Name()))
		}
	}
	installFileTransport()
	opts := git.CloneOptions{URL: url, Depth: 1}
	// The in-process file transport does not speak the shallow capability, so
	// a local path or file:// URL is cloned in full.
	if ep, err := transport.NewEndpoint(url); err == nil && ep.Protocol == "file" {
		opts.Depth = 0
	}
	if ref == "" {
		_, err := git.PlainClone(dest, false, &opts)
		if err != nil {
			cleanup()
			return err
		}
		return nil
	}
	var lastErr error
	for _, name := range []plumbing.ReferenceName{
		plumbing.NewBranchReferenceName(ref),
		plumbing.NewTagReferenceName(ref),
	} {
		attempt := opts
		attempt.ReferenceName = name
		attempt.SingleBranch = true
		if _, err := git.PlainClone(dest, false, &attempt); err != nil {
			// A failed clone leaves a partial directory behind; the next
			// attempt needs it gone, and so does the caller on a failure.
			cleanup()
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

// builtinPull fast-forwards through go-git; already-up-to-date is success.
func builtinPull(dir string) error {
	installFileTransport()
	repo, err := openRepo(dir)
	if err != nil {
		return err
	}
	wt, err := repo.Worktree()
	if err != nil {
		return err
	}
	if err := wt.Pull(&git.PullOptions{RemoteName: "origin"}); err != nil &&
		!errors.Is(err, git.NoErrAlreadyUpToDate) {
		return err
	}
	return nil
}
