package web

// Godog harness for features/websearch_engines.feature: the websearch tool runs
// against the real orchestrator with scripted backends, so the scenarios
// exercise merging, deduplication, the relevance gate and the reporting of a
// blocked engine without a live search.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

type webSearchFeatureState struct {
	engines []string
	scripts map[string]func(context.Context, Query, Settings) ([]Result, error)
	out     searchOutput
	raw     string
	runErr  error
}

func (s *webSearchFeatureState) reset() {
	s.engines = nil
	s.scripts = map[string]func(context.Context, Query, Settings) ([]Result, error){}
	s.out = searchOutput{}
	s.raw = ""
	s.runErr = nil
	searchCache.reset()
}

// install points every engine seam at this scenario's script, so an engine the
// scenario did not describe answers with nothing rather than reaching the web.
func (s *webSearchFeatureState) install() {
	pick := func(name string) func(context.Context, Query, Settings) ([]Result, error) {
		if fn, ok := s.scripts[name]; ok {
			return fn
		}
		return func(context.Context, Query, Settings) ([]Result, error) { return nil, nil }
	}
	braveSearchFunc = pick(EngineBrave)
	bingSearchFunc = pick(EngineBing)
	googleSearchFunc = pick(EngineGoogle)
	searxngSearchFunc = pick(EngineSearXNG)
}

func (s *webSearchFeatureState) theOperatorConfiguredTheSearchEngines(list string) error {
	for _, e := range strings.Split(list, ",") {
		if e = strings.TrimSpace(e); e != "" {
			s.engines = append(s.engines, e)
		}
	}
	if len(s.engines) == 0 {
		return fmt.Errorf("no engines in %q", list)
	}
	return nil
}

func (s *webSearchFeatureState) engineAnswersWith(engine string, table *godog.Table) error {
	rows := make([]Result, 0, len(table.Rows))
	if len(table.Rows) == 0 {
		return fmt.Errorf("empty table for engine %q", engine)
	}
	head := table.Rows[0].Cells
	for _, r := range table.Rows[1:] {
		var out Result
		for i, cell := range r.Cells {
			switch head[i].Value {
			case "title":
				out.Title = cell.Value
			case "url":
				out.URL = cell.Value
			case "snippet":
				out.Snippet = cell.Value
			default:
				return fmt.Errorf("unknown column %q", head[i].Value)
			}
		}
		rows = append(rows, out)
	}
	s.scripts[engine] = func(context.Context, Query, Settings) ([]Result, error) { return rows, nil }
	return nil
}

func (s *webSearchFeatureState) engineAnswersWithNoResults(engine string) error {
	s.scripts[engine] = func(context.Context, Query, Settings) ([]Result, error) { return nil, nil }
	return nil
}

func (s *webSearchFeatureState) engineIsBlockedWith(engine, reason string) error {
	s.scripts[engine] = func(context.Context, Query, Settings) ([]Result, error) {
		return nil, blocked("%s", reason)
	}
	return nil
}

func (s *webSearchFeatureState) theAgentSearchesTheWebFor(query string) error {
	s.install()
	args, err := json.Marshal(map[string]interface{}{"query": query})
	if err != nil {
		return err
	}
	env := &tooling.Env{WebSearch: &tooling.WebSearchSettings{
		Engines:         s.engines,
		CacheTTLSeconds: -1,
	}}
	s.raw, s.runErr = WebSearchTool().Execute(context.Background(), string(args), env)
	if s.runErr != nil {
		return nil
	}
	return json.Unmarshal([]byte(s.raw), &s.out)
}

func (s *webSearchFeatureState) theSearchReturnsNResults(n int) error {
	if s.runErr != nil {
		return fmt.Errorf("search failed: %w", s.runErr)
	}
	if len(s.out.Results) != n {
		return fmt.Errorf("got %d results, want %d: %s", len(s.out.Results), n, s.raw)
	}
	return nil
}

func (s *webSearchFeatureState) resultNIs(n int, url string) error {
	if n < 1 || n > len(s.out.Results) {
		return fmt.Errorf("no result %d in %d results", n, len(s.out.Results))
	}
	if got := s.out.Results[n-1].URL; got != url {
		return fmt.Errorf("result %d is %q, want %q", n, got, url)
	}
	return nil
}

func (s *webSearchFeatureState) report(engine string) (Report, error) {
	for _, r := range s.out.Engines {
		if r.Engine == engine {
			return r, nil
		}
	}
	return Report{}, fmt.Errorf("no report for engine %q in %s", engine, s.raw)
}

func (s *webSearchFeatureState) engineIsReportedAsWithNResults(engine, status string, n int) error {
	rep, err := s.report(engine)
	if err != nil {
		return err
	}
	if string(rep.Status) != status {
		return fmt.Errorf("engine %q is %q, want %q", engine, rep.Status, status)
	}
	if rep.Results != n {
		return fmt.Errorf("engine %q contributed %d results, want %d", engine, rep.Results, n)
	}
	return nil
}

func (s *webSearchFeatureState) engineIsReportedAs(engine, status string) error {
	rep, err := s.report(engine)
	if err != nil {
		return err
	}
	if string(rep.Status) != status {
		return fmt.Errorf("engine %q is %q, want %q", engine, rep.Status, status)
	}
	return nil
}

func (s *webSearchFeatureState) engineIsReportedAsBecauseOf(engine, status, reason string) error {
	if err := s.engineIsReportedAs(engine, status); err != nil {
		return err
	}
	rep, _ := s.report(engine)
	if !strings.Contains(rep.Reason, reason) {
		return fmt.Errorf("engine %q reason is %q, want it to mention %q", engine, rep.Reason, reason)
	}
	return nil
}

func (s *webSearchFeatureState) theSearchFails() error {
	if s.runErr == nil {
		return fmt.Errorf("expected a failure, got: %s", s.raw)
	}
	return nil
}

func (s *webSearchFeatureState) theSearchSucceeds() error {
	if s.runErr != nil {
		return fmt.Errorf("expected success, got: %w", s.runErr)
	}
	return nil
}

func (s *webSearchFeatureState) theFailureNamesEngineAndItsReason(engine, reason string) error {
	if s.runErr == nil {
		return fmt.Errorf("the search did not fail")
	}
	msg := s.runErr.Error()
	if !strings.Contains(msg, engine) || !strings.Contains(msg, reason) {
		return fmt.Errorf("failure %q should name %q and %q", msg, engine, reason)
	}
	return nil
}

func initializeWebSearchScenario(sc *godog.ScenarioContext) {
	s := &webSearchFeatureState{}
	oldBrave, oldBing, oldGoogle, oldSearx := braveSearchFunc, bingSearchFunc, googleSearchFunc, searxngSearchFunc
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return ctx, nil
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		braveSearchFunc, bingSearchFunc = oldBrave, oldBing
		googleSearchFunc, searxngSearchFunc = oldGoogle, oldSearx
		searchCache.reset()
		return ctx, nil
	})
	sc.Step(`^the operator configured the search engines "([^"]*)"$`, s.theOperatorConfiguredTheSearchEngines)
	sc.Step(`^engine "([^"]*)" answers with:$`, s.engineAnswersWith)
	sc.Step(`^engine "([^"]*)" answers with no results$`, s.engineAnswersWithNoResults)
	sc.Step(`^engine "([^"]*)" is blocked with "([^"]*)"$`, s.engineIsBlockedWith)
	sc.Step(`^the agent searches the web for "([^"]*)"$`, s.theAgentSearchesTheWebFor)
	sc.Step(`^the search returns (\d+) results?$`, s.theSearchReturnsNResults)
	sc.Step(`^result (\d+) is "([^"]*)"$`, s.resultNIs)
	sc.Step(`^engine "([^"]*)" is reported as "([^"]*)" with (\d+) results?$`, s.engineIsReportedAsWithNResults)
	sc.Step(`^engine "([^"]*)" is reported as "([^"]*)" because of "([^"]*)"$`, s.engineIsReportedAsBecauseOf)
	sc.Step(`^engine "([^"]*)" is reported as "([^"]*)"$`, s.engineIsReportedAs)
	sc.Step(`^the search fails$`, s.theSearchFails)
	sc.Step(`^the search succeeds$`, s.theSearchSucceeds)
	sc.Step(`^the failure names engine "([^"]*)" and its reason "([^"]*)"$`, s.theFailureNamesEngineAndItsReason)
}

func TestWebSearchEnginesFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "websearch-engines",
		ScenarioInitializer: initializeWebSearchScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/websearch_engines.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("websearch engines feature suite failed")
	}
}
