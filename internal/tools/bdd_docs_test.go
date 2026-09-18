package tools_test

// Godog harness for the @tools scenarios of features/builtin_docs.feature:
// coddy_docs_search and coddy_docs_read run through the real registry the
// agent calls, over the documentation embedded in this test binary.

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	apptools "github.com/EvilFreelancer/coddy-agent/internal/tools"
)

type docsToolsState struct {
	out    string
	err    error
	offset int
	first  string
}

func (s *docsToolsState) run(tool, args string) {
	r := apptools.NewRegistry()
	s.out, s.err = r.Execute(context.Background(), tool, args, &apptools.Env{CWD: "/tmp", SessionID: "sess-bdd-docs"})
}

func (s *docsToolsState) failed() error {
	if s.err != nil {
		return fmt.Errorf("tool failed: %v", s.err)
	}
	return nil
}

func (s *docsToolsState) searches(query string) error {
	s.run(apptools.ToolDocsSearch, fmt.Sprintf(`{"query":%q}`, query))
	return nil
}

func (s *docsToolsState) resultPointsAt(ref string) error {
	if err := s.failed(); err != nil {
		return err
	}
	if !strings.Contains(s.out, " "+ref+"  (") {
		return fmt.Errorf("no result points at %s:\n%s", ref, s.out)
	}
	return nil
}

func (s *docsToolsState) reads(ref string) error {
	s.run(apptools.ToolDocsRead, fmt.Sprintf(`{"page":%q}`, ref))
	return nil
}

func (s *docsToolsState) getsSection(heading, page string) error {
	if err := s.failed(); err != nil {
		return err
	}
	headingLine := regexp.MustCompile(`\n#{2,6} ` + regexp.QuoteMeta(heading) + `\n`)
	if !strings.Contains(s.out, "] "+page+" > "+heading+"\n") || !headingLine.MatchString(s.out) {
		return fmt.Errorf("want the section %q of %q:\n%s", heading, page, s.out)
	}
	return nil
}

var continueRE = regexp.MustCompile(`offset=(\d+)`)

func (s *docsToolsState) getsBeginningWithSections() error {
	if err := s.failed(); err != nil {
		return err
	}
	if !strings.Contains(s.out, "lines 1-") || !strings.Contains(s.out, "# Coddy embedded UI specification") {
		return fmt.Errorf("not the beginning of the page:\n%.600s", s.out)
	}
	if !strings.Contains(s.out, "#sessions  Sessions") {
		return fmt.Errorf("the reading does not list the sections of the page:\n%s", s.out[max(0, len(s.out)-3000):])
	}
	m := continueRE.FindStringSubmatch(s.out)
	if m == nil {
		return fmt.Errorf("no offset to continue at:\n%s", s.out[max(0, len(s.out)-1500):])
	}
	s.offset, _ = strconv.Atoi(m[1])
	s.first = s.out
	return nil
}

func (s *docsToolsState) continuesFrom(ref string) error {
	s.run(apptools.ToolDocsRead, fmt.Sprintf(`{"page":%q,"offset":%d}`, ref, s.offset))
	return nil
}

func (s *docsToolsState) getsNextPart() error {
	if err := s.failed(); err != nil {
		return err
	}
	want := fmt.Sprintf("lines %d-", s.offset)
	if !strings.Contains(s.out, want) || s.out == s.first {
		return fmt.Errorf("want a reading starting at line %d:\n%.600s", s.offset, s.out)
	}
	return nil
}

func (s *docsToolsState) readsContents() error {
	s.run(apptools.ToolDocsRead, `{}`)
	return nil
}

func (s *docsToolsState) getsContentsWith(slug string) error {
	if err := s.failed(); err != nil {
		return err
	}
	if !strings.Contains(s.out, "- "+slug+" - ") || !strings.Contains(s.out, "## Features") {
		return fmt.Errorf("the contents do not list %s:\n%.800s", slug, s.out)
	}
	return nil
}

func initializeDocsToolsScenario(sc *godog.ScenarioContext) {
	s := &docsToolsState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = docsToolsState{}
		return ctx, nil
	})
	sc.Step(`^the agent searches its documentation for "([^"]*)"$`, s.searches)
	sc.Step(`^a result points at the section "([^"]*)"$`, s.resultPointsAt)
	sc.Step(`^the agent reads "([^"]*)"$`, s.reads)
	sc.Step(`^it gets the section "([^"]*)" of the page "([^"]*)"$`, s.getsSection)
	sc.Step(`^it gets the beginning of the page with the list of its sections and the offset to continue at$`, s.getsBeginningWithSections)
	sc.Step(`^the agent continues reading "([^"]*)" from that offset$`, s.continuesFrom)
	sc.Step(`^it gets the next part of the page$`, s.getsNextPart)
	sc.Step(`^the agent reads its documentation without naming a page$`, s.readsContents)
	sc.Step(`^it gets the contents with the page "([^"]*)" and its summary$`, s.getsContentsWith)
}

func TestBuiltinDocsToolsFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "builtin-docs-tools",
		ScenarioInitializer: initializeDocsToolsScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/builtin_docs.feature"},
			Tags:     "@tools",
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("builtin docs tools feature suite failed")
	}
}
