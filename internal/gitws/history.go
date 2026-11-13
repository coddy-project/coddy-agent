package gitws

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// CreateAndSwitch creates a local branch at this checkout's HEAD. It never
// resets an existing branch or uses the worktree preference's base branch.
func CreateAndSwitch(dir, name string) error {
	if name == "" || name != strings.TrimSpace(name) || name == "HEAD" || strings.HasPrefix(name, "-") {
		return fmt.Errorf("invalid branch name: %q", name)
	}
	ref := plumbing.NewBranchReferenceName(name)
	if err := ref.Validate(); err != nil {
		return fmt.Errorf("invalid branch name: %q", name)
	}
	if !GitAvailable() {
		repo, err := openRepo(dir)
		if err != nil {
			return err
		}
		if _, err := repo.Reference(ref, false); !errors.Is(err, plumbing.ErrReferenceNotFound) {
			if err != nil {
				return err
			}
			return fmt.Errorf("branch already exists: %s", name)
		}
		wt, err := repo.Worktree()
		if err != nil {
			return err
		}
		if err := refuseUncommitted(wt); err != nil {
			return err
		}
		return wt.Checkout(&git.CheckoutOptions{Branch: ref, Create: true})
	}
	checked, err := runGit(dir, "check-ref-format", "--branch", name)
	if err != nil || checked != name {
		return fmt.Errorf("invalid branch name: %q", name)
	}
	_, err = runGit(dir, "switch", "--no-guess", "-c", name, "HEAD")
	return err
}

// Commit carries the topology and labels needed to draw a history row.
type Commit struct {
	Hash    string    `json:"hash"`
	Parents []string  `json:"parents"`
	Subject string    `json:"subject"`
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
	Refs    []string  `json:"refs"`
}

type HistoryPage struct {
	Commits []Commit `json:"commits"`
	HasMore bool     `json:"has_more"`
}

// History reads a bounded topological page from all branches and HEAD, including
// detached HEAD. It never fetches or changes the working copy.
func History(dir string, offset, limit int) (HistoryPage, error) {
	page := HistoryPage{Commits: []Commit{}}
	if !IsRepo(dir) {
		return page, ErrNotRepository
	}
	if !GitAvailable() {
		return page, ErrNeedsGitBinary
	}
	if offset < 0 || offset > 100000 || limit < 1 || limit > 500 {
		return page, fmt.Errorf("invalid history page")
	}
	// An unborn repository has no commits, but is still a valid graph.
	head, _ := runGit(dir, "rev-parse", "--verify", "HEAD")
	args := []string{"log", "--all", "--topo-order", "--date-order", "--no-show-signature", "--format=%H%x00%P%x00%s%x00%an%x00%aI", "--skip=" + strconv.Itoa(offset), "-n", strconv.Itoa(limit + 1)}
	if head != "" {
		args = append(args, "HEAD")
	}
	out, err := runGit(dir, args...)
	if err != nil {
		return page, err
	}
	labels := map[string][]string{}
	refs, err := runGit(dir, "for-each-ref", "--format=%(objectname)%00%(*objectname)%00%(refname)", "refs/heads", "refs/remotes", "refs/tags")
	if err != nil {
		return page, err
	}
	for _, line := range strings.Split(refs, "\n") {
		parts := strings.SplitN(line, "\x00", 3)
		if len(parts) != 3 {
			continue
		}
		hash, ref := parts[0], parts[2]
		label := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(ref, "refs/heads/"), "refs/remotes/"), "refs/tags/")
		if strings.HasPrefix(ref, "refs/tags/") {
			label = "tag: " + label
			if parts[1] != "" {
				hash = parts[1]
			}
		}
		labels[hash] = append(labels[hash], label)
	}
	if head != "" {
		labels[head] = append([]string{"HEAD"}, labels[head]...)
	}
	if out == "" {
		return page, nil
	}
	for _, line := range strings.Split(out, "\n") {
		parts := strings.Split(line, "\x00")
		if len(parts) != 5 {
			return page, fmt.Errorf("invalid git history record")
		}
		date, e := time.Parse(time.RFC3339, parts[4])
		if e != nil {
			return page, e
		}
		rowRefs := labels[parts[0]]
		if rowRefs == nil {
			rowRefs = []string{}
		}
		page.Commits = append(page.Commits, Commit{Hash: parts[0], Parents: strings.Fields(parts[1]), Subject: parts[2], Author: parts[3], Date: date, Refs: rowRefs})
	}
	if len(page.Commits) > limit {
		page.HasMore = true
		page.Commits = page.Commits[:limit]
	}
	return page, nil
}
