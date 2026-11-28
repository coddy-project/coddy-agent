//go:build desktop

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"runtime"

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

// Darwin releases are cross-compiled on the Linux runner, like every other
// target, and carry the native window anyway.
//
// The Cocoa/WKWebView window reaches AppKit through the Objective-C runtime
// rather than through a C bridge, so it needs no compiler, no macOS SDK and no
// macOS runner: `CGO_ENABLED=0 GOOS=darwin go build` produces it. One job
// builds and publishes every archive of a release, the two darwin ones
// included, so there is no second builder to keep in step and no artifact to
// hand between jobs.
func TestDarwinReleaseCrossCompilesWithTheRest(t *testing.T) {
	makefile, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(makefile), "desktop.native") {
		t.Error("the native window needs no build tag: the Makefile still names desktop.native")
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
	type workflowFile struct {
		Jobs map[string]struct {
			RunsOn string `yaml:"runs-on"`
			Env    map[string]string
			Needs  string
			Steps  []step
		}
	}
	var workflow workflowFile
	if err := yaml.Unmarshal(b, &workflow); err != nil {
		t.Fatal(err)
	}
	if _, ok := workflow.Jobs["macos-archives"]; ok {
		t.Error("the darwin archives are cross-compiled with the rest: the release still runs a macOS builder")
	}
	publisher := workflow.Jobs["release-binaries"]
	if publisher.RunsOn != "ubuntu-latest" {
		t.Fatalf("the release is built on the Linux runner: %+v", publisher)
	}
	if publisher.Needs != "" {
		t.Errorf("the release job waits for %q, and nothing builds before it", publisher.Needs)
	}
	var build string
	for _, step := range publisher.Steps {
		build += step.Run + "\n"
		if strings.HasPrefix(step.Uses, "actions/download-artifact@") {
			t.Errorf("the release downloads %q, and no job hands it an archive", step.With["name"])
		}
	}
	for _, arch := range []string{"arm64", "amd64"} {
		if !strings.Contains(build, "build_target darwin "+arch) {
			t.Errorf("the release does not cross-build darwin %s", arch)
		}
	}
	// The release binary of a Mac is still driven on a Mac: cross-compiling is
	// not proof that a window opens. This is also the only job that runs a
	// `desktop` tag there, so it runs the whole package rather than the native
	// tests alone (issue 748).
	pr, err := os.ReadFile("../../.github/workflows/tests-on-pr.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var prWorkflow workflowFile
	if err := yaml.Unmarshal(pr, &prWorkflow); err != nil {
		t.Fatal(err)
	}
	var prBuild string
	for _, step := range prWorkflow.Jobs["test-macos"].Steps {
		prBuild += step.Run + "\n"
	}
	macTest := regexp.MustCompile(`go test -tags desktop \./external/desktop\b([^\n]*)`).FindStringSubmatch(prBuild)
	if macTest == nil {
		t.Error("the macOS PR job must drive a real window with `go test -tags desktop ./external/desktop`")
	} else if strings.Contains(macTest[1], "-run") {
		t.Errorf("the macOS job must run the whole package, not %q", strings.TrimSpace(macTest[0]))
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

// The native window follows the platform, and nothing else.
//
// There is no tag to remember or to forget and no compiler to have installed:
// a macOS build compiles the Cocoa window whether or not cgo is on, and every
// other platform compiles the stub beside it. The two constraints are
// complements, so for every combination below exactly one of the files is in
// the package, and the window's own files are ordinary Go - a darwin build has
// no CgoFiles at all, which is what lets the release cross-compile it.
func TestTheNativeWindowFollowsMacOS(t *testing.T) {
	for _, build := range []struct {
		goos, arch, cgo string
		native          bool
	}{
		{"darwin", "arm64", "0", true},
		{"darwin", "amd64", "0", true},
		{"darwin", "arm64", "1", true},
		{"linux", "amd64", "1", false},
		{"windows", "amd64", "0", false},
	} {
		name := build.goos + "-" + build.arch + "-cgo" + build.cgo
		t.Run(name, func(t *testing.T) {
			output, err := goCommand(t, build.goos, build.arch, build.cgo, "list", "-json", "-tags", "desktop", ".").Output()
			if err != nil {
				t.Fatalf("go list for %s: %v", name, err)
			}
			var pkg struct {
				GoFiles  []string
				CgoFiles []string
			}
			if err := json.Unmarshal(output, &pkg); err != nil {
				t.Fatal(err)
			}
			for _, file := range []string{"native_darwin.go", "native_cocoa_darwin.go"} {
				if got := contains(pkg.GoFiles, file); got != build.native {
					t.Errorf("%s compiles %s: %v, want %v (GoFiles=%v)", name, file, got, build.native, pkg.GoFiles)
				}
			}
			if len(pkg.CgoFiles) != 0 {
				t.Errorf("%s needs a C compiler for the window: %v", name, pkg.CgoFiles)
			}
			if got := contains(pkg.GoFiles, "native_stub.go"); got == build.native {
				t.Errorf("%s compiles the stub: %v (GoFiles=%v)", name, got, pkg.GoFiles)
			}
		})
	}
	// Selection is not compilation. Both halves of the partition are compiled
	// here, the macOS one for the two architectures the release publishes, so
	// the host that builds a release proves the window compiles for a Mac
	// before the release does.
	for _, build := range []struct{ goos, arch string }{
		{runtime.GOOS, runtime.GOARCH},
		{"darwin", "arm64"},
		{"darwin", "amd64"},
	} {
		name := build.goos + "-" + build.arch
		if out, err := goCommand(t, build.goos, build.arch, "0", "build", "-o", os.DevNull, "-tags", "desktop", ".").CombinedOutput(); err != nil {
			t.Errorf("the package does not cross-compile for %s without cgo: %v\n%s", name, err, out)
		}
	}
}

// The entry writer follows the desktop tag, as every other file here does.
func TestTheEntryWriterFollowsTheDesktopTag(t *testing.T) {
	files := []string{"menu_shortcut_linux.go", "menu_shortcut_windows.go", "shortcut_receipt.go"}
	for _, build := range []struct {
		goos, arch string
		writer     []string
	}{
		{"linux", "amd64", []string{"menu_shortcut_linux.go", "shortcut_receipt.go"}},
		{"windows", "amd64", []string{"menu_shortcut_windows.go", "shortcut_receipt.go"}},
		{"darwin", "arm64", nil},
		{"freebsd", "amd64", nil},
		{"android", "arm64", nil},
	} {
		for _, tags := range []string{"cli", "desktop", strings.Join(makeVariable(t, "FULL_TAGS"), ",")} {
			got := packageFiles(t, build.goos, build.arch, tags)
			hasTag := contains(strings.Split(tags, ","), "desktop")
			if !hasTag && len(got) != 0 {
				t.Errorf("%s without the desktop tag compiles %v of this package", build.goos, got)
			}
			for _, file := range files {
				want := hasTag && contains(build.writer, file)
				if have := contains(got, file); have != want {
					t.Errorf("%s with %q compiles %s: %v, want %v", build.goos, tags, file, have, want)
				}
			}
		}
	}
}

// packageFiles lists what one tag combination compiles, empty when the
// constraints exclude the whole package.
func packageFiles(t *testing.T, goos, arch, tags string) []string {
	t.Helper()
	output, err := goCommand(t, goos, arch, "0", "list", "-json", "-tags", tags, ".").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && strings.Contains(string(exit.Stderr), "build constraints exclude all Go files") {
			return nil
		}
		t.Fatalf("go list for %s with %q: %v", goos, tags, err)
	}
	var pkg struct{ GoFiles []string }
	if err := json.Unmarshal(output, &pkg); err != nil {
		t.Fatal(err)
	}
	return pkg.GoFiles
}

// goCommand runs the toolchain for one platform, with the environment this
// process was started with but none of the three variables it sets itself.
func goCommand(t *testing.T, goos, arch, cgo string, args ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "go", args...)
	for _, entry := range os.Environ() {
		switch {
		case strings.HasPrefix(entry, "CGO_ENABLED="),
			strings.HasPrefix(entry, "GOOS="),
			strings.HasPrefix(entry, "GOARCH="),
			strings.HasPrefix(entry, "USER="):
		default:
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "CGO_ENABLED="+cgo, "GOOS="+goos, "GOARCH="+arch)
	platform.AdaptCommand(cmd)
	return cmd
}
