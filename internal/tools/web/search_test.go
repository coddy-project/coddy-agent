package web

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// stubEngines installs deterministic backends for the duration of a test and
// empties the cache, so one case cannot answer the next.
func stubEngines(t *testing.T) {
	t.Helper()
	oldBrave, oldBing, oldDDG, oldGoogle, oldSearx := braveSearchFunc, bingSearchFunc, ddgSearchFunc, googleSearchFunc, searxngSearchFunc
	t.Cleanup(func() {
		braveSearchFunc, bingSearchFunc = oldBrave, oldBing
		ddgSearchFunc, googleSearchFunc, searxngSearchFunc = oldDDG, oldGoogle, oldSearx
		searchCache.reset()
	})
	searchCache.reset()
	braveSearchFunc = func(context.Context, Query, Settings) ([]Result, error) { return nil, nil }
	bingSearchFunc = func(context.Context, Query, Settings) ([]Result, error) { return nil, nil }
	googleSearchFunc = func(context.Context, Query, Settings) ([]Result, error) { return nil, nil }
	searxngSearchFunc = func(context.Context, Query, Settings) ([]Result, error) { return nil, nil }
}

// envWith returns a tool environment carrying the given engine list, with the
// cache off so each test drives the backends it installed.
func envWith(engines ...string) *tooling.Env {
	return &tooling.Env{WebSearch: &tooling.WebSearchSettings{
		Engines:         engines,
		CacheTTLSeconds: -1,
	}}
}

func runSearch(t *testing.T, env *tooling.Env, args string) searchOutput {
	t.Helper()
	out, err := WebSearchTool().Execute(context.Background(), args, env)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	var parsed searchOutput
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	return parsed
}

func reportFor(t *testing.T, out searchOutput, engine string) Report {
	t.Helper()
	for _, r := range out.Engines {
		if r.Engine == engine {
			return r
		}
	}
	t.Fatalf("no report for engine %q in %+v", engine, out.Engines)
	return Report{}
}

func TestSearchMergesEnginesInConfiguredOrder(t *testing.T) {
	stubEngines(t)
	braveSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		return []Result{
			{Title: "Context package", URL: "https://pkg.go.dev/context", Snippet: "Package context."},
			{Title: "Go blog", URL: "https://go.dev/blog/context", Snippet: "Share by communicating."},
		}, nil
	}
	bingSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		return []Result{{Title: "Go on GitHub", URL: "https://github.com/golang/go", Snippet: "The Go source."}}, nil
	}

	out := runSearch(t, envWith("brave", "bing"), `{"query":"golang context cancellation"}`)
	if len(out.Results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(out.Results))
	}
	if out.Results[0].URL != "https://pkg.go.dev/context" {
		t.Errorf("first result: %q", out.Results[0].URL)
	}
	if out.Results[2].URL != "https://github.com/golang/go" {
		t.Errorf("last result: %q", out.Results[2].URL)
	}
	if out.Results[0].Source != "brave" || out.Results[2].Source != "bing" {
		t.Errorf("sources: %q, %q", out.Results[0].Source, out.Results[2].Source)
	}
	if got := reportFor(t, out, "brave"); got.Status != OutcomeOK || got.Results != 2 {
		t.Errorf("brave report: %+v", got)
	}
	if got := reportFor(t, out, "bing"); got.Status != OutcomeOK || got.Results != 1 {
		t.Errorf("bing report: %+v", got)
	}
}

func TestSearchDeduplicatesAcrossEngines(t *testing.T) {
	stubEngines(t)
	braveSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		return []Result{{Title: "Context package", URL: "https://pkg.go.dev/context", Snippet: "Package context."}}, nil
	}
	bingSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		// The same page, reached through www with a tracking parameter and a
		// trailing slash: one row, not two.
		return []Result{{Title: "Context package", URL: "https://www.pkg.go.dev/context/?utm_source=bing", Snippet: "Same."}}, nil
	}
	out := runSearch(t, envWith("brave", "bing"), `{"query":"golang context"}`)
	if len(out.Results) != 1 {
		t.Fatalf("expected 1 result after dedup, got %d: %+v", len(out.Results), out.Results)
	}
	if out.Results[0].URL != "https://pkg.go.dev/context" {
		t.Errorf("kept the wrong copy: %q", out.Results[0].URL)
	}
	// The row Bing supplied was already present, so it contributed nothing.
	if got := reportFor(t, out, "bing"); got.Results != 0 {
		t.Errorf("bing should report 0 contributed rows, got %d", got.Results)
	}
}

func TestSearchNamesABlockedEngineInsteadOfCountingItAsEmpty(t *testing.T) {
	stubEngines(t)
	braveSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		return []Result{{Title: "Context package", URL: "https://pkg.go.dev/context"}}, nil
	}
	bingSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		return nil, blocked("anti-bot interstitial")
	}
	out := runSearch(t, envWith("brave", "bing"), `{"query":"golang context"}`)
	if len(out.Results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(out.Results))
	}
	rep := reportFor(t, out, "bing")
	if rep.Status != OutcomeBlocked {
		t.Fatalf("bing status: %q", rep.Status)
	}
	if !strings.Contains(rep.Reason, "anti-bot interstitial") {
		t.Errorf("bing reason: %q", rep.Reason)
	}
	if !strings.Contains(out.Hint, "bing") {
		t.Errorf("hint should name the unavailable engine: %q", out.Hint)
	}
}

func TestSearchDiscardsAnEngineAnsweringADifferentSubject(t *testing.T) {
	stubEngines(t)
	braveSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		return []Result{{Title: "Context package", URL: "https://pkg.go.dev/context"}}, nil
	}
	// The decoy Bing was measured serving: ten well-formed rows about an
	// entirely different subject, with a title that echoes the query.
	bingSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		return []Result{
			{Title: "Explorateur de fichiers Windows", URL: "https://support.microsoft.com/fr/1", Snippet: "Ouvrir l'explorateur."},
			{Title: "Reparer l'Explorateur de fichiers", URL: "https://support.microsoft.com/fr/2", Snippet: "Si l'explorateur ne demarre pas."},
			{Title: "Visit Rainier Official Site", URL: "https://visitrainier.com/", Snippet: "Mount Rainier tourism."},
			{Title: "Les routes panoramiques", URL: "https://visitrainier.com/drives", Snippet: "Itineraires."},
			{Title: "Ou dormir pres de la montagne", URL: "https://visitrainier.com/lodging", Snippet: "Hotels et chalets."},
		}, nil
	}
	out := runSearch(t, envWith("brave", "bing"), `{"query":"golang context cancellation"}`)
	if len(out.Results) != 1 {
		t.Fatalf("decoy rows reached the answer: %+v", out.Results)
	}
	rep := reportFor(t, out, "bing")
	if rep.Status != OutcomeBlocked || !strings.Contains(rep.Reason, "decoy") {
		t.Fatalf("bing report: %+v", rep)
	}
}

func TestSearchFailsWhenEveryEngineIsBlocked(t *testing.T) {
	stubEngines(t)
	braveSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		return nil, blocked("http 403")
	}
	bingSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		return nil, blocked("anti-bot interstitial")
	}
	_, err := WebSearchTool().Execute(context.Background(), `{"query":"golang context"}`, envWith("brave", "bing"))
	if err == nil {
		t.Fatal("expected an error when every engine is blocked, got an answer")
	}
	for _, want := range []string{"brave", "http 403", "bing", "anti-bot interstitial"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

func TestSearchSucceedsEmptyWhenEnginesGenuinelyFoundNothing(t *testing.T) {
	stubEngines(t)
	out := runSearch(t, envWith("brave", "bing"), `{"query":"zzqx nonexistent phrase"}`)
	if len(out.Results) != 0 {
		t.Fatalf("expected no results, got %d", len(out.Results))
	}
	if got := reportFor(t, out, "brave"); got.Status != OutcomeEmpty {
		t.Errorf("brave status: %q", got.Status)
	}
	if !strings.Contains(out.Hint, "different wording") {
		t.Errorf("hint should push a reword: %q", out.Hint)
	}
}

func TestSearchAnErrorFromOneEngineDoesNotSinkTheCall(t *testing.T) {
	stubEngines(t)
	braveSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		return nil, fmt.Errorf("dial tcp: connection refused")
	}
	bingSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		return []Result{{Title: "Context package", URL: "https://pkg.go.dev/context"}}, nil
	}
	out := runSearch(t, envWith("brave", "bing"), `{"query":"golang context"}`)
	if len(out.Results) != 1 {
		t.Fatalf("expected the healthy engine's row, got %d", len(out.Results))
	}
	if got := reportFor(t, out, "brave"); got.Status != OutcomeError {
		t.Errorf("brave status: %q", got.Status)
	}
}

func TestSearchCapsResultsAndClipsSnippets(t *testing.T) {
	stubEngines(t)
	braveSearchFunc = func(_ context.Context, q Query, _ Settings) ([]Result, error) {
		rows := make([]Result, 0, 30)
		for i := 0; i < 30; i++ {
			rows = append(rows, Result{
				Title:   fmt.Sprintf("golang result %d", i),
				URL:     fmt.Sprintf("https://example.com/golang/%d", i),
				Snippet: strings.Repeat("golang ", 200),
			})
		}
		return rows, nil
	}
	out := runSearch(t, envWith("brave"), `{"query":"golang","max_results":5}`)
	if len(out.Results) != 5 {
		t.Fatalf("expected 5 results, got %d", len(out.Results))
	}
	for _, r := range out.Results {
		if len([]rune(r.Description)) > DefaultSnippetChars+4 {
			t.Fatalf("snippet not clipped: %d chars", len([]rune(r.Description)))
		}
	}
	if !strings.Contains(out.Hint, "page incremented") {
		t.Errorf("hint should offer pagination: %q", out.Hint)
	}
}

func TestSearchPassesSiteRestrictionToTheEngines(t *testing.T) {
	stubEngines(t)
	var seen Query
	braveSearchFunc = func(_ context.Context, q Query, _ Settings) ([]Result, error) {
		seen = q
		return []Result{{Title: "Go", URL: "https://go.dev/doc"}}, nil
	}
	runSearch(t, envWith("brave"), `{"query":"context cancellation","site":"https://go.dev/"}`)
	if seen.Site != "https://go.dev/" {
		t.Fatalf("site not forwarded: %q", seen.Site)
	}
	if got := engineQuery(seen); got != "context cancellation site:go.dev" {
		t.Errorf("engine query: %q", got)
	}
}

func TestSearchRefusesAnEmptyQuery(t *testing.T) {
	stubEngines(t)
	if _, err := WebSearchTool().Execute(context.Background(), `{"query":"   "}`, envWith("brave")); err == nil {
		t.Fatal("expected an error for a blank query")
	}
}

func TestSearchReportsAnUnknownEngineRatherThanSkippingIt(t *testing.T) {
	stubEngines(t)
	braveSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		return []Result{{Title: "Go", URL: "https://go.dev"}}, nil
	}
	out := runSearch(t, envWith("brave", "nosuchengine"), `{"query":"go"}`)
	if got := reportFor(t, out, "nosuchengine"); got.Status != OutcomeError {
		t.Errorf("unknown engine should be reported as an error, got %+v", got)
	}
}

func TestSearchWithoutSettingsUsesTheDefaultEngines(t *testing.T) {
	stubEngines(t)
	braveSearchFunc = func(context.Context, Query, Settings) ([]Result, error) {
		return []Result{{Title: "Go", URL: "https://go.dev"}}, nil
	}
	out := runSearch(t, &tooling.Env{}, `{"query":"go"}`)
	names := make([]string, 0, len(out.Engines))
	for _, r := range out.Engines {
		names = append(names, r.Engine)
	}
	if strings.Join(names, ",") != strings.Join(DefaultEngines(), ",") {
		t.Fatalf("default engines: %v, want %v", names, DefaultEngines())
	}
}
