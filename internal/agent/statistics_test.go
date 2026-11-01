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
