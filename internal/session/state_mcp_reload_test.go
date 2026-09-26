package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

func TestReplaceConfiguredMCPClientsPreservesSessionClients(t *testing.T) {
	globalBefore := mcp.NewStaticClient("global-before", nil)
	sessionClient := mcp.NewStaticClient("session", nil)
	globalAfter := mcp.NewStaticClient("global-after", nil)
	st := &State{}
	st.addConfiguredMCPClient(globalBefore)
	st.AddSessionMCPClient(sessionClient)

	st.replaceConfiguredMCPClients([]*mcp.Client{globalAfter})
	got := st.GetMCPClients()
	if len(got) != 2 || got[0] != globalAfter || got[1] != sessionClient {
		t.Fatalf("MCP clients after reload = %+v", got)
	}
}

func TestReloadConfigForSessionConnectsNewMCPImmediately(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	next := &config.Config{
		Skills: config.Skills{Dirs: []string{skillsDir}},
		MCPServers: []config.MCPServerConfig{{
			Name:    "hot-mcp",
			Command: os.Args[0],
			Args:    []string{"-test.run=TestConfigReloadMCPHelperProcess"},
			Env:     []config.EnvVarConfig{{Name: "GO_WANT_CONFIG_RELOAD_MCP", Value: "1"}},
		}},
	}
	raw, err := config.MarshalConfigYAML(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	initial := &config.Config{
		Paths:  config.Paths{Home: dir, CWD: dir, ConfigPath: configPath},
		Skills: config.Skills{Dirs: []string{skillsDir}},
	}
	mgr := NewManager(initial, nil, nil, slog.Default(), dir, nil)
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(created.SessionID)
	otherCreated, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: dir})
	if err != nil {
		t.Fatal(err)
	}
	other := mgr.SessionByID(otherCreated.SessionID)
	t.Cleanup(st.CloseAll)
	t.Cleanup(other.CloseAll)
	reloadCtx, cancelReload := context.WithCancel(context.Background())
	warnings, err := mgr.ReloadConfigForSession(reloadCtx, st)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings: %v", warnings)
	}
	clients := st.GetMCPClients()
	if len(clients) != 1 || clients[0].Name() != "hot-mcp" {
		t.Fatalf("MCP clients = %+v", clients)
	}
	tools := clients[0].Tools()
	if len(tools) != 1 || tools[0].Name != "ping" {
		t.Fatalf("MCP tools were not loaded immediately: %+v", tools)
	}
	otherClients := other.GetMCPClients()
	if len(otherClients) != 1 || otherClients[0].Name() != "hot-mcp" {
		t.Fatalf("other active session MCP clients = %+v", otherClients)
	}
	cancelReload()
	for i := 0; i < 20; i++ {
		callCtx, cancelCall := context.WithTimeout(context.Background(), 100*time.Millisecond)
		got, callErr := clients[0].CallTool(callCtx, "ping", `{}`)
		cancelCall()
		if callErr != nil {
			t.Fatalf("MCP should outlive the config_commit turn context: %v", callErr)
		}
		if got != "pong" {
			t.Fatalf("ping result = %q", got)
		}
	}
}

func TestMCPServerDisabledFromConsoleIsAbsentOnNextTurn(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	cfg := &config.Config{Paths: config.Paths{Home: home, CWD: cwd}}
	if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(home), "toggle-mcp", config.MCPJSONServer{
		Command: os.Args[0], Args: []string{"-test.run=TestConfigReloadMCPHelperProcess"},
		Env: map[string]string{"GO_WANT_CONFIG_RELOAD_MCP": "1"},
	}); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(cfg, nil, nil, slog.Default(), cwd, nil)
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(created.SessionID)
	defer st.CloseAll()
	if len(st.GetMCPClients()) != 1 {
		t.Fatal("server did not connect")
	}
	if err := mgr.SetMCPEnabled(context.Background(), cwd, "toggle-mcp", "", false); err != nil {
		t.Fatal(err)
	}
	if len(st.GetMCPClients()) != 0 {
		t.Fatal("disabled server remained connected")
	}
	if st.GetMCPToolFilter()("toggle-mcp", "ping") {
		t.Fatal("next turn still offers disabled server tool")
	}
}

// reloadHelperEntry declares the stdio MCP server TestConfigReloadMCPHelperProcess
// plays: one tool, ping. mode "hang" starts a server that never answers.
func reloadHelperEntry(mode string) config.MCPJSONServer {
	return config.MCPJSONServer{
		Command: os.Args[0], Args: []string{"-test.run=TestConfigReloadMCPHelperProcess"},
		Env: map[string]string{"GO_WANT_CONFIG_RELOAD_MCP": mode},
	}
}

// configuredClient returns the live client of one server, nil when the
// session has none.
func configuredClient(st *State, name string) *mcp.Client {
	for _, client := range st.GetMCPClients() {
		if client.Name() == name {
			return client
		}
	}
	return nil
}

// newMCPToggleSession starts a session over <home>/mcp.json servers.
func newMCPToggleSession(t *testing.T, servers map[string]config.MCPJSONServer) (*Manager, *State, string) {
	t.Helper()
	home, cwd := t.TempDir(), t.TempDir()
	for name, entry := range servers {
		if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(home), name, entry); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{Paths: config.Paths{Home: home, CWD: cwd}}
	mgr := NewManager(cfg, nil, nil, slog.Default(), cwd, nil)
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(created.SessionID)
	t.Cleanup(st.CloseAll)
	return mgr, st, cwd
}

// A switch touches only the server it names. A tool switch reconnects
// nothing - the per-turn filter reads it - and a server switch dials or closes
// that one server, so a stateful neighbour (a browser-automation server with
// its pages open) keeps its process.
func TestMCPSwitchReconnectsOnlyTheServerThatChanged(t *testing.T) {
	mgr, st, cwd := newMCPToggleSession(t, map[string]config.MCPJSONServer{
		"alpha": reloadHelperEntry("1"), "beta": reloadHelperEntry("1"),
	})
	ctx := context.Background()
	alpha, beta := configuredClient(st, "alpha"), configuredClient(st, "beta")
	if alpha == nil || beta == nil {
		t.Fatalf("clients at start = %+v", st.GetMCPClients())
	}

	if err := mgr.SetMCPEnabled(ctx, cwd, "alpha", "ping", false); err != nil {
		t.Fatal(err)
	}
	if configuredClient(st, "alpha") != alpha || configuredClient(st, "beta") != beta {
		t.Fatal("a tool switch reconnected MCP servers")
	}
	if st.GetMCPToolFilter()("alpha", "ping") {
		t.Fatal("the switched-off tool is still offered")
	}

	if err := mgr.SetMCPEnabled(ctx, cwd, "alpha", "", false); err != nil {
		t.Fatal(err)
	}
	if configuredClient(st, "alpha") != nil {
		t.Fatal("the switched-off server is still connected")
	}
	if configuredClient(st, "beta") != beta {
		t.Fatal("switching alpha off reconnected beta")
	}

	if err := mgr.SetMCPEnabled(ctx, cwd, "alpha", "", true); err != nil {
		t.Fatal(err)
	}
	if configuredClient(st, "alpha") == nil {
		t.Fatal("the switched-on server did not connect in the live session")
	}
	if configuredClient(st, "beta") != beta {
		t.Fatal("switching alpha on reconnected beta")
	}
}

// Trust granted from a surface connects that project server in live sessions,
// trust withdrawn closes it, and an approval of a declaration rewritten since
// it was listed is refused.
func TestMCPTrustConnectsAndClosesOnlyThatServer(t *testing.T) {
	mgr, st, cwd := newMCPToggleSession(t, map[string]config.MCPJSONServer{"beta": reloadHelperEntry("1")})
	ctx := context.Background()
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(cwd), "proj", reloadHelperEntry("1")); err != nil {
		t.Fatal(err)
	}
	beta := configuredClient(st, "beta")
	rows, err := mgr.MCPServers(ctx, cwd)
	if err != nil {
		t.Fatal(err)
	}
	shown := ""
	for _, row := range rows {
		if row.Name == "proj" {
			shown = row.Fingerprint
		}
	}
	if shown == "" {
		t.Fatalf("project row carries no fingerprint: %+v", rows)
	}

	if err := mgr.SetMCPTrust(ctx, cwd, "proj", shown, true); err != nil {
		t.Fatal(err)
	}
	if configuredClient(st, "proj") == nil {
		t.Fatal("the approved project server did not connect in the live session")
	}
	if configuredClient(st, "beta") != beta {
		t.Fatal("approving proj reconnected beta")
	}

	if err := mgr.SetMCPTrust(ctx, cwd, "proj", "", false); err != nil {
		t.Fatal(err)
	}
	if configuredClient(st, "proj") != nil {
		t.Fatal("the project server kept running after its trust was withdrawn")
	}

	rewritten := reloadHelperEntry("1")
	rewritten.Args = append(rewritten.Args, "-test.v")
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(cwd), "proj", rewritten); err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetMCPTrust(ctx, cwd, "proj", shown, true); !errors.Is(err, mcp.ErrDeclarationChanged) {
		t.Fatalf("approving a rewritten declaration: err = %v", err)
	}
	if configuredClient(st, "proj") != nil {
		t.Fatal("the rewritten declaration started")
	}
}

// A session in the middle of a turn keeps the tool list that turn handed the
// model; the switch reaches it when the turn releases its lock.
func TestMCPSwitchWaitsForTheTurnInFlight(t *testing.T) {
	off := reloadHelperEntry("1")
	off.Disabled = true
	mgr, st, cwd := newMCPToggleSession(t, map[string]config.MCPJSONServer{"alpha": off})
	if configuredClient(st, "alpha") != nil {
		t.Fatal("a disabled server connected at session start")
	}
	unlock, err := mgr.acquireTurnLockWithReloadDrain(st.GetID(), st)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.SetMCPEnabled(context.Background(), cwd, "alpha", "", true); err != nil {
		unlock()
		t.Fatal(err)
	}
	if configuredClient(st, "alpha") != nil {
		unlock()
		t.Fatal("the server connected under a turn in flight")
	}
	unlock()
	if configuredClient(st, "alpha") == nil {
		t.Fatal("the switch was not applied when the turn ended")
	}
}

// A server that never answers its handshake does not hold the caller: the
// refresh gives up at its deadline and leaves the switch for the session's
// next turn to retry.
func TestMCPRefreshGivesUpAtItsDeadline(t *testing.T) {
	previous := mcpRefreshTimeout
	mcpRefreshTimeout = 300 * time.Millisecond
	t.Cleanup(func() { mcpRefreshTimeout = previous })
	hang := reloadHelperEntry("hang")
	hang.Disabled = true
	mgr, st, cwd := newMCPToggleSession(t, map[string]config.MCPJSONServer{"hang": hang})

	started := time.Now()
	if err := mgr.SetMCPEnabled(context.Background(), cwd, "hang", "", true); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("switching a hung server on took %v", elapsed)
	}
	if configuredClient(st, "hang") != nil {
		t.Fatal("a server that never answered was installed")
	}
	if !st.hasPendingMCPReload() {
		t.Fatal("the switch was dropped instead of left for the next turn")
	}
}

// mcpTurnSender answers a turn's updates and permissions without a client.
type mcpTurnSender struct{}

func (mcpTurnSender) SendSessionUpdate(string, interface{}) error { return nil }

func (mcpTurnSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow"}, nil
}

func (mcpTurnSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

// reloadHelperServer is reloadHelperEntry as a config.yaml server.
func reloadHelperServer(name string) config.MCPServerConfig {
	return config.MCPServerConfig{
		Name: name, Command: os.Args[0], Args: []string{"-test.run=TestConfigReloadMCPHelperProcess"},
		Env: []config.EnvVarConfig{{Name: "GO_WANT_CONFIG_RELOAD_MCP", Value: "1"}},
	}
}

// newStoredMCPSession leaves a session on disk under a configuration with
// config.yaml MCP servers and loads it back, the way a read of it does.
func newStoredMCPSession(t *testing.T, servers ...config.MCPServerConfig) (*Manager, *State) {
	t.Helper()
	home, cwd := t.TempDir(), t.TempDir()
	cfg := &config.Config{
		Paths:      config.Paths{Home: home, CWD: cwd},
		Providers:  []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:     []config.ModelEntry{{Model: "fake/model", MaxTokens: 200}},
		Agent:      config.Agent{Model: "fake/model"},
		MCPServers: servers,
	}
	runner := func(context.Context, *State, []acp.ContentBlock, acp.UpdateSender) (string, error) { return "", nil }
	mgr := NewManager(cfg, mcpTurnSender{}, runner, slog.Default(), cwd, &FileStore{Root: filepath.Join(home, "sessions")})
	ctx := context.Background()
	created, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	mgr.ForgetLiveSession(created.SessionID)
	st, err := mgr.EnsureHTTPSession(ctx, created.SessionID, cwd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mgr.ForgetLiveSession(created.SessionID) })
	return mgr, st
}

// Nothing a stored session is loaded for starts its MCP servers - a settings
// save, a server switch, a compaction - and its first turn starts the servers
// of the configuration of that moment.
func TestStoredSessionStartsItsMCPServersOnlyForItsFirstTurn(t *testing.T) {
	mgr, st := newStoredMCPSession(t, reloadHelperServer("alpha"))
	ctx := context.Background()
	if clients := st.GetMCPClients(); len(clients) != 0 {
		t.Fatalf("loading the session started %d MCP servers", len(clients))
	}

	next := *mgr.activeCfg()
	next.MCPServers = []config.MCPServerConfig{reloadHelperServer("alpha"), reloadHelperServer("beta")}
	mgr.ReplaceConfig(&next)
	if clients := st.GetMCPClients(); len(clients) != 0 {
		t.Fatalf("a settings save started %d MCP servers of a session nobody ran", len(clients))
	}
	mgr.RefreshMCPServer(ctx, "alpha")
	if clients := st.GetMCPClients(); len(clients) != 0 {
		t.Fatalf("a server switch started %d MCP servers of a session nobody ran", len(clients))
	}
	_, finish, err := mgr.BeginSessionWork(ctx, st.GetID())
	if err != nil {
		t.Fatal(err)
	}
	finish()
	if clients := st.GetMCPClients(); len(clients) != 0 {
		t.Fatalf("a compaction started %d MCP servers", len(clients))
	}

	if _, err := mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
		SessionID: st.GetID(),
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "go on"}},
	}, mcpTurnSender{}, nil); err != nil {
		t.Fatal(err)
	}
	if configuredClient(st, "alpha") == nil || configuredClient(st, "beta") == nil {
		t.Fatalf("the first turn did not start the servers of the current configuration: %+v", st.GetMCPClients())
	}
	// A later turn keeps them rather than starting them again.
	alpha := configuredClient(st, "alpha")
	if _, err := mgr.HandleSessionPromptWithSender(ctx, acp.SessionPromptParams{
		SessionID: st.GetID(),
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "and again"}},
	}, mcpTurnSender{}, nil); err != nil {
		t.Fatal(err)
	}
	if configuredClient(st, "alpha") != alpha || len(st.GetMCPClients()) != 2 {
		t.Fatalf("a second turn restarted the servers: %+v", st.GetMCPClients())
	}
}

func TestReloadConfigForSessionRequiresProjectTrustAndRefreshesFilter(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	skillsDir := filepath.Join(dir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	next := &config.Config{Skills: config.Skills{Dirs: []string{skillsDir}}}
	raw, err := config.MarshalConfigYAML(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(dir), "project-mcp", config.MCPJSONServer{
		Command:       os.Args[0],
		Args:          []string{"-test.run=TestConfigReloadMCPHelperProcess"},
		Env:           map[string]string{"GO_WANT_CONFIG_RELOAD_MCP": "1"},
		DisabledTools: []string{"ping"},
	}); err != nil {
		t.Fatal(err)
	}

	initial := &config.Config{
		Paths:  config.Paths{Home: filepath.Join(dir, "home"), CWD: dir, ConfigPath: configPath},
		Skills: config.Skills{Dirs: []string{skillsDir}},
	}
	mgr := NewManager(initial, nil, nil, slog.Default(), dir, nil)
	st := &State{ID: "reload-project-mcp", CWD: dir, Mode: ModeAgent}
	warnings, err := mgr.ReloadConfigForSession(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "not approved") {
		t.Fatalf("warnings before approval = %v", warnings)
	}
	if clients := st.GetMCPClients(); len(clients) != 0 {
		t.Fatalf("untrusted project MCP clients = %+v", clients)
	}

	managed, err := mcp.ListManagedServers(mgr.Cfg(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(managed) != 1 || managed[0].Config.Name != "project-mcp" {
		t.Fatalf("managed MCP servers = %+v", managed)
	}
	if err := mcp.NewTrustGate(mgr.Cfg()).Approve(dir, managed[0]); err != nil {
		t.Fatal(err)
	}
	warnings, err = mgr.ReloadConfigForSession(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	defer st.CloseAll()
	if len(warnings) != 0 {
		t.Fatalf("warnings after approval: %v", warnings)
	}
	clients := st.GetMCPClients()
	if len(clients) != 1 || clients[0].Name() != "project-mcp" {
		t.Fatalf("MCP clients = %+v", clients)
	}
	if st.GetMCPToolFilter()("project-mcp", "ping") {
		t.Fatal("disabled project MCP tool remained available after reload")
	}
}

func TestConfigReloadMCPHelperProcess(t *testing.T) {
	switch os.Getenv("GO_WANT_CONFIG_RELOAD_MCP") {
	case "1":
	case "hang":
		// Reads the handshake and never answers it.
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	default:
		return
	}
	enc := json.NewEncoder(os.Stdout)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		var req struct {
			ID     interface{} `json:"id"`
			Method string      `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil || req.ID == nil {
			continue
		}
		var result interface{}
		switch req.Method {
		case "initialize":
			result = map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"serverInfo":      map[string]string{"name": "reload-test", "version": "1"},
			}
		case "tools/list":
			result = map[string]interface{}{
				"tools": []interface{}{map[string]interface{}{
					"name": "ping", "description": "Test tool",
					"inputSchema": map[string]string{"type": "object"},
				}},
			}
		case "tools/call":
			result = map[string]interface{}{
				"content": []interface{}{map[string]string{"type": "text", "text": "pong"}},
			}
		default:
			result = map[string]interface{}{}
		}
		_ = enc.Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
	os.Exit(0)
}

// A resume replaces the SessionStart context an earlier run stored, even
// when nothing runs any more: the hooks were removed or switched off.
func TestSessionStartHooksReplaceStaleContext(t *testing.T) {
	root := t.TempDir()
	cfg := &config.Config{
		Paths:     config.Paths{Home: filepath.Join(root, "home"), CWD: filepath.Join(root, "work")},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	cfg.Hooks.ApplyDefaults(cfg.Paths)
	m := NewManager(cfg, nil, nil, slog.Default(), cfg.Paths.CWD, nil)

	st := &State{ID: "sess_stale_hook_context", CWD: cfg.Paths.CWD, Mode: ModeAgent}
	st.RestoreHookContextWithoutPersist("stale context from a removed hook")
	m.runSessionStartHooks(context.Background(), st, "resume")
	if got := st.GetHookContext(); got != "" {
		t.Fatalf("a resume without hook files must clear the stored context, got %q", got)
	}

	off := false
	cfg.Hooks.Enabled = &off
	st.RestoreHookContextWithoutPersist("stale context with hooks disabled")
	m.runSessionStartHooks(context.Background(), st, "resume")
	if got := st.GetHookContext(); got != "" {
		t.Fatalf("a resume with hooks disabled must clear the stored context, got %q", got)
	}
}
