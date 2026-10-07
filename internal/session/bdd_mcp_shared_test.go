package session_test

// Godog harness for features/mcp_shared_servers.feature. It drives the
// session manager the way coddy serve does - StartGlobalMCPServers, then
// session/new for the surfaces' sessions - against a stdio stub that records
// every start and every exit, and checks how many processes the sessions ran.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// sharedMCPRecordEnv names the file TestHelperMCPSharedServer records its
// starts and exits in.
const sharedMCPRecordEnv = "CODDY_TEST_SHARED_MCP_RECORD"

// TestHelperMCPSharedServer is not a real test: re-executed with
// CODDY_TEST_SHARED_MCP_RECORD set it is a stdio MCP server with one tool,
// "ping", that answers "pong". It records "started" when it runs and
// "exited" when its stdin closes.
func TestHelperMCPSharedServer(t *testing.T) {
	record := os.Getenv(sharedMCPRecordEnv)
	if record == "" {
		t.Skip("helper process")
	}
	appendRecord(record, "started", os.O_CREATE)
	in := bufio.NewScanner(os.Stdin)
	out := json.NewEncoder(os.Stdout)
	for in.Scan() {
		var req struct {
			ID     interface{} `json:"id"`
			Method string      `json:"method"`
		}
		if err := json.Unmarshal(in.Bytes(), &req); err != nil || req.ID == nil {
			continue
		}
		var result interface{} = map[string]interface{}{}
		switch req.Method {
		case "initialize":
			result = map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"serverInfo":      map[string]interface{}{"name": "shared", "version": "1"},
			}
		case "tools/list":
			result = map[string]interface{}{"tools": []map[string]interface{}{{
				"name": "ping", "description": "Answer pong", "inputSchema": map[string]interface{}{"type": "object"},
			}}}
		case "tools/call":
			result = map[string]interface{}{"content": []map[string]interface{}{{"type": "text", "text": "pong"}}}
		}
		_ = out.Encode(map[string]interface{}{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}
	// Never created anew: a server stopped as the scenario ends must not put
	// a file back into a folder being removed.
	appendRecord(record, "exited", 0)
	os.Exit(0)
}

func appendRecord(path, line string, create int) {
	if f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|create, 0o644); err == nil {
		_, _ = f.WriteString(line + "\n")
		_ = f.Close()
	}
}

type mcpSharedState struct {
	root       string
	cfg        *config.Config
	mgr        *session.Manager
	record     string
	workspaces [2]string
	// sessions are the open sessions by workspace index.
	sessions [2][]*session.State
}

func (s *mcpSharedState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-mcp-shared-*")
	if err != nil {
		return err
	}
	s.root = root
	home := filepath.Join(root, "home")
	s.workspaces = [2]string{filepath.Join(root, "first"), filepath.Join(root, "second")}
	for _, dir := range []string{home, s.workspaces[0], s.workspaces[1]} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	s.record = filepath.Join(root, "record")
	s.cfg = &config.Config{
		Paths:     config.Paths{Home: home},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 200}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	return nil
}

func (s *mcpSharedState) close() {
	for _, list := range s.sessions {
		for _, st := range list {
			st.CloseAll()
		}
	}
	s.sessions = [2][]*session.State{}
	if s.mgr != nil {
		s.mgr.CloseMCP()
		s.mgr = nil
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

// server declares the recording stub under name.
func (s *mcpSharedState) server(name string) config.MCPServerConfig {
	return config.MCPServerConfig{
		Type:    "stdio",
		Name:    name,
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestHelperMCPSharedServer$"},
		Env:     []config.EnvVarConfig{{Name: sharedMCPRecordEnv, Value: s.record}},
	}
}

// manager builds the manager on first use, once the configuration is set.
func (s *mcpSharedState) manager() *session.Manager {
	if s.mgr == nil {
		runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
			return "", nil
		}
		s.mgr = session.NewManager(s.cfg, noopSender{}, runner, slog.Default(), s.workspaces[0], nil)
	}
	return s.mgr
}

func (s *mcpSharedState) count(kind string) int {
	data, err := os.ReadFile(s.record)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), kind+"\n")
}

func (s *mcpSharedState) waitFor(what string, cond func() bool) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cond() {
		return nil
	}
	return fmt.Errorf("timed out waiting until %s", what)
}

// running reports whether the pool runs name, for the workspace when one is
// given.
func (s *mcpSharedState) running(name, workspace string) bool {
	want := ""
	if workspace != "" {
		want = mcp.CanonicalWorkspace(workspace)
	}
	for _, srv := range s.manager().MCPPoolServers() {
		if srv.Name == name && srv.Connected && (workspace == "" || srv.Workspace == want) {
			return true
		}
	}
	return false
}

func (s *mcpSharedState) openSession(i int) error {
	res, err := s.manager().HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.workspaces[i]})
	if err != nil {
		return err
	}
	st := s.manager().SessionByID(res.SessionID)
	if st == nil {
		return fmt.Errorf("session %s is not live", res.SessionID)
	}
	s.sessions[i] = append(s.sessions[i], st)
	return nil
}

func (s *mcpSharedState) globalDeclares(name string) error {
	return config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(s.cfg.Paths.Home), name, config.MCPJSONFromServer(s.server(name)))
}

func (s *mcpSharedState) startGlobals() error {
	s.manager().StartGlobalMCPServers()
	return nil
}

func (s *mcpSharedState) runsBeforeAnySession(name string) error {
	if err := s.waitFor("the server runs", func() bool { return s.running(name, "") }); err != nil {
		return err
	}
	if n := len(s.sessions[0]) + len(s.sessions[1]); n != 0 {
		return fmt.Errorf("%d sessions are open", n)
	}
	return nil
}

func (s *mcpSharedState) sessionInFirst() error  { return s.openSession(0) }
func (s *mcpSharedState) sessionInSecond() error { return s.openSession(1) }

func (s *mcpSharedState) twoSessionsInFirst() error {
	if err := s.openSession(0); err != nil {
		return err
	}
	return s.openSession(0)
}

func (s *mcpSharedState) bothSessionsCall(name string) error {
	for _, list := range s.sessions {
		for _, st := range list {
			var client *mcp.Client
			for _, c := range st.GetMCPClients() {
				if c.Name() == name {
					client = c
				}
			}
			if client == nil {
				return fmt.Errorf("session %s has no client for %q", st.GetID(), name)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			got, err := client.CallTool(ctx, "ping", "{}")
			cancel()
			if err != nil || got != "pong" {
				return fmt.Errorf("session %s called %q: %q, %v", st.GetID(), name, got, err)
			}
		}
	}
	return nil
}

func (s *mcpSharedState) startedTimes(want int) func(string) error {
	return func(string) error {
		if got := s.count("started"); got != want {
			return fmt.Errorf("the server was started %d times, want %d", got, want)
		}
		return nil
	}
}

func (s *mcpSharedState) closeWorkspace(i int) error {
	for _, st := range s.sessions[i] {
		st.CloseAll()
	}
	s.sessions[i] = nil
	return nil
}

func (s *mcpSharedState) bothSessionsClose() error {
	if err := s.closeWorkspace(0); err != nil {
		return err
	}
	return s.closeWorkspace(1)
}

func (s *mcpSharedState) stillRunning(name string) error {
	time.Sleep(300 * time.Millisecond)
	if got := s.count("exited"); got != 0 {
		return fmt.Errorf("the server exited %d times", got)
	}
	if !s.running(name, "") {
		return fmt.Errorf("the pool no longer runs %q", name)
	}
	return nil
}

func (s *mcpSharedState) projectDeclaresApproved(name string) error {
	srv := s.server(name)
	env := map[string]string{}
	for _, e := range srv.Env {
		env[e.Name] = e.Value
	}
	gate := mcp.NewTrustGate(s.cfg)
	for _, ws := range s.workspaces {
		if err := config.UpsertMCPJSONServer(config.MCPJSONPath(ws), name, config.MCPJSONServer{Command: srv.Command, Args: srv.Args, Env: env}); err != nil {
			return err
		}
		servers, err := mcp.ListManagedServers(s.cfg, ws)
		if err != nil {
			return err
		}
		for _, managed := range servers {
			if managed.Config.Name == name {
				if err := gate.Approve(ws, managed); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *mcpSharedState) idleTimeout(seconds int) error {
	s.cfg.MCP.IdleTimeoutSeconds = &seconds
	return nil
}

func (s *mcpSharedState) firstWorkspaceStopped(name string) error {
	return s.waitFor("the first workspace's server exited", func() bool {
		return s.count("exited") == 1 && !s.running(name, s.workspaces[0])
	})
}

func (s *mcpSharedState) secondWorkspaceRunning(name string) error {
	time.Sleep(300 * time.Millisecond)
	if got := s.count("exited"); got != 1 {
		return fmt.Errorf("%d servers exited, want only the first workspace's", got)
	}
	if !s.running(name, s.workspaces[1]) {
		return fmt.Errorf("the pool no longer runs %q for the second workspace", name)
	}
	return nil
}

func initializeMCPSharedScenario(sc *godog.ScenarioContext) {
	s := &mcpSharedState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^the home mcp\.json declares the MCP server "([^"]*)"$`, s.globalDeclares)
	sc.Step(`^the process starts its global MCP servers$`, s.startGlobals)
	sc.Step(`^the MCP server "([^"]*)" runs before any session opens$`, s.runsBeforeAnySession)
	sc.Step(`^a session opens in one workspace$`, s.sessionInFirst)
	sc.Step(`^another session opens in another workspace$`, s.sessionInSecond)
	sc.Step(`^both sessions call the MCP server "([^"]*)"$`, s.bothSessionsCall)
	sc.Step(`^the MCP server "([^"]*)" was started once$`, s.startedTimes(1))
	sc.Step(`^the MCP server "([^"]*)" was started twice$`, s.startedTimes(2))
	sc.Step(`^both sessions close$`, s.bothSessionsClose)
	sc.Step(`^the MCP server "([^"]*)" is still running$`, s.stillRunning)
	sc.Step(`^two workspaces whose project mcp\.json declares the approved MCP server "([^"]*)"$`, s.projectDeclaresApproved)
	sc.Step(`^an MCP server no session holds stops after (\d+) seconds?$`, s.idleTimeout)
	sc.Step(`^two sessions open in the first workspace$`, s.twoSessionsInFirst)
	sc.Step(`^a session opens in the second workspace$`, s.sessionInSecond)
	sc.Step(`^both sessions of the first workspace close$`, func() error { return s.closeWorkspace(0) })
	sc.Step(`^the MCP server "([^"]*)" of the first workspace has stopped$`, s.firstWorkspaceStopped)
	sc.Step(`^the MCP server "([^"]*)" of the second workspace is still running$`, s.secondWorkspaceRunning)
}

func TestMCPSharedServersFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "mcp-shared-servers",
		ScenarioInitializer: initializeMCPSharedScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/mcp_shared_servers.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("mcp shared servers feature failed")
	}
}
