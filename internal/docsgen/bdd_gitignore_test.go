package docsgen

// Godog harness for features/docsgen_gitignore.feature: proves that the
// documentation checks leave the paths git ignores alone - local scratch under
// docs/ is not a page of the map and its links are nobody's contract - while a
// page the map really is missing, a broken link on a page of the map and the
// design records of docs/plans keep behaving as they did. Every scenario runs
// against a real repository in a temporary directory.

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

type docsIgnoreWorld struct {
	root     string
	listed   []string
	problems []Problem
}

func (w *docsIgnoreWorld) repositoryIgnoring(pattern string) error {
	root, err := os.MkdirTemp("", "docsgen-bdd-")
	if err != nil {
		return err
	}
	w.root = root
	if err := initGitRepo(root); err != nil {
		return err
	}
	return w.writeFile(".gitignore", pattern+"\n")
}

func (w *docsIgnoreWorld) writeFile(rel, content string) error {
	p := filepath.Join(w.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

// page writes a documentation page, carrying a link when target is given.
func (w *docsIgnoreWorld) page(rel, target string) error {
	body := "# " + strings.TrimSuffix(filepath.Base(rel), ".md") + "\n"
	if target != "" {
		body += "\nSee [there](" + target + ").\n"
	}
	return w.writeFile(rel, body)
}

func (w *docsIgnoreWorld) listedPage(rel string) error { return w.listedPageLinkingTo(rel, "") }

func (w *docsIgnoreWorld) listedPageLinkingTo(rel, target string) error {
	w.listed = append(w.listed, rel)
	return w.page(rel, target)
}

func (w *docsIgnoreWorld) unlistedFile(rel string) error { return w.page(rel, "") }

func (w *docsIgnoreWorld) unlistedFileLinkingTo(rel, target string) error {
	return w.page(rel, target)
}

// checksRun writes the map over the pages declared as listed, then runs what
// make docs-check runs over the tree: the map check and the link check.
func (w *docsIgnoreWorld) checksRun() error {
	nav := "groups:\n  - id: docs\n    title: Docs\n    summary: The documentation.\n    pages:\n"
	for _, rel := range w.listed {
		navPath := strings.TrimPrefix(rel, "docs/")
		nav += fmt.Sprintf("      - path: %s\n        title: %s\n        summary: A page.\n", navPath, navPath)
	}
	if err := w.writeFile(NavFile, nav); err != nil {
		return err
	}
	nav0, err := LoadNav(w.root)
	if err != nil {
		return err
	}
	files, err := DocsMarkdown(w.root, false)
	if err != nil {
		return err
	}
	w.problems = append(CheckNav(w.root, nav0), CheckLinks(w.root, files)...)
	return nil
}

func (w *docsIgnoreWorld) checksPass() error {
	if len(w.problems) > 0 {
		return fmt.Errorf("expected no problems, got %v", w.problems)
	}
	return nil
}

func (w *docsIgnoreWorld) reported(rel, want string) error {
	for _, p := range w.problems {
		if p.File == rel && strings.Contains(p.Message, want) {
			return nil
		}
	}
	return fmt.Errorf("nothing reported %q for %s; got %v", want, rel, w.problems)
}

func initializeDocsIgnoreScenario(sc *godog.ScenarioContext) {
	w := &docsIgnoreWorld{}

	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if w.root != "" {
			_ = os.RemoveAll(w.root)
		}
		return ctx, err
	})

	sc.Given(`^a repository whose \.gitignore excludes "([^"]*)"$`, w.repositoryIgnoring)
	sc.Given(`^the page "([^"]*)" listed in docs/nav\.yaml$`, w.listedPage)
	sc.Given(`^the page "([^"]*)" listed in docs/nav\.yaml and linking to "([^"]*)"$`, w.listedPageLinkingTo)
	sc.Given(`^the file "([^"]*)" which docs/nav\.yaml does not list$`, w.unlistedFile)
	sc.Given(`^the file "([^"]*)" which docs/nav\.yaml does not list, linking to "([^"]*)"$`, w.unlistedFileLinkingTo)

	sc.When(`^the documentation checks run$`, w.checksRun)

	sc.Then(`^the documentation checks pass$`, w.checksPass)
	sc.Then(`^"([^"]*)" is reported as not listed in docs/nav\.yaml$`, func(rel string) error {
		return w.reported(rel, "not listed in "+NavFile)
	})
	sc.Then(`^"([^"]*)" is reported as carrying a broken link$`, func(rel string) error {
		return w.reported(rel, "broken link")
	})
}

func TestDocsGitignoreFeature(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	// The answer must come from the .gitignore each scenario writes and from
	// nothing the developer configured globally.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	suite := godog.TestSuite{
		Name:                "docsgen-gitignore",
		ScenarioInitializer: initializeDocsIgnoreScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/docsgen_gitignore.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("docsgen gitignore feature suite failed")
	}
}
