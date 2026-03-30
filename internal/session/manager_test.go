package session_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type noopSender struct{}

func (noopSender) SendSessionUpdate(string, interface{}) error { return nil }

func (noopSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow"}, nil
}

func testConfig() *config.Config {
	return &config.Config{
		Models: config.ModelsConfig{
			Default:   "m1",
			AgentMode: "m1",
			PlanMode:  "m2",
			Defs: []config.ModelDefinition{
				{ID: "m1", Provider: "openai", Model: "gpt-4o"},
				{ID: "m2", Provider: "openai", Model: "gpt-4o-mini"},
				{ID: "m3", Provider: "anthropic", Model: "claude-3"},
			},
		},
	}
}

func noopRunner(context.Context, *session.State, []acp.ContentBlock) (string, error) {
	return string(acp.StopReasonEndTurn), nil
}

func TestManagerSessionNewIncludesConfigOptions(t *testing.T) {
	cfg := testConfig()
	m := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default())

	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatalf("HandleSessionNew: %v", err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	if len(res.ConfigOptions) < 2 {
		t.Fatalf("expected at least mode + model config options, got %d", len(res.ConfigOptions))
	}
	var modeOpt, modelOpt *acp.ConfigOption
	for i := range res.ConfigOptions {
		switch res.ConfigOptions[i].ID {
		case "mode":
			modeOpt = &res.ConfigOptions[i]
		case "model":
			modelOpt = &res.ConfigOptions[i]
		}
	}
	if modeOpt == nil {
		t.Fatal("expected config option id mode")
	}
	if modeOpt.Category != "mode" || modeOpt.Type != "select" {
		t.Fatalf("mode option: %+v", modeOpt)
	}
	if modeOpt.CurrentValue != "agent" {
		t.Fatalf("expected current mode agent, got %q", modeOpt.CurrentValue)
	}
	if modelOpt == nil {
		t.Fatal("expected config option id model")
	}
	if modelOpt.Category != "model" || modelOpt.Type != "select" {
		t.Fatalf("model option: %+v", modelOpt)
	}
	if len(modelOpt.Options) != 3 {
		t.Fatalf("expected 3 model choices, got %d", len(modelOpt.Options))
	}
	if modelOpt.CurrentValue != "m1" {
		t.Fatalf("expected default model m1 for agent mode, got %q", modelOpt.CurrentValue)
	}
}

func TestManagerSetConfigOptionModel(t *testing.T) {
	cfg := testConfig()
	m := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default())

	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatalf("HandleSessionNew: %v", err)
	}

	out, err := m.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{
		SessionID: res.SessionID,
		ConfigID:  "model",
		Value:     "m3",
	})
	if err != nil {
		t.Fatalf("HandleSessionSetConfigOption: %v", err)
	}
	if out == nil || len(out.ConfigOptions) < 2 {
		t.Fatalf("expected config options in result, got %+v", out)
	}
	var current string
	for _, o := range out.ConfigOptions {
		if o.ID == "model" {
			current = o.CurrentValue
			break
		}
	}
	if current != "m3" {
		t.Fatalf("expected model m3 after set, got %q", current)
	}
}

func TestManagerSetConfigOptionMode(t *testing.T) {
	cfg := testConfig()
	m := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default())

	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatalf("HandleSessionNew: %v", err)
	}

	out, err := m.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{
		SessionID: res.SessionID,
		ConfigID:  "mode",
		Value:     "plan",
	})
	if err != nil {
		t.Fatalf("HandleSessionSetConfigOption: %v", err)
	}
	var modeCur, modelCur string
	for _, o := range out.ConfigOptions {
		switch o.ID {
		case "mode":
			modeCur = o.CurrentValue
		case "model":
			modelCur = o.CurrentValue
		}
	}
	if modeCur != "plan" {
		t.Fatalf("expected mode plan, got %q", modeCur)
	}
	// No explicit model override: effective model follows plan_mode (m2).
	if modelCur != "m2" {
		t.Fatalf("expected effective model m2 for plan mode without override, got %q", modelCur)
	}
}

func TestManagerSetConfigOptionUnknownValue(t *testing.T) {
	cfg := testConfig()
	m := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default())

	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatalf("HandleSessionNew: %v", err)
	}

	_, err = m.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{
		SessionID: res.SessionID,
		ConfigID:  "model",
		Value:     "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for unknown model id")
	}
}
