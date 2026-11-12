//go:build desktop

package desktop

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"

	"regexp"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"

	"gopkg.in/yaml.v3"
)

// The window ships with the release binary.
//
// It is a verb of `coddy` and not a binary of its own, so what decides whether
// a release can open a window is the shipped tag set: `desktop` is in it, and
// everything built from it - the release archives, the Homebrew formula, the
// Linux packages, the container image - carries the window. The image gains
// nothing it can run, since a container has no display, and builds it anyway
// so that a `coddy` is the same program wherever it came from.
//
// The two halves of the surface are checked together: a `desktop` without
// `http` and `ui` compiles to the stub that says the window is not built in,
// which is not what a release should ship.
func TestTheReleaseTagSetCarriesTheWindow(t *testing.T) {
	full := makeVariable(t, "FULL_TAGS")
	for _, tag := range []string{"desktop", "http", "ui"} {
		if !contains(full, tag) {
			t.Errorf("the shipped tag set has no %q, so the release binary has no window: %v", tag, full)
		}
	}
}

// Darwin releases add the native backend without changing other platforms.
func TestDarwinReleaseCarriesTheNativeWindow(t *testing.T) {
	full := makeVariable(t, "FULL_TAGS")
	darwin := makeVariable(t, "DARWIN_TAGS")
	if contains(full, "desktop.native") || !sameTags(darwin, append(full, "desktop.native")) {
		t.Fatalf("platform tag sets: full=%v darwin=%v", full, darwin)
	}
	b, err := os.ReadFile("../../.github/workflows/release-binaries.yaml")
	if err != nil {
		t.Fatal(err)
	}
	type step struct {
		Uses string
		Run  string
		With map[string]string
	}
	var workflow struct {
		Jobs map[string]struct {
			RunsOn string `yaml:"runs-on"`
			Env    map[string]string
			Needs  string
			Steps  []step
		}
	}
	if err := yaml.Unmarshal(b, &workflow); err != nil {
		t.Fatal(err)
	}
	mac := workflow.Jobs["macos-archives"]
	if mac.RunsOn != "macos-latest" || mac.Env["CGO_ENABLED"] != "1" || mac.Env["MACOSX_DEPLOYMENT_TARGET"] != "13.0" {
		t.Fatalf("Darwin releases must use a macOS cgo builder with the supported deployment minimum: %+v", mac)
	}
	var build string
	var uploaded bool
	for _, step := range mac.Steps {
		build += step.Run + "\n"
		if strings.HasPrefix(step.Uses, "actions/upload-artifact@") && step.With["name"] == "macos-release-archives" {
			uploaded = true
		}
	}
	for _, contract := range []string{"make -s print-darwin-tags", "for arch in arm64 amd64", "clang -arch x86_64", "-tags=\"${DARWIN_TAGS}\""} {
		if !strings.Contains(build, contract) {
			t.Errorf("Darwin archive builder lost %q", contract)
		}
	}
	if !uploaded {
		t.Error("Darwin archive builder no longer uploads its archives")
	}
	publisher := workflow.Jobs["release-binaries"]
	if publisher.Needs != "macos-archives" {
		t.Error("publisher must wait for the Darwin archive builder")
	}
	downloaded := false
	for _, step := range publisher.Steps {
		if strings.HasPrefix(step.Uses, "actions/download-artifact@") && step.With["name"] == "macos-release-archives" {
			downloaded = true
		}
		if strings.Contains(step.Run, "build_target linux amd64") && !downloaded {
			t.Error("publisher must download Darwin archives before packaging and checksumming")
		}
		if strings.Contains(step.Run, "build_target darwin") {
			t.Error("publisher must not cross-build Darwin without cgo")
		}
	}
	if !downloaded {
		t.Error("publisher no longer downloads the Darwin archives")
	}
}

// makeVariable reads one `NAME := a b c` line of the repository's Makefile,
// with the variables it names in turn put in their place.
func makeVariable(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("reading the Makefile: %v", err)
	}
	return strings.Fields(expandMakeValue(t, string(b), name, 0))
}

func expandMakeValue(t *testing.T, makefile, name string, depth int) string {
	t.Helper()
	if depth > 8 {
		t.Fatalf("%s is defined in terms of itself", name)
	}
	re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `\s*:?=\s*(.*)$`)
	m := re.FindStringSubmatch(makefile)
	if m == nil {
		t.Fatalf("the Makefile sets no %s", name)
	}
	ref := regexp.MustCompile(`\$\(([A-Za-z_][A-Za-z0-9_]*)\)`)
	return ref.ReplaceAllStringFunc(m[1], func(r string) string {
		return expandMakeValue(t, makefile, ref.FindStringSubmatch(r)[1], depth+1)
	})
}

func contains(have []string, want string) bool {
	for _, s := range have {
		if s == want {
			return true
		}
	}
	return false
}

// The same tag set, in the five places the build reads it from.
//
// Two of them derive from FULL_TAGS (PKG_TAGS, LINT_TAGS_NO_UI); the four
// below are written out by hand, in their own order and their own syntax, and
// nothing but this test holds them to it. A release built without `desktop`,
// or an image built with a tag the release does not carry, is a `coddy` that
// is not the same program depending on where it came from - which is the
// promise the Makefile makes at FULL_TAGS.
func TestEveryBuildNamesTheSameTags(t *testing.T) {
	full := makeVariable(t, "FULL_TAGS")
	for _, place := range []struct {
		name string
		file string
		re   string
	}{
		{"the container image", "../../Dockerfile", `(?m)^ARG BUILD_TAGS=(.*)$`},
		{"the release binaries", "../../.github/workflows/release-binaries.yaml", `(?m)^\s*GO_BUILD_TAGS:\s*"([^"]*)"`},
		{"the published image", "../../.github/workflows/docker-build-push.yaml", `(?m)^\s*BUILD_TAGS:\s*"([^"]*)"`},
		{"the Homebrew formula", "../../packaging/homebrew/coddy-formula.rb.tmpl", `(?m)^\s*tags = %w\[([^\]]*)\]`},
	} {
		b, err := os.ReadFile(place.file)
		if err != nil {
			t.Errorf("reading %s: %v", place.file, err)
			continue
		}
		m := regexp.MustCompile(place.re).FindSubmatch(b)
		if m == nil {
			t.Errorf("%s (%s) names no tag list this test can read", place.name, place.file)
			continue
		}
		got := strings.FieldsFunc(string(m[1]), func(r rune) bool { return r == ',' || r == ' ' })
		if !sameTags(got, full) {
			t.Errorf("%s (%s) builds %v, and the shipped set is %v", place.name, place.file, got, full)
		}
	}
}

// sameTags compares two tag lists as sets: the order differs from one file to
// the next and means nothing to the compiler.
func sameTags(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for _, tag := range a {
		if !contains(b, tag) {
			return false
		}
	}
	return true
}

func TestNativeTagWithoutCgoKeepsTheStub(t *testing.T) {
	withoutCgo := func(args ...string) *exec.Cmd {
		cmd := exec.CommandContext(context.Background(), "go", args...)
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "CGO_ENABLED=") && !strings.HasPrefix(entry, "USER=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "CGO_ENABLED=0")
		platform.AdaptCommand(cmd)
		return cmd
	}
	if out, err := withoutCgo("build", "-o", os.DevNull, "-tags", "desktop desktop.native", ".").CombinedOutput(); err != nil {
		t.Fatalf("the package does not build with desktop.native and no cgo: %v\n%s", err, out)
	}
	output, err := withoutCgo("list", "-json", "-tags", "desktop desktop.native", ".").Output()
	if err != nil {
		t.Fatalf("go list without cgo: %v", err)
	}
	var pkg struct {
		GoFiles  []string
		CgoFiles []string
	}
	if err := json.Unmarshal(output, &pkg); err != nil {
		t.Fatal(err)
	}
	if !contains(pkg.GoFiles, "native_stub.go") || contains(pkg.CgoFiles, "native_darwin.go") {
		t.Fatalf("without cgo: GoFiles=%v CgoFiles=%v", pkg.GoFiles, pkg.CgoFiles)
	}
}
