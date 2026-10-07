//go:build http && ui

package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

// runVitestScenario fails unless the Vitest test called name, in file, ran
// and passed; that is where the React component is actually rendered against
// a stubbed API. The file runs once per test binary with Vitest's JSON report,
// and every step naming one of its tests reads that report: a feature costs
// one Vitest start per file it names, not one per test. The http,ui test
// matrix installs the dependencies through make ui-build.
func runVitestScenario(file, name string) error {
	v, _ := vitestFileRuns.LoadOrStore(file, &vitestFileRun{})
	run := v.(*vitestFileRun)
	run.once.Do(func() { run.report, run.err = runVitestFile(file) })
	if run.err != nil {
		return run.err
	}
	return run.report.passed(file, name)
}

// vitestFileRun is the one run of a test file.
type vitestFileRun struct {
	once   sync.Once
	report vitestReport
	err    error
}

// vitestFileRuns holds a *vitestFileRun per test file.
var vitestFileRuns sync.Map

// vitestReport is what Vitest's JSON reporter says about one run: every test
// by its full name (its describe blocks and its own name, joined by spaces,
// which is what a step names), and the messages of a file that failed as a
// whole, such as one that did not compile.
type vitestReport struct {
	tests    map[string][]vitestTestResult
	messages []string
}

type vitestTestResult struct {
	Status          string   `json:"status"`
	FailureMessages []string `json:"failureMessages"`
}

// passed fails unless the report holds a test called name and every test of
// that name passed. A name the report lacks is a step pointing at a renamed
// or mistyped test, which a pattern matching nothing would let through.
func (r vitestReport) passed(file, name string) error {
	results := r.tests[name]
	if len(results) == 0 {
		names := make([]string, 0, len(r.tests))
		for n := range r.tests {
			names = append(names, n)
		}
		sort.Strings(names)
		return fmt.Errorf("%s: no test named %q ran and passed\n%s\nthe file has:\n  %s",
			file, name, strings.Join(r.messages, "\n"), strings.Join(names, "\n  "))
	}
	for _, res := range results {
		if res.Status != "passed" {
			return fmt.Errorf("%s: %q %s\n%s", file, name, res.Status, strings.Join(res.FailureMessages, "\n"))
		}
	}
	return nil
}

// parseVitestReport reads the output of Vitest's JSON reporter.
func parseVitestReport(data []byte) (vitestReport, error) {
	var raw struct {
		TestResults []struct {
			Message          string `json:"message"`
			AssertionResults []struct {
				FullName string `json:"fullName"`
				vitestTestResult
			} `json:"assertionResults"`
		} `json:"testResults"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return vitestReport{}, err
	}
	r := vitestReport{tests: map[string][]vitestTestResult{}}
	for _, f := range raw.TestResults {
		if f.Message != "" {
			r.messages = append(r.messages, f.Message)
		}
		for _, a := range f.AssertionResults {
			r.tests[a.FullName] = append(r.tests[a.FullName], a.vitestTestResult)
		}
	}
	return r, nil
}

// runVitestFile runs every test of file once, the JSON report written to a
// temporary file. Failing tests are not an error here: each step asks about
// its own.
func runVitestFile(file string) (vitestReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	dir, err := os.MkdirTemp("", "coddy-vitest-")
	if err != nil {
		return vitestReport{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	reportPath := filepath.Join(dir, "report.json")
	cmd := exec.CommandContext(ctx, "node", "node_modules/vitest/vitest.mjs", "run", file,
		"--reporter=json", "--outputFile="+reportPath)
	out, runErr := cmd.CombinedOutput()
	data, err := os.ReadFile(reportPath) //nolint:gosec // a path this function made
	if err != nil {
		return vitestReport{}, fmt.Errorf("%s: vitest wrote no report (%v): %w\n%s", file, runErr, err, out)
	}
	report, err := parseVitestReport(data)
	if err != nil {
		return vitestReport{}, fmt.Errorf("%s: vitest report: %w\n%s", file, err, out)
	}
	return report, nil
}

func TestParseVitestReport(t *testing.T) {
	const data = `{"testResults":[
	  {"message":"","assertionResults":[
	    {"fullName":"menu opens","status":"passed","failureMessages":[]},
	    {"fullName":"menu closes on Escape","status":"failed","failureMessages":["expected true to be false"]},
	    {"fullName":"menu skipped one","status":"skipped","failureMessages":[]},
	    {"fullName":"menu twice","status":"passed","failureMessages":[]},
	    {"fullName":"menu twice","status":"failed","failureMessages":["the second one broke"]}]},
	  {"message":"SyntaxError: Unexpected token","assertionResults":[]}]}`
	report, err := parseVitestReport([]byte(data))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, want string // want: "" for a pass, else a fragment of the error
	}{
		{"menu opens", ""},
		{"menu closes on Escape", "expected true to be false"},
		{"menu skipped one", "skipped"},
		{"menu twice", "the second one broke"},
		{"menu renamed", "no test named"},
	}
	for _, c := range cases {
		err := report.passed("src/x.test.tsx", c.name)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: err = %v, want one saying %q", c.name, err, c.want)
		case err != nil && (!strings.Contains(err.Error(), "src/x.test.tsx") || !strings.Contains(err.Error(), `"`+c.name+`"`)):
			t.Errorf("%s: error does not name the file and the test: %v", c.name, err)
		}
	}
	if err := report.passed("src/x.test.tsx", "menu renamed"); !strings.Contains(err.Error(), "SyntaxError") ||
		!strings.Contains(err.Error(), "menu opens") {
		t.Errorf("a missing test does not show the file's failure and its tests: %v", err)
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
