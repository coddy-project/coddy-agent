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
	cmd := exec.CommandContext(context.Background(), "go", "list", "-json", "-tags", "desktop desktop.native", ".")
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CGO_ENABLED=") && !strings.HasPrefix(entry, "USER=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "CGO_ENABLED=0")
	platform.AdaptCommand(cmd)
	output, err := cmd.Output()
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
