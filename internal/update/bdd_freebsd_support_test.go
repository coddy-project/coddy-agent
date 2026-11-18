package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// freebsdFeatureState is one scenario of features/freebsd.feature.
//
// The scenarios ask the toolchain rather than the running kernel: which file a
// package compiles for a platform is the whole of the decision this repository
// takes about that platform, and `go list` answers it for any GOOS from any
// host. The spec therefore runs in the ordinary suite instead of waiting for
// the FreeBSD job, which proves the other half - that the binary works there.
//
// The harness lives in internal/update because that package owns the release
// asset a FreeBSD machine downloads and may import the rest; the argv
// decisions of internal/platform are unit-tested where they are taken.
type freebsdFeatureState struct {
	repo     string
	workflow string
	tags     string
	files    []string
	pkg      string
}

const freebsdFeatureVersion = "1.2.3"

func (s *freebsdFeatureState) reset() { *s = freebsdFeatureState{} }

func (s *freebsdFeatureState) releaseWorkflow() error {
	b, err := os.ReadFile(filepath.Join(s.repo, ".github", "workflows", "release-binaries.yaml"))
	if err != nil {
		return err
	}
	s.workflow = string(b)
	return nil
}

func (s *freebsdFeatureState) crossBuildsFor(goos, goarch string) error {
	want := fmt.Sprintf("build_target %s %s", goos, goarch)
	if !strings.Contains(s.workflow, want) {
		return fmt.Errorf("the release workflow has no %q", want)
	}
	return nil
}

func (s *freebsdFeatureState) asksForAsset(goarch, want string) error {
	got, err := AssetFileName(freebsdFeatureVersion, "freebsd", goarch)
	if err != nil {
		return fmt.Errorf("freebsd/%s has no release asset: %w", goarch, err)
	}
	if got != want {
		return fmt.Errorf("asset = %q, want %q", got, want)
	}
	return nil
}

// theShippedTagSet reads FULL_TAGS out of the Makefile, which is the one place
// the shipped tag set is written down.
func (s *freebsdFeatureState) theShippedTagSet() error {
	b, err := os.ReadFile(filepath.Join(s.repo, "Makefile"))
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(b), "\n") {
		rest, ok := strings.CutPrefix(line, "FULL_TAGS :=")
		if !ok {
			continue
		}
		// The embedded SPA is a build artefact rather than a source file, so
		// `ui` is left out: a checkout that has not run `make ui-build` has
		// nothing to embed, and the scenario would report a missing directory
		// instead of a platform problem.
		s.tags = strings.Join(freebsdWithout(strings.Fields(rest), "ui"), ",")
		return nil
	}
	return fmt.Errorf("the Makefile sets no FULL_TAGS")
}

func (s *freebsdFeatureState) crossCompiled() error {
	out, err := s.toolchain("build", "-tags", s.tags, "./...").CombinedOutput()
	if err != nil {
		return fmt.Errorf("the tree does not cross-compile for FreeBSD: %w\n%s", err, out)
	}
	return nil
}

func (s *freebsdFeatureState) everyPackageBuilds() error { return nil }

func (s *freebsdFeatureState) builtForFreeBSD() error { return s.listFiles("") }

func (s *freebsdFeatureState) builtForFreeBSDWithTag(tag string) error { return s.listFiles(tag) }

// listFiles records the Go files every package of the tree compiles for
// FreeBSD, so a scenario can name a file and the package that owns it without
// a toolchain run of its own per line.
func (s *freebsdFeatureState) listFiles(tag string) error {
	args := []string{"list", "-f", "{{.ImportPath}} {{range .GoFiles}}{{.}} {{end}}"}
	if tag != "" {
		args = append(args, "-tags", tag)
	}
	out, err := s.toolchain(append(args, "./...")...).Output()
	if err != nil {
		return fmt.Errorf("listing the FreeBSD build: %w", err)
	}
	s.files = strings.Split(strings.TrimSpace(string(out)), "\n")
	return nil
}

// packageCompiles asserts that pkg's FreeBSD build holds file, and remembers
// the package so the next step can say "it" instead of repeating the path.
func (s *freebsdFeatureState) packageCompiles(pkg, file string) error {
	s.pkg = pkg
	has, err := s.has(pkg, file)
	if err != nil {
		return err
	}
	if !has {
		return fmt.Errorf("the FreeBSD build of %s does not compile %s", pkg, file)
	}
	return nil
}

func (s *freebsdFeatureState) doesNotCompile(file string) error {
	has, err := s.has(s.pkg, file)
	if err != nil {
		return err
	}
	if has {
		return fmt.Errorf("the FreeBSD build of %s still compiles %s", s.pkg, file)
	}
	return nil
}

func (s *freebsdFeatureState) has(pkg, file string) (bool, error) {
	const module = "github.com/EvilFreelancer/coddy-agent/"
	for _, line := range s.files {
		path, files, _ := strings.Cut(line, " ")
		if strings.TrimPrefix(path, module) != pkg {
			continue
		}
		for _, name := range strings.Fields(files) {
			if name == file {
				return true, nil
			}
		}
		return false, nil
	}
	return false, fmt.Errorf("no package %s in the FreeBSD build", pkg)
}

// toolchain runs the Go command over this checkout for FreeBSD, with the
// environment of the test but none of the three variables it sets itself.
func (s *freebsdFeatureState) toolchain(args ...string) *exec.Cmd {
	cmd := exec.CommandContext(context.Background(), "go", args...)
	cmd.Dir = s.repo
	for _, entry := range os.Environ() {
		switch {
		case strings.HasPrefix(entry, "GOOS="),
			strings.HasPrefix(entry, "GOARCH="),
			strings.HasPrefix(entry, "CGO_ENABLED="):
		default:
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "GOOS=freebsd", "GOARCH=amd64", "CGO_ENABLED=0")
	return cmd
}

func freebsdWithout(tags []string, drop string) []string {
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		if tag != drop {
			out = append(out, tag)
		}
	}
	return out
}

func TestFreeBSDFeature(t *testing.T) {
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	s := &freebsdFeatureState{}

	suite := godog.TestSuite{
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
				s.reset()
				s.repo = repo
				return ctx, nil
			})
			sc.Step(`^the release workflow of the repository$`, s.releaseWorkflow)
			sc.Step(`^it cross-builds coddy for "([^"]*)" and "([^"]*)"$`, s.crossBuildsFor)
			sc.Step(`^a FreeBSD machine on "([^"]*)" asks for "([^"]*)" to update to `+freebsdFeatureVersion+`$`, s.asksForAsset)
			sc.Step(`^the tag set the release builds$`, s.theShippedTagSet)
			sc.Step(`^the tree is cross-compiled for FreeBSD$`, s.crossCompiled)
			sc.Step(`^every package builds$`, s.everyPackageBuilds)
			sc.Step(`^Coddy is built for FreeBSD$`, s.builtForFreeBSD)
			sc.Step(`^Coddy is built for FreeBSD with the "([^"]*)" tag$`, s.builtForFreeBSDWithTag)
			sc.Step(`^the package "([^"]*)" compiles "([^"]*)"$`, s.packageCompiles)
			sc.Step(`^it does not compile "([^"]*)"$`, s.doesNotCompile)
		},
		Options: &godog.Options{
			Format: "progress",
			Paths:  []string{"../../features/freebsd.feature"},
		},
	}
	if suite.Run() != 0 {
		t.Fatal("freebsd feature failed")
	}
}
