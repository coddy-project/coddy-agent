package tools_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
	apptools "github.com/EvilFreelancer/coddy-agent/internal/tools"
)

func openWorktreeArgs(s *worktreeNavigationState, args string) error {
	_, err := apptools.NewRegistry().Execute(context.Background(), apptools.ToolWorktreeOpen, args, s.env)
	return err
}

func TestWorktreeOpenRefusesWhatIsNotAnotherWorktree(t *testing.T) {
	s := openedNavigation(t, "feature/here")
	wt := s.worktrees["feature/here"]
	for _, args := range []string{
		fmt.Sprintf(`{"path":%q}`, s.root),     // the main checkout is worktree_exit's
		`{"branch":"main"}`,                    // the same, by its branch
		`{"path":"/no/such/worktree"}`,         // no worktree there
		`{}`,                                   // neither
		`{"path":"x","branch":"feature/here"}`, // both
	} {
		if err := openWorktreeArgs(s, args); err == nil {
			t.Fatalf("worktree_open %s succeeded", args)
		}
		stays(t, s, wt)
	}
}

// A worktree on a detached HEAD has no branch to open it by; its folder,
// relative to the main checkout, is enough.
func TestWorktreeOpenEntersADetachedWorktreeByItsFolder(t *testing.T) {
	s := openedNavigation(t, "feature/here")
	if _, err := s.git(s.root, "worktree", "add", "-q", "--detach", filepath.Join(s.root, "review")); err != nil {
		t.Fatal(err)
	}
	if err := openWorktreeArgs(s, `{"path":"review"}`); err != nil {
		t.Fatalf("worktree_open review = %v", err)
	}
	if got := s.state.GetCWD(); !session.SameWorkspacePath(got, filepath.Join(s.root, "review")) {
		t.Fatalf("session works in %q, want the detached worktree", got)
	}
}

// Every live session is counted once, in the deepest worktree that holds its
// folder: the main checkout, which holds the worktrees under it, does not
// count theirs. A worktree whose folder is gone is listed as missing.
func TestWorktreeListPlacesEverySessionInItsOwnWorktree(t *testing.T) {
	s := openedNavigation(t, "feature/one")
	one := s.worktrees["feature/one"]
	if err := s.worksInWorktree("feature/two"); err != nil {
		t.Fatal(err)
	}
	two := s.worktrees["feature/two"]
	s.env.SessionsInWorkspace = func(dir string) []string {
		var out []string
		for id, cwd := range map[string]string{"sess_main": s.root, "sess_one": filepath.Join(one, "pkg")} {
			if strings.HasPrefix(cwd+string(filepath.Separator), dir+string(filepath.Separator)) || cwd == dir {
				out = append(out, id)
			}
		}
		return out
	}
	if _, err := s.git(s.root, "worktree", "add", "-q", "-b", "feature/gone", filepath.Join(s.root, "gone")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.git(s.root, "worktree", "lock", filepath.Join(s.root, "gone")); err != nil {
		t.Fatal(err)
	}
	if err := removeAll(filepath.Join(s.root, "gone")); err != nil {
		t.Fatal(err)
	}
	if err := s.lists(); err != nil {
		t.Fatal(err)
	}
	got := map[string]apptools.WorktreeListEntry{}
	for _, e := range s.listing.Worktrees {
		got[e.Branch] = e
	}
	if e := got["main"]; !e.Main || fmt.Sprint(e.Sessions) != "[sess_main]" {
		t.Fatalf("main checkout = %+v, want sess_main alone", e)
	}
	if e := got["feature/one"]; fmt.Sprint(e.Sessions) != "[sess_one]" || e.Current {
		t.Fatalf("feature/one = %+v, want sess_one and not current", e)
	}
	if e := got["feature/two"]; !e.Current || !session.SameWorkspacePath(e.Path, two) || len(e.Sessions) != 0 {
		t.Fatalf("feature/two = %+v, want the current worktree with no other session", e)
	}
	if e := got["feature/gone"]; !e.Missing || !e.Locked {
		t.Fatalf("feature/gone = %+v, want it missing and still locked", e)
	}
	// git keeps a locked worktree whose folder is gone, so prune alone does
	// not forget it: the refusal says how to.
	if _, err := apptools.OpenWorktree(context.Background(), s.env, apptools.WorktreeTarget{Path: filepath.Join(s.root, "gone")}); err == nil || !strings.Contains(err.Error(), "unlock") {
		t.Fatalf("OpenWorktree(gone, locked) = %v, want a refusal naming git worktree unlock", err)
	}
}

func removeAll(dir string) error { return os.RemoveAll(dir) }

// A worktree tool that points the model at another one names a tool the
// registry has: worktree_list once sent it to a worktree_enter that did not
// exist.
func TestWorktreeToolDescriptionsNameRegisteredTools(t *testing.T) {
	reg := apptools.NewRegistry()
	name := regexp.MustCompile(`\bworktree_[a-z_]+`)
	for _, def := range reg.AllToolDefinitions() {
		for _, ref := range name.FindAllString(def.Description, -1) {
			if _, ok := reg.Get(ref); !ok {
				t.Errorf("the description of %s names %s, which is not a registered tool", def.Name, ref)
			}
		}
	}
}
