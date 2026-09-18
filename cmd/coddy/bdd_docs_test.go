package main

// Godog harness for the @cli scenario of features/builtin_docs.feature:
// `coddy docs` run in process against the documentation embedded in the test
// binary, its output captured.

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

type docsCLIState struct {
	out bytes.Buffer
	err error
}

func (s *docsCLIState) runs(command string) error {
	fields := strings.Fields(command)
	if len(fields) < 2 || fields[0] != "coddy" || fields[1] != "docs" {
		return fmt.Errorf("not a coddy docs command: %q", command)
	}
	s.out.Reset()
	s.err = runDocs(fields[2:], &s.out)
	return nil
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

func initializeDocsCLIScenario(sc *godog.ScenarioContext) {
	s := &docsCLIState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.out.Reset()
		s.err = nil
		return ctx, nil
	})
	sc.Step(`^the operator runs "([^"]*)"$`, s.runs)
	sc.Step(`^the output starts with "([^"]*)"$`, s.outputStartsWith)
	sc.Step(`^the output lists "([^"]*)"$`, s.outputLists)
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
