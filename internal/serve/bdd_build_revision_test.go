package serve

// Godog harness for features/build_revision.feature: a real checkout with a
// worktree nested in a folder of it, a program the Go toolchain builds in that
// worktree, and the repository's own Makefile asked which revision it passes
// to a build there (`make -s print-revision`). The program's build info, read
// back from the file, is what buildOf describes.

import (
	"context"
	"debug/buildinfo"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

type buildRevisionState struct {
	t *testing.T

	outer, worktree         string
	outerHead, worktreeHead string

	stamped *debug.BuildInfo
	named   string
}

// fixtureEnv is the environment of the fixture's git, go and make. A test run
// from a git hook inherits GIT_DIR and GIT_INDEX_FILE, which would point every
// git command at the repository being committed, and a test run by `make test`
// inherits the parent make's flags and command-line variables through
// MAKEFLAGS, which would override the Makefile's own REVISION.
func fixtureEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(strings.ToUpper(name), "GIT_"):
		case name == "MAKEFLAGS", name == "MFLAGS", name == "MAKEOVERRIDES", name == "MAKELEVEL":
		default:
			env = append(env, kv)
		}
	}
	env = append(env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull)
	return append(env, extra...)
}

func (s *buildRevisionState) run(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = fixtureEnv("GOFLAGS=-buildvcs=true", "GOTOOLCHAIN=local", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

func (s *buildRevisionState) commit(dir, message string) (string, error) {
	if _, err := s.run(dir, "git", "-c", "user.email=coddy@test", "-c", "user.name=coddy",
		"commit", "--allow-empty", "-q", "-m", message); err != nil {
		return "", err
	}
	return s.run(dir, "git", "rev-parse", "HEAD")
}

func (s *buildRevisionState) aCheckoutWithANestedWorktree() error {
	s.outer = filepath.Join(s.t.TempDir(), "project")
	if _, err := s.run(filepath.Dir(s.outer), "git", "init", "-q", "-b", "main", s.outer); err != nil {
		return err
	}
	head, err := s.commit(s.outer, "the enclosing checkout")
	if err != nil {
		return err
	}
	s.outerHead = head
	// Where Coddy puts the worktrees of a checkout: a folder inside it.
	s.worktree = filepath.Join(s.outer, ".coddy", "worktrees", "feature")
	if _, err := s.run(s.outer, "git", "worktree", "add", "-q", "-b", "feature", s.worktree); err != nil {
		return err
	}
	for name, body := range map[string]string{
		"go.mod":  "module example.com/stamp\n\ngo 1.22\n",
		"main.go": "package main\n\nfunc main() {}\n",
	} {
		if err := os.WriteFile(filepath.Join(s.worktree, name), []byte(body), 0o644); err != nil {
			return err
		}
	}
	if _, err := s.run(s.worktree, "git", "add", "."); err != nil {
		return err
	}
	head, err = s.commit(s.worktree, "the worktree's own commit")
	if err != nil {
		return err
	}
	s.worktreeHead = head
	if s.worktreeHead == s.outerHead {
		return fmt.Errorf("the worktree and the enclosing checkout are both on %s", head)
	}
	return nil
}

func (s *buildRevisionState) aProgramBuiltInTheWorktree() error {
	program := filepath.Join(s.t.TempDir(), "stamp")
	if _, err := s.run(s.worktree, "go", "build", "-o", program, "."); err != nil {
		return err
	}
	info, err := buildinfo.ReadFile(program)
	if err != nil {
		return err
	}
	var stamped string
	for _, setting := range info.Settings {
		if setting.Key == "vcs.revision" {
			stamped = setting.Value
		}
	}
	if stamped != s.outerHead {
		return fmt.Errorf("the toolchain stamped vcs.revision %q, want the enclosing checkout's %s (worktree %s)",
			stamped, s.outerHead, s.worktreeHead)
	}
	s.stamped = info
	return nil
}

func (s *buildRevisionState) theMakefileNamesTheRevision() error {
	makefile, err := filepath.Abs(filepath.Join("..", "..", "Makefile"))
	if err != nil {
		return err
	}
	named, err := s.run(s.worktree, "make", "-s", "--no-print-directory", "-f", makefile, "print-revision")
	if err != nil {
		return err
	}
	s.named = named
	return nil
}

func (s *buildRevisionState) itNamesTheWorktreesCommit() error {
	if s.named != s.worktreeHead {
		return fmt.Errorf("the Makefile names %q, want the worktree's %s (the enclosing checkout is on %s)",
			s.named, s.worktreeHead, s.outerHead)
	}
	return nil
}

func (s *buildRevisionState) theProgramDescribesItselfByTheWorktreesCommit() error {
	prev := version.Revision
	version.Revision = s.named
	defer func() { version.Revision = prev }()
	if got := buildOf(s.stamped).Revision; got != s.worktreeHead {
		return fmt.Errorf("the build describes itself by %q, want the worktree's %s (stamped with %s)",
			got, s.worktreeHead, s.outerHead)
	}
	return nil
}

func TestBuildRevisionFeature(t *testing.T) {
	for _, tool := range []string{"git", "go", "make"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH: the scenario builds a real checkout and runs the Makefile", tool)
		}
	}
	suite := godog.TestSuite{
		Name: "build-revision",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &buildRevisionState{t: t}
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				*s = buildRevisionState{t: t}
				return ctx, nil
			})
			sc.Step(`^a checkout with a worktree nested in a folder of it, each on a commit of its own$`, s.aCheckoutWithANestedWorktree)
			sc.Step(`^a program built in the worktree, which the Go toolchain stamps with the enclosing checkout's commit$`, s.aProgramBuiltInTheWorktree)
			sc.Step(`^the Makefile of the repository names the revision for a build in the worktree$`, s.theMakefileNamesTheRevision)
			sc.Step(`^it names the worktree's commit$`, s.itNamesTheWorktreesCommit)
			sc.Step(`^the program, given that revision, describes itself by the worktree's commit$`, s.theProgramDescribesItselfByTheWorktreesCommit)
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/build_revision.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("build revision feature suite failed")
	}
}
