package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestWebSearchDefaultsWhenNothingIsConfigured(t *testing.T) {
	var w ToolWebSearch
	got := w.ToolSettings()
	if !reflect.DeepEqual(got.Engines, WebSearchDefaultEngines()) {
		t.Errorf("engines: %v, want %v", got.Engines, WebSearchDefaultEngines())
	}
	if got.EngineTimeoutSeconds != WebSearchDefaultEngineTimeoutSeconds {
		t.Errorf("engine timeout: %d", got.EngineTimeoutSeconds)
	}
	if got.TotalTimeoutSeconds != WebSearchDefaultTotalTimeoutSeconds {
		t.Errorf("total timeout: %d", got.TotalTimeoutSeconds)
	}
	if got.SnippetChars != WebSearchDefaultSnippetChars {
		t.Errorf("snippet chars: %d", got.SnippetChars)
	}
}

// TestWebSearchDefaultEnginesExcludeTheMeasuredDeadOnes pins the decision:
// DuckDuckGo answers a server with an anti-bot interstitial and Google renders
// its results in the browser, so neither is asked unless an operator says so.
func TestWebSearchDefaultEnginesExcludeTheMeasuredDeadOnes(t *testing.T) {
	for _, e := range WebSearchDefaultEngines() {
		if e == WebSearchEngineDDG || e == WebSearchEngineGoogle {
			t.Fatalf("%q must not be a default engine", e)
		}
	}
	if WebSearchDefaultEngines()[0] != WebSearchEngineBrave {
		t.Errorf("brave should lead the merge order, got %v", WebSearchDefaultEngines())
	}
}

func TestWebSearchResolvedEnginesDropsBlanksAndRepeats(t *testing.T) {
	w := ToolWebSearch{Engines: []string{" Brave ", "bing", "brave", "", "BING"}}
	got := w.ResolvedEngines()
	if !reflect.DeepEqual(got, []string{"brave", "bing"}) {
		t.Errorf("got %v", got)
	}
}

func TestWebSearchRejectsAnUnknownEngine(t *testing.T) {
	w := ToolWebSearch{Engines: []string{"brave", "yahoo"}}
	err := w.validate()
	if err == nil {
		t.Fatal("expected an error for an unknown engine")
	}
	if !strings.Contains(err.Error(), "yahoo") || !strings.Contains(err.Error(), "brave") {
		t.Errorf("error should name the bad value and the known ones: %v", err)
	}
}

func TestWebSearchRejectsSearXNGWithoutAnAddress(t *testing.T) {
	w := ToolWebSearch{Engines: []string{"searxng"}}
	if err := w.validate(); err == nil {
		t.Fatal("an engine that cannot be reached is a configuration error")
	}
}

func TestWebSearchAcceptsSearXNGWithAnAddress(t *testing.T) {
	w := ToolWebSearch{Engines: []string{"searxng"}, SearXNGURL: "http://localhost:8080"}
	if err := w.validate(); err != nil {
		t.Fatalf("a self-hosted instance must be accepted: %v", err)
	}
}

// TestSearXNGURLAllowsPrivateAddresses is the decision the reviewers split on.
// The address comes from the operator's own configuration, not from the model,
// and a self-hosted SearXNG normally listens on localhost or a LAN address -
// the guard webfetch applies to a model-chosen URL would refuse exactly the
// deployments this engine exists for.
func TestSearXNGURLAllowsPrivateAddresses(t *testing.T) {
	for _, raw := range []string{
		"http://localhost:8080",
		"http://127.0.0.1:8888",
		"http://192.168.1.10:8080",
		"http://10.0.0.5",
		"https://searx.internal.example",
	} {
		if err := validateSearXNGURL(raw); err != nil {
			t.Errorf("%q should be allowed: %v", raw, err)
		}
	}
}

// TestSearXNGURLRefusesTheMetadataRange keeps the one target where a mistyped
// or model-written value turns a search into a credential read. No SearXNG
// listens there.
func TestSearXNGURLRefusesTheMetadataRange(t *testing.T) {
	for _, raw := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://169.254.170.2/",
	} {
		if err := validateSearXNGURL(raw); err == nil {
			t.Errorf("%q should be refused", raw)
		}
	}
}

func TestSearXNGURLRefusesANonHTTPScheme(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "gopher://example.com", "ftp://example.com"} {
		if err := validateSearXNGURL(raw); err == nil {
			t.Errorf("%q should be refused", raw)
		}
	}
}

func TestWebSearchRejectsNegativeBounds(t *testing.T) {
	for _, w := range []ToolWebSearch{
		{EngineTimeoutSeconds: -1},
		{TotalTimeoutSeconds: -1},
		{MaxConcurrentEngines: -1},
		{SnippetChars: -1},
	} {
		if err := w.validate(); err == nil {
			t.Errorf("expected an error for %+v", w)
		}
	}
}

// TestWebSearchNegativeCacheTTLTurnsCachingOff: unlike the bounds above, a
// negative cache TTL is meaningful rather than invalid.
func TestWebSearchNegativeCacheTTLTurnsCachingOff(t *testing.T) {
	w := ToolWebSearch{CacheTTLSeconds: -1}
	if err := w.validate(); err != nil {
		t.Fatalf("a negative TTL means off, not invalid: %v", err)
	}
	if got := w.ToolSettings().CacheTTLSeconds; got != -1 {
		t.Errorf("cache ttl: %d", got)
	}
}

func TestWebSearchValidateLowercasesEngineNames(t *testing.T) {
	w := ToolWebSearch{Engines: []string{"BRAVE", " Bing "}}
	if err := w.validate(); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(w.Engines, []string{"brave", "bing"}) {
		t.Errorf("got %v", w.Engines)
	}
}
