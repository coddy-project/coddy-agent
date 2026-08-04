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
	root       string
	home       string
	cwd        string
	cfg        *config.Config
	mgr        *session.Manager
	markerPath string
	cleanup    []func()
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
	s.mgr = session.NewManager(s.cfg, noopSender{}, runner, slog.Default(), s.cwd, nil)
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
	return config.UpsertMCPJSONServer(config.MCPJSONPath(s.cwd), "marker", s.markerServerEntry(s.markerPath))
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

func (s *mcpTrustState) markerHasRun() error {
	if _, err := os.Stat(s.markerPath); err != nil {
		return fmt.Errorf("marker %s missing: the approved MCP server did not start", s.markerPath)
	}
	return nil
}

func (s *mcpTrustState) markerHasNotRun() error {
	if _, err := os.Stat(s.markerPath); err == nil {
		return fmt.Errorf("marker %s exists: the project MCP command ran without approval", s.markerPath)
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
	sc.Step(`^an ACP client creates a session for that workspace$`, s.createSession)
	sc.Step(`^the marker command has run$`, s.markerHasRun)
	sc.Step(`^the marker command has not run$`, s.markerHasNotRun)
	sc.Step(`^coddy reports the project MCP server "([^"]*)" as awaiting approval$`, s.reportedAwaitingApproval)
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
