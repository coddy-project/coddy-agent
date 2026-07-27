//go:build http

package httpserver

// Godog harness for features/mcp_management.feature: drives the /coddy/mcp
// HTTP surface against a project .coddy/mcp.json and a fake stdio MCP server
// (this test binary re-executed via TestHelperMCPServerHTTP).

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// TestHelperMCPServerHTTP is not a real test: re-executed with
// GO_WANT_MCP_HELPER=1 it becomes a minimal MCP stdio server with one tool
// (echo) speaking newline-delimited JSON-RPC.
func TestHelperMCPServerHTTP(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		t.Skip("helper process")
	}
	in := bufio.NewReader(os.Stdin)
	out := bufio.NewWriter(os.Stdout)
	respond := func(id interface{}, result interface{}) {
		msg := map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": result}
		data, _ := json.Marshal(msg)
		_, _ = out.Write(append(data, '\n'))
		_ = out.Flush()
	}
	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			os.Exit(0)
		}
		var req struct {
			ID     interface{}     `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil || req.ID == nil {
			continue
		}
		switch req.Method {
		case "initialize":
			respond(req.ID, map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"serverInfo":      map[string]interface{}{"name": "fake-mcp", "version": "0.0.1"},
			})
		case "tools/list":
			respond(req.ID, map[string]interface{}{"tools": []map[string]interface{}{
				{
					"name":        "echo",
					"description": "Echo text back",
					"inputSchema": map[string]interface{}{
						"type":       "object",
						"properties": map[string]interface{}{"text": map[string]interface{}{"type": "string"}},
					},
				},
				{
					"name":        "get_token",
					"description": "Return this server's token",
					"inputSchema": map[string]interface{}{"type": "object"},
				},
			}})
		case "tools/call":
			var params struct {
				Name string `json:"name"`
			}
			_ = json.Unmarshal(req.Params, &params)
			text := "ok"
			if params.Name == "get_token" {
				text = os.Getenv("MCP_HELPER_TOKEN")
				if text == "" {
					text = "NO-TOKEN"
				}
			}
			respond(req.ID, map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": text}},
			})
		default:
			respond(req.ID, nil)
		}
	}
}

type mcpFeatureState struct {
	root     string
	home     string
	cwd      string
	ts       *httptest.Server
	mgr      *session.Manager
	srv      *Server
	status   int
	body     map[string]interface{}
	prevHOME string
}

func (s *mcpFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-bdd-mcp-*")
	if err != nil {
		return err
	}
	s.root = root
	s.status = 0
	s.body = nil
	return nil
}

func (s *mcpFeatureState) close() {
	if s.ts != nil {
		s.ts.Close()
		s.ts = nil
	}
	if s.srv != nil {
		s.srv.Drain()
		s.srv = nil
	}
	if s.prevHOME != "" {
		_ = os.Setenv("CODDY_HOME", s.prevHOME)
	} else if s.root != "" {
		_ = os.Unsetenv("CODDY_HOME")
	}
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *mcpFeatureState) startServer() error {
	s.home = filepath.Join(s.root, "home")
	s.cwd = filepath.Join(s.root, "workspace")
	for _, dir := range []string{s.home, s.cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	s.prevHOME = os.Getenv("CODDY_HOME")
	if err := os.Setenv("CODDY_HOME", s.home); err != nil {
		return err
	}
	cfgPath := filepath.Join(s.home, "config.yaml")
	if err := os.WriteFile(cfgPath, []byte("skills:\n  sources: []\n"), 0o644); err != nil {
		return err
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	s.mgr = session.NewManager(cfg, noopSender{}, runner, slog.Default(), s.cwd, nil)
	s.srv = New(cfg, s.mgr, slog.Default(), s.cwd)
	s.ts = httptest.NewServer(s.srv.Handler())
	return nil
}

func (s *mcpFeatureState) do(method, path string, body interface{}) error {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, s.ts.URL+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	s.status = res.StatusCode
	raw, _ := io.ReadAll(res.Body)
	s.body = map[string]interface{}{}
	if len(bytes.TrimSpace(raw)) > 0 {
		_ = json.Unmarshal(raw, &s.body)
	}
	return nil
}

// fakeMCPEntry returns an mcp.json entry re-executing this test binary as
// the fake MCP server above.
func fakeMCPEntry() config.MCPJSONServer {
	return config.MCPJSONServer{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperMCPServerHTTP"},
		Env:     map[string]string{"GO_WANT_MCP_HELPER": "1"},
	}
}

// ---- steps ----

func (s *mcpFeatureState) givenProjectServer(name string) error {
	return config.UpsertMCPJSONServer(config.MCPJSONPath(s.cwd), name, fakeMCPEntry())
}

func (s *mcpFeatureState) givenGlobalServer(name string) error {
	return config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(s.home), name, fakeMCPEntry())
}

func (s *mcpFeatureState) listServers() error {
	if err := s.do(http.MethodGet, "/coddy/mcp", nil); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("list status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *mcpFeatureState) serverRow(name string) (map[string]interface{}, error) {
	if err := s.listServers(); err != nil {
		return nil, err
	}
	items, _ := s.body["items"].([]interface{})
	for _, it := range items {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		if n, _ := m["name"].(string); n == name {
			return m, nil
		}
	}
	return nil, fmt.Errorf("server %q not in list %v", name, s.body["items"])
}

func (s *mcpFeatureState) listShowsServer(name, source string) error {
	row, err := s.serverRow(name)
	if err != nil {
		return err
	}
	if got, _ := row["source"].(string); got != source {
		return fmt.Errorf("server %q source = %q, want %q", name, got, source)
	}
	if enabled, _ := row["enabled"].(bool); !enabled {
		return fmt.Errorf("server %q not enabled: %v", name, row)
	}
	return nil
}

func (s *mcpFeatureState) listShowsServerDisabled(name string) error {
	row, err := s.serverRow(name)
	if err != nil {
		return err
	}
	if enabled, _ := row["enabled"].(bool); enabled {
		return fmt.Errorf("server %q still enabled: %v", name, row)
	}
	if status, _ := row["status"].(string); status != "disabled" {
		return fmt.Errorf("server %q status = %q, want disabled", name, status)
	}
	return nil
}

func (s *mcpFeatureState) listDoesNotShowServer(name string) error {
	if err := s.listServers(); err != nil {
		return err
	}
	items, _ := s.body["items"].([]interface{})
	for _, it := range items {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		if n, _ := m["name"].(string); n == name {
			return fmt.Errorf("server %q unexpectedly present: %v", name, m)
		}
	}
	return nil
}

func (s *mcpFeatureState) serverToolState(server, tool string, wantEnabled bool) error {
	row, err := s.serverRow(server)
	if err != nil {
		return err
	}
	tools, _ := row["tools"].([]interface{})
	for _, it := range tools {
		m, ok := it.(map[string]interface{})
		if !ok {
			continue
		}
		if n, _ := m["name"].(string); n == tool {
			if enabled, _ := m["enabled"].(bool); enabled != wantEnabled {
				return fmt.Errorf("tool %s__%s enabled = %v, want %v", server, tool, enabled, wantEnabled)
			}
			return nil
		}
	}
	return fmt.Errorf("tool %q not in server %q tools: %v", tool, server, row["tools"])
}

func (s *mcpFeatureState) toolEnabled(server, tool string) error {
	return s.serverToolState(server, tool, true)
}

func (s *mcpFeatureState) toolDisabled(server, tool string) error {
	return s.serverToolState(server, tool, false)
}

func (s *mcpFeatureState) toggle(path string) error {
	if err := s.do(http.MethodPost, path, nil); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("toggle %s status %d body %v", path, s.status, s.body)
	}
	return nil
}

func (s *mcpFeatureState) disableTool(tool, server string) error {
	return s.toggle("/coddy/mcp/" + url.PathEscape(server) + "/tools/" + url.PathEscape(tool) + "/disable")
}

func (s *mcpFeatureState) disableServer(server string) error {
	return s.toggle("/coddy/mcp/" + url.PathEscape(server) + "/disable")
}

func (s *mcpFeatureState) enableServer(server string) error {
	return s.toggle("/coddy/mcp/" + url.PathEscape(server) + "/enable")
}

func (s *mcpFeatureState) addServer(name string) error {
	entry := fakeMCPEntry()
	if err := s.do(http.MethodPut, "/coddy/mcp/"+url.PathEscape(name), entry); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("add server status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *mcpFeatureState) deleteServer(name string) error {
	if err := s.do(http.MethodDelete, "/coddy/mcp/"+url.PathEscape(name), nil); err != nil {
		return err
	}
	if s.status != http.StatusOK {
		return fmt.Errorf("delete server status %d body %v", s.status, s.body)
	}
	return nil
}

func (s *mcpFeatureState) fileRecordsDisabledTool(tool, server string) error {
	entries, err := config.ReadMCPJSONFile(config.MCPJSONPath(s.cwd))
	if err != nil {
		return err
	}
	for _, t := range entries[server].DisabledTools {
		if t == tool {
			return nil
		}
	}
	return fmt.Errorf("mcp.json %q disabledTools = %v, want %q", server, entries[server].DisabledTools, tool)
}

func (s *mcpFeatureState) fileRecordsServerDisabled(server string) error {
	entries, err := config.ReadMCPJSONFile(config.MCPJSONPath(s.cwd))
	if err != nil {
		return err
	}
	if !entries[server].Disabled {
		return fmt.Errorf("mcp.json %q not disabled: %+v", server, entries[server])
	}
	return nil
}

func (s *mcpFeatureState) fileContainsServer(server string) error {
	entries, err := config.ReadMCPJSONFile(config.MCPJSONPath(s.cwd))
	if err != nil {
		return err
	}
	if _, ok := entries[server]; !ok {
		return fmt.Errorf("mcp.json does not contain %q: %+v", server, entries)
	}
	return nil
}

func (s *mcpFeatureState) fileDoesNotContainServer(server string) error {
	entries, err := config.ReadMCPJSONFile(config.MCPJSONPath(s.cwd))
	if err != nil {
		return err
	}
	if _, ok := entries[server]; ok {
		return fmt.Errorf("mcp.json still contains %q: %+v", server, entries)
	}
	return nil
}

func initializeMCPScenario(sc *godog.ScenarioContext) {
	s := &mcpFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a running coddy HTTP server$`, s.startServer)
	sc.Step(`^a project mcp\.json defining the stdio server "([^"]*)"$`, s.givenProjectServer)
	sc.Step(`^a global mcp\.json defining the stdio server "([^"]*)"$`, s.givenGlobalServer)
	sc.Step(`^I list the MCP servers$`, s.listServers)
	sc.Step(`^I disable the tool "([^"]*)" of MCP server "([^"]*)"$`, s.disableTool)
	sc.Step(`^I disable the MCP server "([^"]*)"$`, s.disableServer)
	sc.Step(`^I enable the MCP server "([^"]*)"$`, s.enableServer)
	sc.Step(`^I add a project MCP server "([^"]*)" running the fake MCP command$`, s.addServer)
	sc.Step(`^I delete the MCP server "([^"]*)"$`, s.deleteServer)

	sc.Step(`^the MCP list shows server "([^"]*)" from source "([^"]*)" as enabled$`, s.listShowsServer)
	sc.Step(`^the MCP list shows server "([^"]*)" as disabled$`, s.listShowsServerDisabled)
	sc.Step(`^the MCP list does not show server "([^"]*)"$`, s.listDoesNotShowServer)
	sc.Step(`^server "([^"]*)" exposes the tool "([^"]*)" as enabled$`, func(server, tool string) error {
		return s.toolEnabled(server, tool)
	})
	sc.Step(`^server "([^"]*)" exposes the tool "([^"]*)" as disabled$`, func(server, tool string) error {
		return s.toolDisabled(server, tool)
	})
	sc.Step(`^the project mcp\.json records "([^"]*)" as a disabled tool of "([^"]*)"$`, s.fileRecordsDisabledTool)
	sc.Step(`^the project mcp\.json records server "([^"]*)" as disabled$`, s.fileRecordsServerDisabled)
	sc.Step(`^the project mcp\.json contains server "([^"]*)"$`, s.fileContainsServer)
	sc.Step(`^the project mcp\.json does not contain server "([^"]*)"$`, s.fileDoesNotContainServer)
}

func TestMCPManagementFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "mcp_management",
		ScenarioInitializer: initializeMCPScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/mcp_management.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("mcp_management feature failed")
	}
}
