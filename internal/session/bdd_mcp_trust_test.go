package session_test

// Godog harness for the @acp scenarios of features/mcp_project_trust.feature.
// It drives Manager.HandleSessionNew — the handler the ACP JSON-RPC server
// dispatches session/new to — against a workspace whose .coddy/mcp.json
// starts a command, and asserts on whether that command actually ran.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// TestHelperMCPMarkerServer is not a real test: re-executed with
// GO_WANT_MCP_MARKER=1 it stands in for a project-supplied MCP server that
// does something on startup. It touches CODDY_MCP_MARKER_FILE before speaking
// any protocol, exactly like the proof of concept in the report, and then
// serves a minimal stdio MCP server so an approved run connects cleanly.
func TestHelperMCPMarkerServer(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_MARKER") != "1" {
		t.Skip("helper process")
	}
	if path := os.Getenv("CODDY_MCP_MARKER_FILE"); path != "" {
		_ = os.WriteFile(path, []byte("CODDY_PROJECT_MCP_STARTED\n"), 0o600)
	}
	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	respond := func(id interface{}, result interface{}) {
		msg, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": result})
		_, _ = out.Write(append(msg, '\n'))
		_ = out.Flush()
	}
	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			os.Exit(0)
		}
		var req struct {
			ID     interface{} `json:"id"`
			Method string      `json:"method"`
		}
		if err := json.Unmarshal(line, &req); err != nil || req.ID == nil {
			continue
		}
		switch req.Method {
		case "initialize":
			respond(req.ID, map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"serverInfo":      map[string]interface{}{"name": "marker", "version": "0.0.1"},
			})
		case "tools/list":
			respond(req.ID, map[string]interface{}{"tools": []map[string]interface{}{{
				"name":        "noop",
				"description": "Does nothing",
				"inputSchema": map[string]interface{}{"type": "object"},
			}}})
		default:
			respond(req.ID, nil)
		}
	}
}

type mcpTrustState struct {
	root            string
	home            string
	cwd             string
	cfg             *config.Config
	mgr             *session.Manager
	markerPath      string
	projectOriginal []byte
	store           *session.FileStore
	storedID        string
	cleanup         []func()
}

func (s *mcpTrustState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-mcp-trust-*")
	if err != nil {
		return err
	}
	s.cleanup = append(s.cleanup, func() { _ = os.RemoveAll(root) })
	s.root = root
	s.home = filepath.Join(root, "home")
	s.cwd = filepath.Join(root, "workspace")
	for _, dir := range []string{s.home, s.cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	s.cfg = &config.Config{
		Paths:     config.Paths{Home: s.home, CWD: s.cwd},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 200}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return "", nil
	}
	s.store = &session.FileStore{Root: filepath.Join(root, "sessions")}
	s.mgr = session.NewManager(s.cfg, noopSender{}, runner, slog.Default(), s.cwd, s.store)
	return nil
}

func (s *mcpTrustState) close() {
	for _, fn := range s.cleanup {
		fn()
	}
	s.cleanup = nil
	s.mgr = nil
}

// markerServerEntry is the mcp.json entry that runs the marker helper.
func (s *mcpTrustState) markerServerEntry(marker string) config.MCPJSONServer {
	return config.MCPJSONServer{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperMCPMarkerServer"},
		Env: map[string]string{
			"GO_WANT_MCP_MARKER":    "1",
			"CODDY_MCP_MARKER_FILE": marker,
		},
	}
}

// ---- steps ----

func (s *mcpTrustState) projectMCPJSONRunsMarker() error {
	s.markerPath = filepath.Join(s.root, "marker-1.txt")
	path := config.MCPJSONPath(s.cwd)
	if err := config.UpsertMCPJSONServer(path, "marker", s.markerServerEntry(s.markerPath)); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	s.projectOriginal = data
	return err
}

func (s *mcpTrustState) disableServer(name string) error {
	return s.mgr.SetMCPEnabled(context.Background(), s.cwd, name, "", false)
}

func (s *mcpTrustState) projectUnchanged() error {
	data, err := os.ReadFile(config.MCPJSONPath(s.cwd))
	if err != nil {
		return err
	}
	if string(data) != string(s.projectOriginal) {
		return fmt.Errorf("project mcp.json changed after disabling server")
	}
	return nil
}

func (s *mcpTrustState) globalMCPJSONRunsMarker() error {
	s.markerPath = filepath.Join(s.root, "marker-global.txt")
	return config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(s.home), "marker", s.markerServerEntry(s.markerPath))
}

func (s *mcpTrustState) projectMCPJSONRewritten() error {
	s.markerPath = filepath.Join(s.root, "marker-2.txt")
	return config.UpsertMCPJSONServer(config.MCPJSONPath(s.cwd), "marker", s.markerServerEntry(s.markerPath))
}

func (s *mcpTrustState) operatorApproved(name string) error {
	srv, err := s.managed(name)
	if err != nil {
		return err
	}
	return mcp.NewTrustGate(s.cfg).Approve(s.cwd, *srv)
}

func (s *mcpTrustState) managed(name string) (*mcp.ManagedServer, error) {
	servers, err := mcp.ListManagedServers(s.cfg, s.cwd)
	if err != nil {
		return nil, err
	}
	for i := range servers {
		if servers[i].Config.Name == name {
			return &servers[i], nil
		}
	}
	return nil, fmt.Errorf("mcp server %q not in the merged list", name)
}

func (s *mcpTrustState) createSession() error {
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.cwd})
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	s.mgr.ForgetLiveSession(res.SessionID)
	return nil
}

func (s *mcpTrustState) liveSession() error {
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.cwd})
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	id := res.SessionID
	s.cleanup = append(s.cleanup, func() { s.mgr.ForgetLiveSession(id) })
	return nil
}

// settingsSaved is what PUT /coddy/config does to the live manager. The
// reconnect it triggers must re-evaluate trust rather than replay the merged
// list, so a project declaration nobody approved still does not spawn.
func (s *mcpTrustState) settingsSaved() error {
	next := *s.cfg
	next.MCPServers = []config.MCPServerConfig{{
		Name:     "unrelated",
		Command:  os.Args[0],
		Disabled: true,
	}}
	s.mgr.ReplaceConfig(&next)
	return nil
}

// storedSession writes a session bundle and drops it from memory, the way
// sessions a server restart or an earlier one-off run leave on disk.
func (s *mcpTrustState) storedSession() error {
	res, err := s.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.cwd})
	if err != nil {
		return fmt.Errorf("session/new: %w", err)
	}
	s.storedID = res.SessionID
	s.mgr.ForgetLiveSession(res.SessionID)
	if !s.store.HasPersistedSnapshot(res.SessionID) {
		return fmt.Errorf("session %s was not stored", res.SessionID)
	}
	return nil
}

// webPageReadsStoredSession is what a /coddy/sessions/{id}/... read does to a
// session that is only on disk: it loads it into the manager.
func (s *mcpTrustState) webPageReadsStoredSession() error {
	st, err := s.mgr.EnsureHTTPSession(context.Background(), s.storedID, s.cwd)
	if err != nil {
		return err
	}
	s.cleanup = append(s.cleanup, func() { s.mgr.ForgetLiveSession(s.storedID) })
	_ = st.GetMessages()
	return nil
}

func (s *mcpTrustState) turnRunsOnStoredSession() error {
	_, err := s.mgr.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: s.storedID,
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "go on"}},
	}, noopSender{}, nil)
	return err
}

func (s *mcpTrustState) markerHasRun() error {
	if _, err := os.Stat(s.markerPath); err != nil {
		return fmt.Errorf("marker %s missing: the approved MCP server did not start", s.markerPath)
	}
	return nil
}

func (s *mcpTrustState) markerHasNotRun() error {
	if _, err := os.Stat(s.markerPath); err == nil {
		return fmt.Errorf("marker %s exists: the MCP command ran", s.markerPath)
	}
	return nil
}

func (s *mcpTrustState) reportedAwaitingApproval(name string) error {
	srv, err := s.managed(name)
	if err != nil {
		return err
	}
	got := mcp.NewTrustGate(s.cfg).Evaluate(s.cwd, *srv)
	if got != mcp.TrustStateNeedsApproval {
		return fmt.Errorf("trust state of %q = %q, want %q", name, got, mcp.TrustStateNeedsApproval)
	}
	return nil
}

func initializeMCPTrustScenario(sc *godog.ScenarioContext) {
	s := &mcpTrustState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a workspace whose project mcp\.json runs a marker command$`, s.projectMCPJSONRunsMarker)
	sc.Step(`^a workspace whose global mcp\.json runs a marker command$`, s.globalMCPJSONRunsMarker)
	sc.Step(`^the project mcp\.json is rewritten to run a different marker command$`, s.projectMCPJSONRewritten)
	sc.Step(`^the operator approved the project MCP server "([^"]*)" for that workspace$`, s.operatorApproved)
	sc.Step(`^the operator disables the MCP server "([^"]*)"$`, s.disableServer)
	sc.Step(`^the project mcp\.json is unchanged$`, s.projectUnchanged)
	sc.Step(`^an ACP client creates a session for that workspace$`, s.createSession)
	sc.Step(`^an ACP client has a live session for that workspace$`, s.liveSession)
	sc.Step(`^the operator saves settings that change the configured MCP servers$`, s.settingsSaved)
	sc.Step(`^the marker command has run$`, s.markerHasRun)
	sc.Step(`^a stored session for a workspace$`, s.storedSession)
	sc.Step(`^a web page reads that stored session$`, s.webPageReadsStoredSession)
	sc.Step(`^a turn runs on that stored session$`, s.turnRunsOnStoredSession)
	sc.Step(`^the marker command has not run$`, s.markerHasNotRun)
	sc.Step(`^coddy reports the project MCP server "([^"]*)" as awaiting approval$`, s.reportedAwaitingApproval)
}

func TestMCPSessionRestoreFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "mcp-session-restore",
		ScenarioInitializer: initializeMCPTrustScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/mcp_session_restore.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("mcp session restore feature failed")
	}
}

func TestMCPProjectTrustACP(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "mcp_project_trust_acp",
		ScenarioInitializer: initializeMCPTrustScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/mcp_project_trust.feature"},
			Tags:     "@acp",
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("mcp_project_trust @acp feature failed")
	}
}
