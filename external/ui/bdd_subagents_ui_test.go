//go:build http && ui

package ui

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// runVitestScenario runs one named Vitest test, where the React component is
// actually rendered against a stubbed API. The http,ui test matrix installs
// the dependencies through make ui-build.
func runVitestScenario(file, name string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "node_modules/vitest/vitest.mjs", "run",
		"--configLoader", "runner", file, "--testNamePattern", "^"+regexp.QuoteMeta(name)+"$")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %q: %w\n%s", file, name, err, out)
	}
	return vitestPassedSome(file, name, out)
}

var (
	ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)
	// The Tests line of Vitest's summary, e.g. "Tests  1 passed | 62 skipped (63)".
	vitestTestsPassed = regexp.MustCompile(`(?m)^\s*Tests\s+[1-9]\d* passed\b`)
)

// vitestPassedSome fails unless Vitest's summary counts a passed test. Vitest
// exits 0 when the name pattern matches nothing ("Tests  63 skipped (63)"), so
// the exit status alone lets a step pointing at a renamed test pass.
func vitestPassedSome(file, name string, out []byte) error {
	if vitestTestsPassed.Match(ansiEscape.ReplaceAll(out, nil)) {
		return nil
	}
	return fmt.Errorf("%s: no test named %q ran and passed\n%s", file, name, out)
}

func TestVitestPassedSome(t *testing.T) {
	cases := []struct {
		name string
		out  string
		ok   bool
	}{
		{"one passed", " Test Files  1 passed (1)\n      Tests  1 passed | 62 skipped (63)\n", true},
		{"several passed", "      Tests  12 passed (12)\n", true},
		{"colored", "\x1b[2m      Tests \x1b[22m \x1b[1m\x1b[32m1 passed\x1b[39m\x1b[22m\x1b[90m (1)\x1b[39m\n", true},
		{"nothing matched", " Test Files  1 skipped (1)\n      Tests  63 skipped (63)\n", false},
		{"only the file passed", " Test Files  1 passed (1)\n", false},
		{"failed", "      Tests  1 failed | 62 skipped (63)\n", false},
		{"no summary", "", false},
	}
	for _, c := range cases {
		err := vitestPassedSome("src/x.test.tsx", "a test", []byte(c.out))
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok = %v", c.name, err, c.ok)
		}
		if err != nil && (!strings.Contains(err.Error(), "src/x.test.tsx") || !strings.Contains(err.Error(), `"a test"`)) {
			t.Errorf("%s: error does not name the file and the test: %v", c.name, err)
		}
	}
}

// A step whose Vitest test was renamed or mistyped must fail: Vitest itself
// exits 0 when the name pattern matches nothing and every test is skipped.
func TestRunVitestScenarioFailsWhenNoTestMatches(t *testing.T) {
	const file, name = "src/ui/chat/backgroundWake.test.ts", "no such test"
	err := runVitestScenario(file, name)
	if err == nil {
		t.Fatal("a name no test carries passed")
	}
	for _, want := range []string{file, name} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not name %q: %v", want, err)
		}
	}
}

func TestSubagentsWebUIFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name: "subagents_web_ui",
		ScenarioInitializer: func(sc *godog.ScenarioContext) {
			sc.Step(`^a new background permission follows a reader at the bottom without interrupting a reader of older messages$`, func() error {
				return runVitestScenario("src/ui/chat/ChatScreen.test.tsx",
					"new background permission prompts follow the reader at the bottom, but polling does not")
			})
			sc.Step(`^the Subagents settings tab lists the definitions of the session workspace with their scope, description and file$`, func() error {
				return runVitestScenario("src/ui/settings/SubagentsSection.test.tsx",
					"lists every definition of the session workspace with its scope, description and file")
			})
			sc.Step(`^a background subagent's prompt waits at the end of its parent chat$`, func() error {
				return runVitestScenario("src/ui/chat/ChatScreen.test.tsx",
					"a background subagent's prompt waits at the end of its parent chat")
			})
			sc.Step(`^the parent chat answers that prompt against the child session$`, func() error {
				return runVitestScenario("src/ui/chat/SubagentPermissionCard.test.tsx",
					"a background subagent's prompt is answered in the parent chat against the child session")
			})
		},
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/subagents_web_ui.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("subagents web UI feature failed")
	}
}
