package session_test

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type noopSender struct{}

func (noopSender) SendSessionUpdate(string, interface{}) error { return nil }

func (noopSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow"}, nil
}

func testConfig() *config.Config {
	return &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "p1", Type: "openai", APIKey: "k"},
			{Name: "p2", Type: "openai", APIKey: "k"},
			{Name: "p3", Type: "anthropic", APIKey: "k"},
		},
		Models: []config.ModelEntry{
			{ID: "m1", Provider: "p1", Model: "gpt-4o"},
			{ID: "m2", Provider: "p2", Model: "gpt-4o-mini"},
			{ID: "m3", Provider: "p3", Model: "claude-3"},
		},
		React: config.React{Model: "m1"},
	}
}

func noopRunner(context.Context, *session.State, []acp.ContentBlock) (string, error) {
	return string(acp.StopReasonEndTurn), nil
}

func TestInitializeWithoutPersistenceOmitsLoadAndList(t *testing.T) {
	cfg := testConfig()
	m := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default(), "/tmp", nil)
	res, err := m.HandleInitialize(context.Background(), acp.InitializeParams{ProtocolVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.AgentCapabilities.LoadSession {
		t.Fatal("expected LoadSession false without store")
	}
	if res.AgentCapabilities.SessionCapabilities != nil {
		t.Fatal("expected SessionCapabilities omitted without store")
	}
}

func TestManagerSessionNewUsesDefaultCWWhenClientEmpty(t *testing.T) {
	defaultDir := t.TempDir()
	want, err := filepath.Abs(defaultDir)
	if err != nil {
		t.Fatal(err)
	}
	var gotCWD string
	runner := func(_ context.Context, st *session.State, _ []acp.ContentBlock) (string, error) {
		gotCWD = st.GetCWD()
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := testConfig()
	m := session.NewManager(cfg, noopSender{}, runner, slog.Default(), defaultDir, nil)

	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: ""})
	if err != nil {
		t.Fatalf("HandleSessionNew: %v", err)
	}
	if _, err := m.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: res.SessionID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "x"}},
	}); err != nil {
		t.Fatalf("HandleSessionPrompt: %v", err)
	}
	if gotCWD != want {
		t.Fatalf("session cwd %q, want %q", gotCWD, want)
	}
}

func TestManagerSessionNewIncludesConfigOptions(t *testing.T) {
	cfg := testConfig()
	m := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default(), "", nil)

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
	m := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default(), "", nil)

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
	m := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default(), "", nil)

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
	// No explicit model override: effective model stays react.model (m1).
	if modelCur != "m1" {
		t.Fatalf("expected effective model m1 for plan mode without override, got %q", modelCur)
	}
}

func TestManagerSetConfigOptionUnknownValue(t *testing.T) {
	cfg := testConfig()
	m := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default(), "", nil)

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

func TestManagerPersistMessagesAndReload(t *testing.T) {
	root := t.TempDir()
	store := &session.FileStore{Root: root}
	cfg := testConfig()

	persistRunner := func(_ context.Context, st *session.State, _ []acp.ContentBlock) (string, error) {
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "u"})
		st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "a"})
		return string(acp.StopReasonEndTurn), nil
	}

	m1 := session.NewManager(cfg, noopSender{}, persistRunner, slog.Default(), "/tmp", store)
	ctx := context.Background()
	res1, err := m1.HandleSessionNew(ctx, acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	id := res1.SessionID
	if _, err := m1.HandleSessionPrompt(ctx, acp.SessionPromptParams{
		SessionID: id,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "ignored-by-test-runner"}},
	}); err != nil {
		t.Fatal(err)
	}

	snap, err := store.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) != 2 {
		t.Fatalf("expected 2 persisted messages after prompt, got %d", len(snap.Messages))
	}

	var afterReload int
	peekRunner := func(_ context.Context, st *session.State, _ []acp.ContentBlock) (string, error) {
		afterReload = len(st.GetMessages())
		return string(acp.StopReasonEndTurn), nil
	}

	m2 := session.NewManager(cfg, noopSender{}, peekRunner, slog.Default(), "/tmp", store)
	if _, err := m2.HandleSessionLoad(ctx, acp.SessionLoadParams{
		SessionID:  id,
		CWD:        "/tmp",
		MCPServers: nil,
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := m2.HandleSessionPrompt(ctx, acp.SessionPromptParams{
		SessionID: id,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "check"}},
	}); err != nil {
		t.Fatal(err)
	}
	if afterReload != 2 {
		t.Fatalf("session/load should restore 2 persisted messages before turn runs, got %d", afterReload)
	}
}
