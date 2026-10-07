package gitws

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	git "github.com/go-git/go-git/v5"
	gitconfig "github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
)

// This file is the built-in backend of remotes.go: the remote branches, their
// refresh and a checkout tracking one of them, through go-git.

// builtinListRemotes names the configured remotes, sorted.
func builtinListRemotes(dir string) []string {
	repo, err := openRepo(dir)
	if err != nil {
		return nil
	}
	cfg, err := repo.Config()
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(cfg.Remotes))
	for name := range cfg.Remotes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// builtinListRefs returns the full names of the local and remote-tracking
// branches.
func builtinListRefs(dir string) []string {
	repo, err := openRepo(dir)
	if err != nil {
		return nil
	}
	iter, err := repo.References()
	if err != nil {
		return nil
	}
	var refs []string
	_ = iter.ForEach(func(ref *plumbing.Reference) error {
		if name := ref.Name(); name.IsBranch() || name.IsRemote() {
			refs = append(refs, name.String())
		}
		return nil
	})
	sort.Strings(refs)
	return refs
}

// builtinBranchUpstream reads branch.<name>.remote and .merge the way git
// maps them through the default refspec: refs/remotes/<remote>/<branch>.
func builtinBranchUpstream(dir, branch string) string {
	repo, err := openRepo(dir)
	if err != nil {
		return ""
	}
	cfg, err := repo.Config()
	if err != nil {
		return ""
	}
	b, ok := cfg.Branches[branch]
	if !ok || b.Remote == "" || b.Remote == "." || !b.Merge.IsBranch() {
		return ""
	}
	return "refs/remotes/" + b.Remote + "/" + b.Merge.Short()
}

// refuseUncommitted refuses a checkout over tracked files that carry
// uncommitted changes, so nothing a checkout would discard is ever at risk;
// untracked files do not count.
func refuseUncommitted(wt *git.Worktree) error {
	status, err := wt.Status()
	if err != nil {
		return err
	}
	for _, fs := range status {
		if (fs.Staging != git.Unmodified && fs.Staging != git.Untracked) ||
			(fs.Worktree != git.Unmodified && fs.Worktree != git.Untracked) {
			return fmt.Errorf("refusing checkout: the working copy has uncommitted changes")
		}
	}
	return nil
}

// builtinCheckoutTracking creates target.Branch at the remote-tracking branch
// and records it as the branch's upstream, as `git checkout --track -b` does.
func builtinCheckoutTracking(dir string, target BranchTarget) error {
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
	start, err := repo.Reference(plumbing.ReferenceName(target.upstreamRef()), true)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", target.upstreamRef(), err)
	}
	if err := wt.Checkout(&git.CheckoutOptions{
		Hash:   start.Hash(),
		Branch: plumbing.NewBranchReferenceName(target.Branch),
		Create: true,
	}); err != nil {
		return err
	}
	cfg, err := repo.Config()
	if err != nil {
		return err
	}
	cfg.Branches[target.Branch] = &gitconfig.Branch{
		Name:   target.Branch,
		Remote: target.Remote,
		Merge:  plumbing.NewBranchReferenceName(target.Branch),
	}
	return repo.SetConfig(cfg)
}

// builtinFetchRemotes is fetchRemotes through go-git: each remote in turn,
// pruned, without tags, under the same plan the git path follows.
func builtinFetchRemotes(ctx context.Context, dir string) ([]string, error) {
	installFileTransport()
	repo, err := openRepo(dir)
	if err != nil {
		return nil, err
	}
	cfg, err := repo.Config()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(cfg.Remotes))
	settings := map[string]remoteSettings{}
	for name, rc := range cfg.Remotes {
		names = append(names, name)
		var s remoteSettings
		for _, spec := range rc.Fetch {
			s.refspecs = append(s.refspecs, spec.String())
		}
		if sub := cfg.Raw.Section("remote").Subsection(name); sub.HasOption("skipFetchAll") {
			s.skipFetchAll = gitBool(sub.Option("skipFetchAll"))
		}
		settings[name] = s
	}
	sort.Strings(names)
	remotes, problems := fetchPlan(names, settings)
	for _, name := range remotes {
		err := repo.FetchContext(ctx, &git.FetchOptions{
			RemoteName: name,
			Prune:      true,
			Tags:       git.NoTags,
		})
		if err == nil || errors.Is(err, git.NoErrAlreadyUpToDate) {
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
