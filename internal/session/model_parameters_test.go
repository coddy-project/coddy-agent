package session

import (
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The listing a provider row is read for its context windows also names the
// request parameters each model takes (supported_parameters on OpenRouter-style
// aggregators). The manager keeps them next to the windows, and a request reads
// them to choose between reasoning_effort and the reasoning object (issue #258).

var aggregatorListedParams = []string{"include_reasoning", "max_tokens", "reasoning", "tools"}

func modelParamsTestConfig(home string) *config.Config {
	cfg := windowTestConfig(home)
	// A window of its own, so only its reasoning levels make the listing worth reading.
	cfg.Models = append(cfg.Models, config.ModelEntry{Model: "hub/qwen/qwen3.8-flash", MaxContextTokens: 1000000})
	return cfg
}

func TestModelParametersComeFromTheProviderListing(t *testing.T) {
	cfg := modelParamsTestConfig(t.TempDir())
	listing := &windowListing{params: map[string][]string{"qwen/qwen3.8-flash": aggregatorListedParams}}
	m := newWindowTestManager(t, cfg, listing, nil)

	if got := m.ModelParameters(cfg, "hub", "qwen/qwen3.8-flash"); got != nil {
		t.Fatalf("parameters before the listing was read = %v, want unknown", got)
	}
	m.AwaitContextWindows(context.Background(), cfg, []string{"hub/qwen/qwen3.8-flash"}, time.Second)
	if n := listing.calls.Load(); n != 1 {
		t.Fatalf("listing read %d times, want 1: a model with reasoning levels needs it even with max_context_tokens", n)
	}
	if got := m.ModelParameters(cfg, "hub", "qwen/qwen3.8-flash"); !slices.Equal(got, aggregatorListedParams) {
		t.Fatalf("parameters = %v, want %v", got, aggregatorListedParams)
	}
	// The window configured for the model still wins over anything listed.
	if tokens, source := m.ContextWindow(cfg, "hub/qwen/qwen3.8-flash"); tokens != 1000000 || source != ContextWindowFromConfig {
		t.Fatalf("ContextWindow = %d/%q, want the configured 1000000", tokens, source)
	}
	for _, tc := range []struct{ provider, model string }{
		{"hub", "unlisted"},
		{"oai", "gpt-4o"},
		{"nope", "qwen/qwen3.8-flash"},
	} {
		if got := m.ModelParameters(cfg, tc.provider, tc.model); got != nil {
			t.Errorf("%s/%s: parameters = %v, want unknown", tc.provider, tc.model, got)
		}
	}
}

// What a session's turn reads: the parameters of the model it runs on, fetched
// when the turn is admitted, like its window.
func TestTurnsReadTheModelParametersThroughTheManager(t *testing.T) {
	root := t.TempDir()
	cfg := modelParamsTestConfig(t.TempDir())
	cfg.Agent.Model = "hub/qwen/qwen3.8-flash"
	listing := &windowListing{params: map[string][]string{"qwen/qwen3.8-flash": aggregatorListedParams}}
	var seen []string
	runner := func(_ context.Context, st *State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		seen = st.ModelParameters(cfg, "hub", "qwen/qwen3.8-flash")
		return string(acp.StopReasonEndTurn), nil
	}
	m := NewManager(cfg, &contextUsageCapture{}, runner, slog.Default(), root, &FileStore{Root: t.TempDir()})
	m.SetContextWindowLister(listing.list, nil)
	t.Cleanup(func() { _ = m.WaitContextWindowsIdle(5 * time.Second) })
	ctx := context.Background()

	res, err := m.HandleSessionNew(ctx, acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.HandleSessionPrompt(ctx, acp.SessionPromptParams{
		SessionID: res.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "hello"}},
	}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seen, aggregatorListedParams) {
		t.Fatalf("the first turn read %v, want %v", seen, aggregatorListedParams)
	}

	bare := &State{ID: "sess_bare"}
	if got := bare.ModelParameters(cfg, "hub", "qwen/qwen3.8-flash"); got != nil {
		t.Fatalf("a state no manager built knows no parameters, got %v", got)
	}
}

// Only an OpenAI-compatible row behind an explicit api_base (an aggregator)
// publishes request parameters: a model of another row with a window of its
// own has nothing to learn from its listing, and the listing is not read.
func TestModelParametersAreReadOnlyFromOpenAICompatibleRows(t *testing.T) {
	cfg := modelParamsTestConfig(t.TempDir())
	for i := range cfg.Models {
		if cfg.Models[i].Model == "nd/qwen3.8-27b" {
			cfg.Models[i].MaxContextTokens = 262144
		}
	}
	listing := &windowListing{params: map[string][]string{"qwen3.8-27b": aggregatorListedParams}}
	m := newWindowTestManager(t, cfg, listing, nil)

	m.AwaitContextWindows(context.Background(), cfg, []string{"nd/qwen3.8-27b"}, time.Second)
	if n := listing.calls.Load(); n != 0 {
		t.Fatalf("listing read %d times for a neuraldeep model with its own window, want 0", n)
	}
	if got := m.ModelParameters(cfg, "nd", "qwen3.8-27b"); got != nil {
		t.Fatalf("parameters = %v, want unknown", got)
	}
}
