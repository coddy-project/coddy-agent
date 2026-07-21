package session_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
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

type captureSender struct {
	mu  sync.Mutex
	ups []interface{}
}

func (c *captureSender) SendSessionUpdate(_ string, u interface{}) error {
	c.mu.Lock()
	c.ups = append(c.ups, u)
	c.mu.Unlock()
	return nil
}

func (c *captureSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow"}, nil
}

func (c *captureSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func (noopSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func testConfig() *config.Config {
	return &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "p1", Type: "openai", APIKey: "k"},
			{Name: "p2", Type: "openai", APIKey: "k"},
			{Name: "p3", Type: "anthropic", APIKey: "k"},
		},
		Models: []config.ModelEntry{
			{Model: "p1/gpt-4o"},
			{Model: "p2/gpt-4o-mini"},
			{Model: "p3/claude-3"},
		},
		Agent: config.Agent{Model: "p1/gpt-4o"},
	}
}

func noopRunner(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
	return string(acp.StopReasonEndTurn), nil
}

func TestInitializeWithPersistenceAdvertisesLoad(t *testing.T) {
	cfg := testConfig()
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	if err := os.MkdirAll(store.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	m := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default(), "/tmp", store)
	res, err := m.HandleInitialize(context.Background(), acp.InitializeParams{ProtocolVersion: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !res.AgentCapabilities.LoadSession {
		t.Fatal("expected LoadSession true with store")
	}
	if res.AgentCapabilities.SessionCapabilities == nil {
		t.Fatal("expected SessionCapabilities with store")
	}
}

func TestManagerSessionNewUsesDefaultCWWhenClientEmpty(t *testing.T) {
	defaultDir := t.TempDir()
	want, err := filepath.Abs(defaultDir)
	if err != nil {
		t.Fatal(err)
	}
	var gotCWD string
	runner := func(_ context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
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
		return
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
		return
	}
	if modeOpt.Category != "mode" || modeOpt.Type != "select" {
		t.Fatalf("mode option: %+v", modeOpt)
	}
	if modeOpt.CurrentValue != "agent" {
		t.Fatalf("expected current mode agent, got %q", modeOpt.CurrentValue)
	}
	if modelOpt == nil {
		t.Fatal("expected config option id model")
		return
	}
	if modelOpt.Category != "model" || modelOpt.Type != "select" {
		t.Fatalf("model option: %+v", modelOpt)
	}
	if len(modelOpt.Options) != 3 {
		t.Fatalf("expected 3 model choices, got %d", len(modelOpt.Options))
	}
	if modelOpt.CurrentValue != "p1/gpt-4o" {
		t.Fatalf("expected default model p1/gpt-4o for agent mode, got %q", modelOpt.CurrentValue)
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
		Value:     "p3/claude-3",
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
	if current != "p3/claude-3" {
		t.Fatalf("expected model p3/claude-3 after set, got %q", current)
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
	// No explicit model override: effective model stays agent.model (p1/gpt-4o).
	if modelCur != "p1/gpt-4o" {
		t.Fatalf("expected effective model p1/gpt-4o for plan mode without override, got %q", modelCur)
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

func TestSessionLoadDoesNotRewriteSessionUpdatedAt(t *testing.T) {
	root := t.TempDir()
	store := &session.FileStore{Root: root}
	cfg := testConfig()

	id := "sess_list_order_keep"
	dir, err := store.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	metaPath := filepath.Join(dir, "session.json")
	var meta struct {
		Version   int    `json:"version"`
		ID        string `json:"id"`
		CWD       string `json:"cwd"`
		Mode      string `json:"mode"`
		UpdatedAt string `json:"updatedAt"`
	}
	before := "2019-06-01T12:00:00Z"
	if err := json.Unmarshal(slurpFile(t, metaPath), &meta); err != nil {
		t.Fatal(err)
	}
	meta.CWD = "/tmp"
	meta.Mode = "agent"
	meta.UpdatedAt = before
	writeJSONIndent(t, metaPath, meta)
	msgPath := filepath.Join(dir, "messages.json")
	msgWrap := map[string]interface{}{
		"version":  1,
		"messages": []interface{}{},
	}
	writeJSONIndent(t, msgPath, msgWrap)

	m := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default(), "/tmp", store)
	ctx := context.Background()
	if _, err := m.HandleSessionLoad(ctx, acp.SessionLoadParams{
		SessionID:  id,
		CWD:        "/tmp",
		MCPServers: nil,
	}); err != nil {
		t.Fatal(err)
	}
	var metaAfter struct {
		UpdatedAt string `json:"updatedAt"`
	}
	if err := json.Unmarshal(slurpFile(t, metaPath), &metaAfter); err != nil {
		t.Fatal(err)
	}
	if metaAfter.UpdatedAt != before {
		t.Fatalf("session.json updatedAt changed on load: %q -> %q", before, metaAfter.UpdatedAt)
	}
}

func slurpFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func writeJSONIndent(t *testing.T, path string, v interface{}) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestManagerPersistMessagesAndReload(t *testing.T) {
	root := t.TempDir()
	store := &session.FileStore{Root: root}
	cfg := testConfig()

	persistRunner := func(_ context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
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
	peekRunner := func(_ context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
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

func TestHandleSessionCancelEndsBlockedPrompt(t *testing.T) {
	cfg := testConfig()
	blockStarted := make(chan struct{})
	runner := func(ctx context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		close(blockStarted)
		<-ctx.Done()
		return string(acp.StopReasonCancelled), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	ctx := context.Background()
	res, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	id := res.SessionID

	var wg sync.WaitGroup
	wg.Add(1)
	var out *acp.SessionPromptResult
	var promptErr error
	go func() {
		defer wg.Done()
		out, promptErr = mgr.HandleSessionPrompt(ctx, acp.SessionPromptParams{
			SessionID: id,
			Prompt:    []acp.ContentBlock{{Type: "text", Text: "hello"}},
		})
	}()

	<-blockStarted
	mgr.HandleSessionCancel(acp.SessionCancelParams{SessionID: id})
	wg.Wait()

	if promptErr != nil {
		t.Fatalf("prompt: %v", promptErr)
	}
	if out == nil {
		t.Fatal("nil prompt result")
	}
	if out.StopReason != acp.StopReasonCancelled {
		t.Fatalf("stop reason %q want %q", out.StopReason, acp.StopReasonCancelled)
	}
}

func TestHandleSessionPromptWithSenderSkipTurnLockSurvivesParentCancel(t *testing.T) {
	runBlock := make(chan struct{})
	cont := make(chan struct{})
	runner := func(ctx context.Context, _ *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		close(runBlock)
		<-cont
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return string(acp.StopReasonEndTurn), nil
	}
	cfg := testConfig()
	m := session.NewManager(cfg, noopSender{}, runner, slog.Default(), "/tmp", nil)
	sn, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	var res *acp.SessionPromptResult
	var perr error
	go func() {
		defer wg.Done()
		res, perr = m.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
			SessionID: sn.SessionID,
			Prompt:    []acp.ContentBlock{{Type: "text", Text: "x"}},
		}, noopSender{}, &session.PromptRunOpts{SkipTurnLock: true})
	}()
	<-runBlock
	cancel()
	close(cont)
	wg.Wait()
	if perr != nil {
		t.Fatalf("prompt: %v", perr)
	}
	if res == nil || res.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("unexpected %+v err=%v", res, perr)
	}
}

func TestSessionNewSendsAvailableSlashCommandsUpdate(t *testing.T) {
	skRoot := t.TempDir()
	skillDir := filepath.Join(skRoot, "probe")
	if err := os.MkdirAll(filepath.Join(skillDir, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "demo", "SKILL.md"), []byte("# Demo skill\n\nRuns demo flow.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	cfg.Skills.Dirs = []string{skillDir}
	snd := &captureSender{}
	m := session.NewManager(cfg, snd, noopRunner, slog.Default(), t.TempDir(), nil)
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("HandleSessionNew: %v", err)
	}
	_ = res
	var slash *acp.AvailableCommandsUpdate
	for _, u := range snd.ups {
		if v, ok := u.(acp.AvailableCommandsUpdate); ok && v.SessionUpdate == acp.UpdateTypeAvailableCommandsUpdate {
			slash = &v
			break
		}
	}
	if slash == nil {
		t.Fatalf("expected AvailableCommandsUpdate in %#v", snd.ups)
		return
	}
	// Skills plus the built-in compact command (advertised while compaction is enabled).
	if len(slash.AvailableCommands) != 3 {
		t.Fatalf("unexpected commands %+v", slash.AvailableCommands)
	}
	names := map[string]bool{}
	for _, c := range slash.AvailableCommands {
		names[c.Name] = true
	}
	if !names["demo"] || !names["generate-rules"] || !names["compact"] {
		t.Fatalf("expected demo, generate-rules, and compact, got %+v", slash.AvailableCommands)
	}
}

func TestSetSessionWorkspaceSwitchesCwdAndPersists(t *testing.T) {
	cfg := testConfig()
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	if err := os.MkdirAll(store.Root, 0o755); err != nil {
		t.Fatal(err)
	}
	m := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default(), "/tmp", store)

	alpha := filepath.Join(root, "alpha")
	beta := filepath.Join(root, "beta")
	for _, d := range []string{alpha, beta} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: alpha})
	if err != nil {
		t.Fatalf("HandleSessionNew: %v", err)
	}
	st := m.SessionByID(res.SessionID)
	if st == nil {
		t.Fatal("session not registered")
	}

	if err := m.SetSessionWorkspace(st, beta); err != nil {
		t.Fatalf("SetSessionWorkspace: %v", err)
	}
	if got := st.GetCWD(); got != beta {
		t.Fatalf("cwd = %q, want %q", got, beta)
	}

	raw, err := os.ReadFile(filepath.Join(store.Root, res.SessionID, "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		CWD string `json:"cwd"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.CWD != beta {
		t.Fatalf("persisted cwd = %q, want %q", meta.CWD, beta)
	}

	if err := m.SetSessionWorkspace(st, filepath.Join(root, "missing")); err == nil {
		t.Fatal("expected error for missing folder")
	}
	if got := st.GetCWD(); got != beta {
		t.Fatalf("cwd changed on failed switch: %q", got)
	}
}
