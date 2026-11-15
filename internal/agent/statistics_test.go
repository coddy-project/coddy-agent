package agent

import (
	"context"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/statistics"
)

type statisticsProvider struct{}

func (statisticsProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return &llm.Response{Content: "done", StopReason: "end_turn", InputTokens: 100, OutputTokens: 20, CachedInputTokens: 50}, nil
}
func (p statisticsProvider) Stream(ctx context.Context, m []llm.Message, defs []llm.ToolDefinition, chunk func(llm.StreamChunk)) (*llm.Response, error) {
	if chunk != nil {
		chunk(llm.StreamChunk{TextDelta: "done"})
	}
	return p.Complete(ctx, m, defs)
}

func TestStatisticsRecordsTheProviderCallAndTurnAcrossSurfaces(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Paths: config.Paths{Home: home}, Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}}, Models: []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000}}, Agent: config.Agent{Model: "fake/model"}}
	st := &session.State{ID: "sess_stats", CWD: home, Mode: session.ModeAgent}
	ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
		return llm.WrapResilient(statisticsProvider{}, llm.ResilientOptions{Name: "fake", Model: "model", RetryDisabled: true}), nil
	}
	for _, source := range []string{"webui", "gateway:telegram", "api"} {
		if _, err := ag.Run(statistics.WithSource(context.Background(), source), []acp.ContentBlock{{Type: "text", Text: "answer"}}); err != nil {
			t.Fatal(err)
		}
	}
	report, err := statistics.New(home).Query(statistics.Filter{})
	if err != nil || report.Totals.Total == nil || *report.Totals.Total != 360 || report.Totals.Requests != 3 || report.Totals.Calls != 3 || len(report.Sources) != 3 {
		t.Fatalf("persisted agent usage: %+v %v", report, err)
	}
}

// Every surface names itself for the usage history, and a turn that names
// none still lands somewhere. The console and the editor's ACP wire used to
// name none, so both fell into one bucket with whatever else did not.
func TestStatisticsRecordsTheSurfaceThatRanTheTurn(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{Paths: config.Paths{Home: home}, Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}}, Models: []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000}}, Agent: config.Agent{Model: "fake/model"}}
	run := func(id string, ctx context.Context) {
		t.Helper()
		st := &session.State{ID: id, CWD: home, Mode: session.ModeAgent}
		ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
		ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) {
			return llm.WrapResilient(statisticsProvider{}, llm.ResilientOptions{Name: "fake", Model: "model", RetryDisabled: true}), nil
		}
		if _, err := ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: "answer"}}); err != nil {
			t.Fatal(err)
		}
	}
	run("sess_console", statistics.WithSource(context.Background(), "console"))
	run("sess_acp", statistics.WithSource(context.Background(), "acp"))
	// A turn whose surface said nothing: the bucket that used to hold both.
	run("sess_quiet", context.Background())

	report, err := statistics.New(home).Query(statistics.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, row := range report.Sources {
		got[row.Name] = row.Count
	}
	for _, name := range []string{"console", "acp", "local"} {
		if got[name] != 1 {
			t.Fatalf("source %q recorded %d turns, want 1: %+v", name, got[name], report.Sources)
		}
	}
	if len(report.Sources) != 3 {
		t.Fatalf("the surfaces did not stay apart: %+v", report.Sources)
	}
}
