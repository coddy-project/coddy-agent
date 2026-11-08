//go:build desktop

package desktop

import (
	"os"
	"regexp"
	"strings"
	"testing"
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
