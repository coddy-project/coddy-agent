package main

// The Homebrew cask and the tap it is published to, driven through the two
// scripts the release job runs: scripts/build-homebrew-cask.sh renders the cask
// of a release from its macOS archives, scripts/publish-homebrew-tap.sh commits
// it to the tap. Nothing here needs Homebrew: the cask is read as text and the
// tap is a bare git repository on disk. Installing the cask with a real brew is
// scripts/check-homebrew-cask.sh, run on macOS by the "Homebrew tap" workflow.

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// skipWithoutBash skips on Windows: the scripts are bash and the release job
// runs them on Linux, the check on macOS.
func skipWithoutBash(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the Homebrew scripts are bash scripts for Linux and macOS")
	}
	for _, tool := range []string{"bash", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not on PATH", tool)
		}
	}
}

// isolatedGitEnv keeps the operator's git configuration out of the scripts, so
// a commit identity or a default branch set on this machine cannot make a test
// pass that would fail on a CI runner, which has neither. useConfigOnly stops
// git from making up an identity from the user and host names, which it does
// on some machines and not on others.
func isolatedGitEnv(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	global := filepath.Join(home, "gitconfig")
	if err := os.WriteFile(global, []byte("[user]\n\tuseConfigOnly = true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=" + global}
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch {
		case name == "HOME", strings.HasPrefix(name, "GIT_"), name == "HOMEBREW_TAP_DEPLOY_KEY":
			continue
		}
		env = append(env, kv)
	}
	return env
}

// runScript runs one of the repository's scripts and returns what it printed.
func runScript(t *testing.T, env []string, script string, args ...string) (string, error) {
	t.Helper()
	return runScriptIn(t, "", env, script, args...)
}

// runScriptIn runs a script from dir ("" for the test's own directory).
func runScriptIn(t *testing.T, dir string, env []string, script string, args ...string) (string, error) {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "scripts", script))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", append([]string{path}, args...)...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// writeDarwinArchives puts stand-ins for the two macOS archives of a release in
// dir and returns their SHA-256 by cask architecture. The cask script hashes
// whatever it finds there, which is how it runs inside the release job.
func writeDarwinArchives(t *testing.T, dir, version string) map[string]string {
	t.Helper()
	sums := map[string]string{}
	for caskArch, goarch := range map[string]string{"arm": "arm64", "intel": "amd64"} {
		body := []byte(fmt.Sprintf("coddy %s darwin %s\n", version, goarch))
		name := fmt.Sprintf("coddy_%s_darwin_%s.tar.gz", version, goarch)
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		sums[caskArch] = hex.EncodeToString(sum[:])
	}
	return sums
}

// renderCask renders the cask of version from the archives in dir and returns
// the file it wrote.
func renderCask(t *testing.T, env []string, dir, version string) (string, error) {
	t.Helper()
	out, err := runScript(t, env, "build-homebrew-cask.sh", "--version", version, "--out", dir)
	if err != nil {
		return "", fmt.Errorf("build-homebrew-cask.sh: %v\n%s", err, out)
	}
	return filepath.Join(dir, "coddy.rb"), nil
}

// caskFacts is what the tests read out of a rendered cask.
type caskFacts struct {
	token   string
	version string
	arch    map[string]string // cask arch (arm, intel) -> archive arch
	sha256  map[string]string // cask arch -> checksum
	url     string            // with #{version} and #{arch} still in it
	text    string
}

var (
	caskTokenRe   = regexp.MustCompile(`(?m)^cask "([^"]+)" do$`)
	caskVersionRe = regexp.MustCompile(`(?m)^\s*version "([^"]+)"$`)
	caskArchRe    = regexp.MustCompile(`(?m)^\s*arch arm: "([^"]+)", intel: "([^"]+)"$`)
	caskShaRe     = regexp.MustCompile(`(?m)^\s*sha256 arm:\s+"([0-9a-f]+)",\s*\n\s*intel:\s+"([0-9a-f]+)"$`)
	caskURLRe     = regexp.MustCompile(`(?m)^\s*url "([^"]+)"`)
)

func parseCask(text string) (caskFacts, error) {
	f := caskFacts{text: text, arch: map[string]string{}, sha256: map[string]string{}}
	m := caskTokenRe.FindStringSubmatch(text)
	if m == nil {
		return f, fmt.Errorf("no `cask \"...\" do` line in:\n%s", text)
	}
	f.token = m[1]
	if m = caskVersionRe.FindStringSubmatch(text); m == nil {
		return f, fmt.Errorf("no version stanza in:\n%s", text)
	}
	f.version = m[1]
	if m = caskArchRe.FindStringSubmatch(text); m == nil {
		return f, fmt.Errorf("no `arch arm: ..., intel: ...` stanza in:\n%s", text)
	}
	f.arch["arm"], f.arch["intel"] = m[1], m[2]
	if m = caskShaRe.FindStringSubmatch(text); m == nil {
		return f, fmt.Errorf("no per-architecture sha256 stanza in:\n%s", text)
	}
	f.sha256["arm"], f.sha256["intel"] = m[1], m[2]
	if m = caskURLRe.FindStringSubmatch(text); m == nil {
		return f, fmt.Errorf("no url stanza in:\n%s", text)
	}
	f.url = m[1]
	return f, nil
}

// downloadFor is the URL Homebrew fetches on one architecture.
func (f caskFacts) downloadFor(caskArch string) string {
	return strings.NewReplacer("#{version}", f.version, "#{arch}", f.arch[caskArch]).Replace(f.url)
}

// caskArtifactRe matches the stanzas that link a file out of the archive.
var caskArtifactRe = regexp.MustCompile(`(?m)^\s*(binary|manpage|bash_completion|zsh_completion|fish_completion) "([^"]+)"(?:, target: "([^"]+)")?$`)

type caskArtifact struct{ stanza, source, target string }

func (f caskFacts) artifacts() []caskArtifact {
	var out []caskArtifact
	for _, m := range caskArtifactRe.FindAllStringSubmatch(f.text, -1) {
		out = append(out, caskArtifact{stanza: m[1], source: m[2], target: m[3]})
	}
	return out
}

// deprecatedCaskStanzas are spellings Homebrew 7 still loads from a third-party
// tap but warns about on every install and upgrade, and that its own taps
// reject. Each entry names what replaced it.
var deprecatedCaskStanzas = []struct {
	re   *regexp.Regexp
	note string
}{
	{regexp.MustCompile(`verified:`), "the `verified:` parameter of `url` is ignored and deprecated"},
	{regexp.MustCompile(`(?m)^\s*(uninstall_)?(pre|post)flight do\b`), "Ruby flight blocks are deprecated in favour of `*flight_steps`"},
	{regexp.MustCompile(`(?m)^\s*binary "[^"]*\.(bash|zsh|fish)"`), "completions are linked by `bash_completion` / `zsh_completion`, not as a binary"},
	{regexp.MustCompile(`depends_on macos: "`), "`depends_on macos:` takes a release symbol, not a comparison string"},
}

// newTap creates a bare git repository standing in for the tap, its main branch
// carrying a cask at version (none when version is empty).
func newTap(t *testing.T, env []string, version string) string {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "homebrew-coddy-agent.git")
	work := filepath.Join(root, "work")
	git := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=Tap Seed", "-c", "user.email=seed@example.invalid"}, args...)...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git(root, "init", "--quiet", "--bare", "--initial-branch=main", bare)
	git(root, "init", "--quiet", "--initial-branch=main", work)
	if err := os.WriteFile(filepath.Join(work, "README.md"), []byte("# Homebrew tap\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if version != "" {
		if err := os.MkdirAll(filepath.Join(work, "Casks"), 0o755); err != nil {
			t.Fatal(err)
		}
		cask := fmt.Sprintf("cask \"coddy\" do\n  version \"%s\"\nend\n", version)
		if err := os.WriteFile(filepath.Join(work, "Casks", "coddy.rb"), []byte(cask), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(work, "add", ".")
	git(work, "commit", "--quiet", "-m", "seed")
	git(work, "push", "--quiet", bare, "HEAD:main")
	return bare
}

// publishCask publishes the rendered cask of version to the tap.
func publishCask(t *testing.T, env []string, tap, version, cask string) (string, error) {
	t.Helper()
	return runScript(t, env, "publish-homebrew-tap.sh", "--version", version, "--cask", cask, "--tap-repo", tap)
}

// tapGit runs a read-only git command against the bare tap.
func tapGit(t *testing.T, env []string, tap string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"--git-dir", tap}, args...)...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestHomebrewCaskLinksOnlyWhatTheArchiveCarries(t *testing.T) {
	// The cask links files out of the macOS archive by name, so every file it
	// names has to be a member of the archive the release job packs.
	workflow := readRepoFile(t, "../../.github/workflows/release-binaries.yaml")
	m := regexp.MustCompile(`tar -C "\$bindir" -czf "\$DIST/\$\{stem\}\.tar\.gz" ([^\n]+)`).FindStringSubmatch(workflow)
	if m == nil {
		t.Fatal("release-binaries.yaml: no `tar -C \"$bindir\" -czf` line packing the macOS archive")
	}
	members := map[string]bool{}
	for _, name := range strings.Fields(m[1]) {
		members[name] = true
	}
	f := caskFacts{text: readRepoFile(t, "../../packaging/homebrew/coddy.rb.tmpl")}
	if len(f.artifacts()) == 0 {
		t.Fatal("the cask template links nothing")
	}
	for _, a := range f.artifacts() {
		if !members[a.source] {
			t.Errorf("the cask links %q (%s), which the macOS archive does not carry (members: %s)", a.source, a.stanza, m[1])
		}
	}
}

func TestHomebrewCaskRenderedWithoutTheTemplateHeader(t *testing.T) {
	skipWithoutBash(t)
	env := isolatedGitEnv(t)
	dir := t.TempDir()
	writeDarwinArchives(t, dir, "1.3.14")
	path, err := renderCask(t, env, dir, "1.3.14")
	if err != nil {
		t.Fatal(err)
	}
	text := readRepoFile(t, path)
	if !strings.HasPrefix(text, "cask \"coddy\" do\n") {
		t.Errorf("the rendered cask must start at its `cask` line, got:\n%s", text)
	}
	if strings.Contains(text, "__") {
		t.Errorf("the rendered cask still carries a placeholder:\n%s", text)
	}
}

func TestPublishHomebrewTapTwiceAddsNoCommit(t *testing.T) {
	skipWithoutBash(t)
	env := isolatedGitEnv(t)
	dir := t.TempDir()
	writeDarwinArchives(t, dir, "1.3.14")
	cask, err := renderCask(t, env, dir, "1.3.14")
	if err != nil {
		t.Fatal(err)
	}
	tap := newTap(t, env, "1.3.13")
	if out, err := publishCask(t, env, tap, "1.3.14", cask); err != nil {
		t.Fatalf("first publish: %v\n%s", err, out)
	}
	head := tapGit(t, env, tap, "rev-parse", "main")
	out, err := publishCask(t, env, tap, "1.3.14", cask)
	if err != nil {
		t.Fatalf("second publish of the same cask must succeed: %v\n%s", err, out)
	}
	if again := tapGit(t, env, tap, "rev-parse", "main"); again != head {
		t.Errorf("publishing the same cask again moved the tap from %s to %s", head, again)
	}
	if !strings.Contains(out, "already") {
		t.Errorf("a publish that changes nothing should say so, got:\n%s", out)
	}
}

func TestPublishHomebrewTapKeepsANewerRelease(t *testing.T) {
	// A release job re-run for an old tag must not take the tap back. 1.3.10 is
	// newer than 1.3.9: versions compare by number, not as strings.
	skipWithoutBash(t)
	env := isolatedGitEnv(t)
	dir := t.TempDir()
	writeDarwinArchives(t, dir, "1.3.9")
	cask, err := renderCask(t, env, dir, "1.3.9")
	if err != nil {
		t.Fatal(err)
	}
	tap := newTap(t, env, "1.3.10")
	head := tapGit(t, env, tap, "rev-parse", "main")
	out, err := publishCask(t, env, tap, "1.3.9", cask)
	if err != nil {
		t.Fatalf("an older release is skipped, not an error: %v\n%s", err, out)
	}
	if again := tapGit(t, env, tap, "rev-parse", "main"); again != head {
		t.Errorf("an older release replaced the tap's cask:\n%s", tapGit(t, env, tap, "show", "main:Casks/coddy.rb"))
	}
	if !strings.Contains(out, "1.3.10") {
		t.Errorf("the skip should name the version the tap keeps, got:\n%s", out)
	}
}

func TestPublishHomebrewTapRefusesAMismatchedVersion(t *testing.T) {
	skipWithoutBash(t)
	env := isolatedGitEnv(t)
	dir := t.TempDir()
	writeDarwinArchives(t, dir, "1.3.13")
	cask, err := renderCask(t, env, dir, "1.3.13")
	if err != nil {
		t.Fatal(err)
	}
	tap := newTap(t, env, "1.3.12")
	head := tapGit(t, env, tap, "rev-parse", "main")
	out, err := publishCask(t, env, tap, "1.3.14", cask)
	if err == nil {
		t.Fatalf("a cask rendered for 1.3.13 was published as 1.3.14:\n%s", out)
	}
	if again := tapGit(t, env, tap, "rev-parse", "main"); again != head {
		t.Error("a refused publish moved the tap")
	}
}

func TestPublishHomebrewTapStartsAnEmptyTap(t *testing.T) {
	skipWithoutBash(t)
	env := isolatedGitEnv(t)
	dir := t.TempDir()
	writeDarwinArchives(t, dir, "1.3.14")
	cask, err := renderCask(t, env, dir, "1.3.14")
	if err != nil {
		t.Fatal(err)
	}
	tap := newTap(t, env, "")
	if out, err := publishCask(t, env, tap, "1.3.14", cask); err != nil {
		t.Fatalf("publish to a tap without a cask: %v\n%s", err, out)
	}
	if got, want := tapGit(t, env, tap, "show", "main:Casks/coddy.rb"), readRepoFile(t, cask); got != want {
		t.Errorf("the tap's cask differs from the rendered one:\n%s", got)
	}
	if readme := tapGit(t, env, tap, "show", "main:README.md"); readme != "# Homebrew tap\n" {
		t.Errorf("publishing touched the tap's README:\n%s", readme)
	}
}

func TestPublishHomebrewTapRetriesARejectedPush(t *testing.T) {
	// Two releases merged close together publish at the same time, and the
	// second push is refused. The script reads the tap again and retries.
	skipWithoutBash(t)
	env := isolatedGitEnv(t)
	dir := t.TempDir()
	writeDarwinArchives(t, dir, "1.3.14")
	cask, err := renderCask(t, env, dir, "1.3.14")
	if err != nil {
		t.Fatal(err)
	}
	tap := newTap(t, env, "1.3.13")
	hook := "#!/bin/sh\nif [ -f \"$GIT_DIR/rejected-once\" ]; then exit 0; fi\ntouch \"$GIT_DIR/rejected-once\"\necho 'busy' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(tap, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := publishCask(t, env, tap, "1.3.14", cask)
	if err != nil {
		t.Fatalf("a push refused once must be retried: %v\n%s", err, out)
	}
	if !strings.Contains(out, "rejected") {
		t.Errorf("the retry should be reported, got:\n%s", out)
	}
	if got := tapGit(t, env, tap, "show", "main:Casks/coddy.rb"); got != readRepoFile(t, cask) {
		t.Errorf("after the retry the tap carries:\n%s", got)
	}
}

func TestHomebrewCaskTakesChecksumsFromTheRelease(t *testing.T) {
	// Outside the release job the archives are not on disk, and the release's
	// own SHA256SUMS gives their checksums without downloading them.
	skipWithoutBash(t)
	env := isolatedGitEnv(t)
	releases := t.TempDir()
	arm := strings.Repeat("a", 64)
	intel := strings.Repeat("b", 64)
	sums := fmt.Sprintf("%s  coddy_1.3.14_linux_amd64.tar.gz\n%s  coddy_1.3.14_darwin_amd64.tar.gz\n%s  coddy_1.3.14_darwin_arm64.tar.gz\n",
		strings.Repeat("c", 64), intel, arm)
	if err := os.MkdirAll(filepath.Join(releases, "1.3.14"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(releases, "1.3.14", "SHA256SUMS"), []byte(sums), 0o644); err != nil {
		t.Fatal(err)
	}
	base := "file://" + filepath.ToSlash(releases)
	dir := t.TempDir()
	out, err := runScript(t, env, "build-homebrew-cask.sh", "--version", "1.3.14", "--out", dir, "--release-url", base)
	if err != nil {
		t.Fatalf("render from SHA256SUMS: %v\n%s", err, out)
	}
	f, err := parseCask(readRepoFile(t, filepath.Join(dir, "coddy.rb")))
	if err != nil {
		t.Fatal(err)
	}
	if f.sha256["arm"] != arm || f.sha256["intel"] != intel {
		t.Errorf("checksums arm=%s intel=%s, want the SHA256SUMS entries arm=%s intel=%s", f.sha256["arm"], f.sha256["intel"], arm, intel)
	}

	// A list without the macOS archives is an error, not an empty checksum.
	if err := os.WriteFile(filepath.Join(releases, "1.3.14", "SHA256SUMS"), []byte(strings.Repeat("c", 64)+"  coddy_1.3.14_linux_amd64.tar.gz\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := runScript(t, env, "build-homebrew-cask.sh", "--version", "1.3.14", "--out", t.TempDir(), "--release-url", base); err == nil {
		t.Errorf("a SHA256SUMS without the darwin archives rendered a cask:\n%s", out)
	}
}

func TestPublishHomebrewTapCommitsWithoutTheCallersRepoIdentity(t *testing.T) {
	// `make brew-publish` runs from a checkout that may carry its own
	// user.email; the commit is made in a fresh clone of the tap, which does
	// not inherit it, so the identity has to be judged there.
	skipWithoutBash(t)
	env := isolatedGitEnv(t)
	dir := t.TempDir()
	writeDarwinArchives(t, dir, "1.3.14")
	cask, err := renderCask(t, env, dir, "1.3.14")
	if err != nil {
		t.Fatal(err)
	}
	tap := newTap(t, env, "1.3.13")
	caller := t.TempDir()
	for _, args := range [][]string{{"init", "--quiet"}, {"config", "user.email", "operator@example.invalid"}, {"config", "user.name", "Operator"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = caller
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	out, err := runScriptIn(t, caller, env, "publish-homebrew-tap.sh", "--version", "1.3.14", "--cask", cask, "--tap-repo", tap)
	if err != nil {
		t.Fatalf("publish from a checkout with a local identity: %v\n%s", err, out)
	}
	if got := tapGit(t, env, tap, "show", "main:Casks/coddy.rb"); got != readRepoFile(t, cask) {
		t.Errorf("the tap's cask was not published:\n%s", got)
	}
}
