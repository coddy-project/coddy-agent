package main

// The commit gate's models check, driven through a real `git commit` in a
// scratch repository that holds this repository's .githooks/pre-commit and
// scripts/checks.sh. A `go` on PATH stands for the toolchain: `go run
// ./cmd/modelsgen ARGS` runs this test binary as modelsgen over a local catalog,
// so nothing asks models.dev, and every call is logged, so a test can tell the
// check did not run.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm/models"
)

const (
	gateCatalogPath = models.Path
	gateNewModel    = "gpt-new"

	// runAsModelsgen makes the test binary run main instead of the tests.
	runAsModelsgen = "MODELSGEN_TEST_RUN_MAIN"

	gateOldCatalog = `{"openai":{"models":{"gpt-old":{"limit":{"context":1000,"output":100}}}}}`
	gateNewCatalog = `{"openai":{"models":{"gpt-old":{"limit":{"context":1000,"output":100}},"gpt-new":{"limit":{"context":2000,"output":200}}}}}`

	gateGoShim = `#!/bin/sh
printf '%s\n' "$*" >>"$GATE_GO_LOG"
case "$1 $2" in
  "run ./cmd/modelsgen")
    shift 2
    ` + runAsModelsgen + `=1 exec "$GATE_MODELSGEN" -source "$GATE_CATALOG" "$@" ;;
esac
exit 0
`
)

func TestMain(m *testing.M) {
	if os.Getenv(runAsModelsgen) == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

// skipWithoutBash skips on Windows: the gate is bash, and the shim standing for
// go is a shell script Go's os/exec cannot start there.
func skipWithoutBash(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the commit gate is driven through a shell script standing for go")
	}
	for _, tool := range []string{"bash", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH", tool)
		}
	}
}

// gateCheckout is a scratch repository whose catalog lacks gateNewModel while
// the catalog models.dev stands for lists it.
type gateCheckout struct {
	dir     string
	catalog string
	goLog   string
	env     []string
}

func newGateCheckout(t *testing.T) *gateCheckout {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	c := &gateCheckout{
		dir:     t.TempDir(),
		catalog: filepath.Join(outside, "api.json"),
		goLog:   filepath.Join(outside, "go.log"),
	}
	writeFile(t, c.catalog, gateNewCatalog, 0o644)

	shimDir := filepath.Join(outside, "bin")
	writeFile(t, filepath.Join(shimDir, "go"), gateGoShim, 0o755)
	global := filepath.Join(outside, "gitconfig")
	writeFile(t, global, "[user]\n\tname = Gate Test\n\temail = gate@example.invalid\n", 0o644)
	c.env = []string{
		"HOME=" + outside,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + global,
		"GATE_GO_LOG=" + c.goLog,
		"GATE_MODELSGEN=" + self,
		"GATE_CATALOG=" + c.catalog,
		// The linter has its own gate and is not what this exercises.
		"CODDY_HOOK_LINT=0",
	}
	for _, kv := range os.Environ() {
		name, value, _ := strings.Cut(kv, "=")
		switch {
		case name == "HOME", strings.HasPrefix(name, "GIT_"), strings.HasPrefix(name, "CODDY_HOOK_"):
			continue
		case name == "PATH":
			c.env = append(c.env, "PATH="+shimDir+string(os.PathListSeparator)+value)
			continue
		}
		c.env = append(c.env, kv)
	}

	root := filepath.Join("..", "..")
	copyExecutable(t, filepath.Join(root, ".githooks", "pre-commit"), filepath.Join(c.dir, ".githooks", "pre-commit"))
	copyExecutable(t, filepath.Join(root, "scripts", "checks.sh"), filepath.Join(c.dir, "scripts", "checks.sh"))
	stale, err := models.Compact([]byte(gateOldCatalog))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(c.dir, filepath.FromSlash(gateCatalogPath)), string(stale), 0o644)
	writeFile(t, filepath.Join(c.dir, "main.go"), "package main\n", 0o644)
	writeFile(t, filepath.Join(c.dir, "README.md"), "# scratch\n", 0o644)

	c.git(t, "init", "-q", "-b", "main")
	c.git(t, "config", "core.hooksPath", ".githooks")
	c.git(t, "add", "-A")
	c.git(t, "commit", "-q", "--no-verify", "-m", "seed")
	return c
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func copyExecutable(t *testing.T, from, to string) {
	t.Helper()
	body, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout may carry CRLF; the shebang would not survive it.
	writeFile(t, to, strings.ReplaceAll(string(body), "\r\n", "\n"), 0o755)
}

func (c *gateCheckout) command(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.Dir = c.dir
	cmd.Env = c.env
	return cmd
}

// git runs a git command that has to succeed and returns its output.
func (c *gateCheckout) git(t *testing.T, args ...string) string {
	t.Helper()
	out, err := c.command("git", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// refreshCatalog is `make models-catalog` of the scratch repository.
func (c *gateCheckout) refreshCatalog(t *testing.T) {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := c.command(self, "-source", c.catalog)
	cmd.Env = append(cmd.Env, runAsModelsgen+"=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("refresh: %v\n%s", err, out)
	}
}

// commit attempts a commit and reports what the gate said and whether it let
// the commit through.
func (c *gateCheckout) commit(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, err := c.command("git", append([]string{"commit"}, args...)...).CombinedOutput()
	return string(out), err
}

// editCode changes a code file, so the gate treats the commit as one that
// touches code.
func (c *gateCheckout) editCode(t *testing.T) {
	t.Helper()
	writeFile(t, filepath.Join(c.dir, "main.go"), "package main\n\n// changed\n", 0o644)
}

func (c *gateCheckout) commitCodeChange(t *testing.T) (string, error) {
	t.Helper()
	c.editCode(t)
	c.git(t, "add", "main.go")
	return c.commit(t, "-m", "change code")
}

func (c *gateCheckout) ranModelsgen(t *testing.T) bool {
	t.Helper()
	log, err := os.ReadFile(c.goLog)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return strings.Contains(string(log), "run ./cmd/modelsgen")
}

func (c *gateCheckout) committedCatalog(t *testing.T) string {
	t.Helper()
	return c.git(t, "show", "HEAD:"+gateCatalogPath)
}

func requireLanded(t *testing.T, out string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("the commit was refused: %v\n%s", err, out)
	}
}

func requireRefused(t *testing.T, out string, err error) {
	t.Helper()
	if err == nil {
		t.Fatalf("the commit landed:\n%s", out)
	}
}

// A refresh that was run and not staged would otherwise pass the gate and never
// reach a commit: it is the staged copy that has to be current.
func TestCommitGateRefusesARefreshLeftUnstaged(t *testing.T) {
	skipWithoutBash(t)
	c := newGateCheckout(t)
	c.refreshCatalog(t)
	out, err := c.commitCodeChange(t)
	requireRefused(t, out, err)
	if !strings.Contains(out, "git add "+gateCatalogPath) {
		t.Errorf("the gate does not say to stage the file:\n%s", out)
	}
}

// `git commit -a` stages through a temporary index handed to the hook, which is
// what the commit will hold.
func TestCommitGateReadsTheIndexOfCommitAll(t *testing.T) {
	skipWithoutBash(t)
	c := newGateCheckout(t)
	c.refreshCatalog(t)
	c.editCode(t)
	out, err := c.commit(t, "-a", "-m", "change code")
	requireLanded(t, out, err)
	if got := c.committedCatalog(t); !strings.Contains(got, gateNewModel) {
		t.Errorf("the committed catalog does not list %s:\n%s", gateNewModel, got)
	}
}

// models.dev out of reach says nothing about the catalog: the commit goes on,
// with a word about it.
func TestCommitGateGoesOnWhenTheCatalogCannotBeRead(t *testing.T) {
	skipWithoutBash(t)
	c := newGateCheckout(t)
	if err := os.Remove(c.catalog); err != nil {
		t.Fatal(err)
	}
	out, err := c.commitCodeChange(t)
	requireLanded(t, out, err)
	if !strings.Contains(out, "not checked") {
		t.Errorf("the gate says nothing about the skipped check:\n%s", out)
	}
}

func TestCommitGateChecksNothingWhenSwitchedOff(t *testing.T) {
	skipWithoutBash(t)
	c := newGateCheckout(t)
	c.env = append(c.env, "CODDY_HOOK_MODELS=0")
	out, err := c.commitCodeChange(t)
	requireLanded(t, out, err)
	if c.ranModelsgen(t) {
		t.Errorf("modelsgen ran with the check switched off:\n%s", out)
	}
}

// A documentation commit stays fast and offline.
func TestCommitGateLeavesADocumentationCommitAlone(t *testing.T) {
	skipWithoutBash(t)
	c := newGateCheckout(t)
	writeFile(t, filepath.Join(c.dir, "README.md"), "# scratch, edited\n", 0o644)
	c.git(t, "add", "README.md")
	out, err := c.commit(t, "-m", "docs")
	requireLanded(t, out, err)
	if c.ranModelsgen(t) {
		t.Errorf("modelsgen ran for a documentation commit:\n%s", out)
	}
}

// A refresh committed on its own is what the generator wrote and needs no gate.
func TestCommitGateTakesARefreshOnItsOwn(t *testing.T) {
	skipWithoutBash(t)
	c := newGateCheckout(t)
	c.refreshCatalog(t)
	c.git(t, "add", gateCatalogPath)
	out, err := c.commit(t, "-m", "chore(models): refresh the models catalog")
	requireLanded(t, out, err)
	if got := c.committedCatalog(t); !strings.Contains(got, fmt.Sprintf("%q", gateNewModel)) {
		t.Errorf("the committed catalog does not list %s:\n%s", gateNewModel, got)
	}
}
