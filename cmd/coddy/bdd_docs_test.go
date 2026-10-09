package main

// Godog harness for the @cli scenario of features/builtin_docs.feature:
// `coddy docs` run in process against the documentation embedded in the test
// binary, its output captured.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

type docsCLIState struct {
	out bytes.Buffer
	err error
}

// runs runs a coddy docs command in a terminal whose locale is English,
// whatever the machine running the test speaks.
func (s *docsCLIState) runs(command string) error {
	return s.runsWithLang(command, "en_US.UTF-8")
}

// runsWithLang runs a coddy docs command with LANG set to lang and the other
// locale variables, Coddy's own override included, unset.
func (s *docsCLIState) runsWithLang(command, lang string) error {
	fields := strings.Fields(command)
	if len(fields) < 2 || fields[0] != "coddy" || fields[1] != "docs" {
		return fmt.Errorf("not a coddy docs command: %q", command)
	}
	restore := setLocaleEnv(map[string]string{"LANG": lang, "LC_ALL": "", "LC_MESSAGES": "", "CODDY_LANG": ""})
	defer restore()
	s.out.Reset()
	s.err = runDocs(fields[2:], &s.out)
	return nil
}

// setLocaleEnv sets (or, for an empty value, unsets) environment variables
// and returns what puts them back.
func setLocaleEnv(vars map[string]string) func() {
	type saved struct {
		value string
		set   bool
	}
	old := map[string]saved{}
	for k, v := range vars {
		prev, ok := os.LookupEnv(k)
		old[k] = saved{prev, ok}
		if v == "" {
			_ = os.Unsetenv(k)
		} else {
			_ = os.Setenv(k, v)
		}
	}
	return func() {
		for k, v := range old {
			if v.set {
				_ = os.Setenv(k, v.value)
			} else {
				_ = os.Unsetenv(k)
			}
		}
	}
}

func (s *docsCLIState) outputStartsWith(prefix string) error {
	if s.err != nil {
		return fmt.Errorf("coddy docs failed: %v", s.err)
	}
	if !strings.HasPrefix(s.out.String(), prefix) {
		return fmt.Errorf("output does not start with %q:\n%.400s", prefix, s.out.String())
	}
	return nil
}

func (s *docsCLIState) outputLists(slug string) error {
	if s.err != nil {
		return fmt.Errorf("coddy docs failed: %v", s.err)
	}
	if !strings.Contains(s.out.String(), " "+slug) {
		return fmt.Errorf("output does not list %s:\n%s", slug, s.out.String())
	}
	return nil
}

// toolsPointAtAPageWith checks the model-facing description of the reader
// names the mention form before any address, so the agent sends a user to the
// documentation built into the binary rather than to the site.
func (s *docsCLIState) toolsPointAtAPageWith(form string) error {
	desc := docsToolDescriptions()[docsReadToolName()]
	mention := strings.Index(desc, form)
	if mention < 0 {
		return fmt.Errorf("the reader's description never names %q", form)
	}
	if site := strings.Index(desc, "https://"); site >= 0 && site < mention {
		return fmt.Errorf("an address comes before %q in the reader's description", form)
	}
	return nil
}

// everyNamedCommandIsAccepted runs each `coddy docs <verb>` spelling the two
// tool descriptions teach and fails on one the binary answers with its usage.
func (s *docsCLIState) everyNamedCommandIsAccepted() error {
	accepted := map[string]bool{}
	for _, v := range docsVerbs {
		accepted[v] = true
	}
	for name, desc := range docsToolDescriptions() {
		for _, m := range docsCommandRE.FindAllStringSubmatch(desc, -1) {
			if !accepted[m[1]] {
				return fmt.Errorf("%s names %q, but coddy docs accepts only %v", name, m[0], docsVerbs)
			}
		}
	}
	return nil
}

func initializeDocsCLIScenario(sc *godog.ScenarioContext) {
	s := &docsCLIState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.out.Reset()
		s.err = nil
		return ctx, nil
	})
	sc.Step(`^the operator runs "([^"]*)"$`, s.runs)
	sc.Step(`^the operator runs "([^"]*)" in a terminal with LANG "([^"]*)"$`, s.runsWithLang)
	sc.Step(`^the output starts with "([^"]*)"$`, s.outputStartsWith)
	sc.Step(`^the output lists "([^"]*)"$`, s.outputLists)
	sc.Step(`^the documentation tools point at a page with "([^"]*)" before any address$`, s.toolsPointAtAPageWith)
	sc.Step(`^every "coddy docs" command they name is one the binary accepts$`, s.everyNamedCommandIsAccepted)
}

func TestBuiltinDocsCLIFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "builtin-docs-cli",
		ScenarioInitializer: initializeDocsCLIScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/builtin_docs.feature"},
			Tags:     "@cli",
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("builtin docs cli feature suite failed")
	}
}
