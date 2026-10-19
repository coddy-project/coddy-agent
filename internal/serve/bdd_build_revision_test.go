package serve

// Godog harness for features/build_revision.feature: a real checkout with a
// worktree nested in a folder of it, and the repository's own Makefile running
// its `build` recipe in that worktree. The worktree holds a stand-in of the
// module - the same module path, an internal/version package with Version and
// Revision, a cmd/coddy that prints the revision it was linked with - so the
// recipe, LDFLAGS and the -X symbol names are the real ones, and the build
// info of the binary it writes is what buildOf describes.

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

// standInModule is the part of the module the Makefile's build recipe and its
// LDFLAGS name: the module path, the version package and ./cmd/coddy/.
var standInModule = map[string]string{
	"go.mod": "module github.com/EvilFreelancer/coddy-agent\n\ngo 1.22\n",
	"internal/version/version.go": "package version\n\n" +
		"var Version = \"dev\"\n\nvar Revision string\n",
	"cmd/coddy/main.go": "package main\n\n" +
		"import (\n\t\"fmt\"\n\n\t\"github.com/EvilFreelancer/coddy-agent/internal/version\"\n)\n\n" +
		"func main() { fmt.Println(version.Revision) }\n",
}

// fixtureEnv is the environment of the fixture's git, go and make. A test run
// from a git hook inherits GIT_DIR and GIT_INDEX_FILE, which would point every
// git command at the repository being committed, and a test run by `make test`
// inherits the parent make's flags and command-line variables through
// MAKEFLAGS, which would override the Makefile's own REVISION; TAGS from the
// environment would pull the SPA into the build recipe. GOFLAGS asks for the
// toolchain's VCS stamp outright, so a build that cannot read it fails.
func fixtureEnv(extra ...string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(strings.ToUpper(name), "GIT_"):
		case name == "MAKEFLAGS", name == "MFLAGS", name == "MAKEOVERRIDES", name == "MAKELEVEL", name == "TAGS":
		default:
			env = append(env, kv)
		}
	}
	env = append(env,
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull,
		"GOFLAGS=-buildvcs=true", "GOTOOLCHAIN=local", "GOWORK=off")
	return append(env, extra...)
}

func runFixture(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out)), nil
}

// repoMakefile is the Makefile of this repository, run with -f from another
// directory: its $(shell ...) values are worked out where make runs.
func repoMakefile() (string, error) {
	return filepath.Abs(filepath.Join("..", "..", "Makefile"))
}

type buildRevisionState struct {
	t *testing.T

	outer, worktree         string
	outerHead, worktreeHead string

	binary  string
	stamped *debug.BuildInfo
	named   string
}

func (s *buildRevisionState) run(dir, name string, args ...string) (string, error) {
	return runFixture(dir, fixtureEnv(), name, args...)
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
	if _, err := s.run(filepath.Dir(s.outer), "git", "init", "-q", s.outer); err != nil {
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
	for name, body := range standInModule {
		path := filepath.Join(s.worktree, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
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

func (s *buildRevisionState) theMakefileBuildsTheBinary() error {
	makefile, err := repoMakefile()
	if err != nil {
		return err
	}
	// TAGS empty on the command line: the plain build, no SPA to build first.
	if _, err := s.run(s.worktree, "make", "-s", "--no-print-directory", "-f", makefile, "build", "TAGS="); err != nil {
		return err
	}
	exe, err := s.run(s.worktree, "go", "env", "GOEXE")
	if err != nil {
		return err
	}
	s.binary = filepath.Join(s.worktree, "build", "coddy"+exe)
	return nil
}

func (s *buildRevisionState) theToolchainStampedTheEnclosingCheckout() error {
	info, err := buildinfo.ReadFile(s.binary)
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

func (s *buildRevisionState) theBuildNamedTheWorktreesCommit() error {
	named, err := s.run(s.worktree, s.binary)
	if err != nil {
		return err
	}
	if named != s.worktreeHead {
		return fmt.Errorf("the binary was linked with Revision %q, want the worktree's %s (the enclosing checkout is on %s)",
			named, s.worktreeHead, s.outerHead)
	}
	s.named = named
	return nil
}

func (s *buildRevisionState) theBinaryDescribesItselfByTheWorktreesCommit() error {
	prev := version.Revision
	version.Revision = s.named
	defer func() { version.Revision = prev }()
	if got := buildOf(s.stamped).Revision; got != s.worktreeHead {
		return fmt.Errorf("the build describes itself by %q, want the worktree's %s (stamped with %s)",
			got, s.worktreeHead, s.outerHead)
	}
	return nil
}

// skipWithoutBuildTools skips a test that needs a real checkout and the
// repository's Makefile where git, go or make is missing.
func skipWithoutBuildTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"git", "go", "make"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH: the test runs git and the Makefile", tool)
		}
	}
}

func TestBuildRevisionFeature(t *testing.T) {
	skipWithoutBuildTools(t)
	suite := godog.TestSuite{
		Name: "build-revision",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			s := &buildRevisionState{t: t}
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				*s = buildRevisionState{t: t}
				return ctx, nil
			})
			sc.Step(`^a checkout with a worktree nested in a folder of it, each on a commit of its own$`, s.aCheckoutWithANestedWorktree)
			sc.Step(`^the Makefile of the repository builds the binary in the worktree$`, s.theMakefileBuildsTheBinary)
			sc.Step(`^the Go toolchain has stamped the binary with the enclosing checkout's commit$`, s.theToolchainStampedTheEnclosingCheckout)
			sc.Step(`^the build has named the worktree's commit as the binary's revision$`, s.theBuildNamedTheWorktreesCommit)
			sc.Step(`^the binary describes itself by the worktree's commit$`, s.theBinaryDescribesItselfByTheWorktreesCommit)
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

// Where there is no commit to name - outside git, or in a repository with no
// commit yet, where `git rev-parse HEAD` prints the word HEAD - the Makefile
// names none, and the binary keeps whatever the toolchain stamped.
func TestMakefileNamesNoRevisionWithoutACommit(t *testing.T) {
	skipWithoutBuildTools(t)
	makefile, err := repoMakefile()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		init bool
	}{
		{"outside git", false},
		{"no commit yet", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// Git stops looking for a repository at the folder above, so a
			// temporary folder inside some checkout is still outside git.
			env := fixtureEnv("GIT_CEILING_DIRECTORIES=" + filepath.Dir(dir))
			if tc.init {
				if _, err := runFixture(dir, env, "git", "init", "-q"); err != nil {
					t.Fatal(err)
				}
			}
			named, err := runFixture(dir, env, "make", "-s", "--no-print-directory", "-f", makefile, "print-revision")
			if err != nil {
				t.Fatal(err)
			}
			if named != "" {
				t.Errorf("print-revision = %q, want nothing", named)
			}
		})
	}
}
