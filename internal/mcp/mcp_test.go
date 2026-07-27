package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// TestHelperMCPServer is not a real test: when re-executed with
// GO_WANT_MCP_HELPER=1 it becomes a minimal MCP stdio server speaking
// newline-delimited JSON-RPC with two tools (echo, reverse).
func TestHelperMCPServer(t *testing.T) {
	if os.Getenv("GO_WANT_MCP_HELPER") != "1" {
		t.Skip("helper process")
	}
	runFakeMCPServer()
	os.Exit(0)
}

func runFakeMCPServer() {
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
			return
		}
		var req struct {
			ID     interface{}     `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &req); err != nil || req.ID == nil {
			continue // notification or garbage
		}
		switch req.Method {
		case "initialize":
			respond(req.ID, map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"serverInfo":      map[string]interface{}{"name": "fake-mcp", "version": "0.0.1"},
			})
		case "tools/list":
			schema := map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"text": map[string]interface{}{"type": "string"}},
			}
			respond(req.ID, map[string]interface{}{"tools": []map[string]interface{}{
				{"name": "echo", "description": "Echo text back", "inputSchema": schema},
				{"name": "reverse", "description": "Reverse text", "inputSchema": schema},
			}})
		case "tools/call":
			var params struct {
				Name      string            `json:"name"`
				Arguments map[string]string `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &params)
			text := params.Arguments["text"]
			if params.Name == "reverse" {
				r := []rune(text)
				for i, j := 0, len(r)-1; i < j; i, j = i+1, j-1 {
					r[i], r[j] = r[j], r[i]
				}
				text = string(r)
			}
			respond(req.ID, map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": text}},
			})
		default:
			respond(req.ID, nil)
		}
	}
}

// fakeServerConfig returns an MCPServerConfig that re-executes this test
// binary as the fake MCP server above.
func fakeServerConfig(name string) config.MCPServerConfig {
	return config.MCPServerConfig{
		Name:    name,
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperMCPServer"},
		Env:     []config.EnvVarConfig{{Name: "GO_WANT_MCP_HELPER", Value: "1"}},
	}
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestStdioClientAgainstFakeServer(t *testing.T) {
	srv := fakeServerConfig("fake")
	client, err := NewStdioClient(testCtx(t), srv.Name, srv.Command, srv.Args, []string{"GO_WANT_MCP_HELPER=1"}, slog.Default())
	if err != nil {
		t.Fatalf("NewStdioClient: %v", err)
	}
	defer func() { _ = client.Close() }()

	tools := client.Tools()
	if len(tools) != 2 || tools[0].Name != "echo" || tools[1].Name != "reverse" {
		t.Fatalf("tools = %+v, want echo+reverse", tools)
	}
	got, err := client.CallTool(testCtx(t), "reverse", `{"text":"abc"}`)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if got != "cba" {
		t.Fatalf("reverse = %q, want cba", got)
	}
}

func TestProbeListsTools(t *testing.T) {
	tools, err := Probe(testCtx(t), fakeServerConfig("fake"), t.TempDir(), slog.Default())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %+v, want 2", tools)
	}
}

func TestProbeRejectsNonStdio(t *testing.T) {
	srv := config.MCPServerConfig{Name: "remote", Type: "http", URL: "https://example.com"}
	if _, err := Probe(testCtx(t), srv, t.TempDir(), slog.Default()); err == nil {
		t.Fatal("http transport must be rejected")
	}
}

func TestProbeBadCommand(t *testing.T) {
	srv := config.MCPServerConfig{Name: "bad", Command: "/nonexistent-mcp-binary"}
	if _, err := Probe(testCtx(t), srv, t.TempDir(), slog.Default()); err == nil {
		t.Fatal("bad command must error")
	}
}

// ---- management operations ----

// writeTestConfig writes a config.yaml with one global MCP server and loads it.
func writeTestConfig(t *testing.T) (*config.Config, string) {
	t.Helper()
	home := t.TempDir()
	cfgPath := home + "/config.yaml"
	yaml := `
mcp_servers:
  - name: global-srv
    command: global-mcp
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	return cfg, cfgPath
}

func TestListManagedServersMergesSources(t *testing.T) {
	cfg, _ := writeTestConfig(t)
	cwd := t.TempDir()
	if err := config.UpsertProjectMCPServer(cwd, "proj-srv", config.ProjectMCPServer{Command: "proj-mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertProjectMCPServer(cwd, "global-srv", config.ProjectMCPServer{Command: "override-mcp"}); err != nil {
		t.Fatal(err)
	}

	servers, err := ListManagedServers(cfg, cwd)
	if err != nil {
		t.Fatalf("ListManagedServers: %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("servers = %+v, want 2", servers)
	}
	bySource := map[string]string{}
	for _, s := range servers {
		bySource[s.Config.Name] = s.Source
	}
	// A project override wins and is labeled project; untouched globals stay config.
	if bySource["global-srv"] != "project" {
		t.Errorf("global-srv source = %q, want project (overridden)", bySource["global-srv"])
	}
	if bySource["proj-srv"] != "project" {
		t.Errorf("proj-srv source = %q, want project", bySource["proj-srv"])
	}

	// Without the override the global stays config-sourced.
	if _, err := config.DeleteProjectMCPServer(cwd, "global-srv"); err != nil {
		t.Fatal(err)
	}
	servers, _ = ListManagedServers(cfg, cwd)
	for _, s := range servers {
		if s.Config.Name == "global-srv" && s.Source != "config" {
			t.Errorf("global-srv source = %q, want config", s.Source)
		}
	}
}

func TestSetServerDisabledPersistsToOwningFile(t *testing.T) {
	cfg, cfgPath := writeTestConfig(t)
	cwd := t.TempDir()
	if err := config.UpsertProjectMCPServer(cwd, "proj-srv", config.ProjectMCPServer{Command: "proj-mcp"}); err != nil {
		t.Fatal(err)
	}

	// Project-sourced toggle lands in .coddy/mcp.json.
	if err := SetServerDisabled(cfg, cwd, "proj-srv", true); err != nil {
		t.Fatalf("disable project server: %v", err)
	}
	entries, _ := config.ReadProjectMCPFile(cwd)
	if !entries["proj-srv"].Disabled {
		t.Errorf("proj-srv not disabled in mcp.json: %+v", entries)
	}

	// Config-sourced toggle lands in config.yaml.
	if err := SetServerDisabled(cfg, cwd, "global-srv", true); err != nil {
		t.Fatalf("disable global server: %v", err)
	}
	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.MCPServers) != 1 || !reloaded.MCPServers[0].Disabled {
		t.Errorf("config.yaml servers = %+v, want global-srv disabled", reloaded.MCPServers)
	}

	if err := SetServerDisabled(cfg, cwd, "ghost", true); err == nil {
		t.Error("unknown server must error")
	}
}

func TestSetToolDisabledPersistsToOwningFile(t *testing.T) {
	cfg, cfgPath := writeTestConfig(t)
	cwd := t.TempDir()
	if err := config.UpsertProjectMCPServer(cwd, "proj-srv", config.ProjectMCPServer{Command: "proj-mcp"}); err != nil {
		t.Fatal(err)
	}

	if err := SetToolDisabled(cfg, cwd, "proj-srv", "echo", true); err != nil {
		t.Fatalf("disable project tool: %v", err)
	}
	entries, _ := config.ReadProjectMCPFile(cwd)
	if got := entries["proj-srv"].DisabledTools; len(got) != 1 || got[0] != "echo" {
		t.Errorf("proj-srv disabledTools = %v, want [echo]", got)
	}

	if err := SetToolDisabled(cfg, cwd, "global-srv", "reverse", true); err != nil {
		t.Fatalf("disable global tool: %v", err)
	}
	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.MCPServers[0].DisabledTools; len(got) != 1 || got[0] != "reverse" {
		t.Errorf("config.yaml disabled_tools = %v, want [reverse]", got)
	}

	// Re-enable removes the entry again.
	if err := SetToolDisabled(cfg, cwd, "global-srv", "reverse", false); err != nil {
		t.Fatal(err)
	}
	reloaded, _ = config.Load(cfgPath)
	if got := reloaded.MCPServers[0].DisabledTools; len(got) != 0 {
		t.Errorf("config.yaml disabled_tools = %v, want empty", got)
	}
}

func TestDeleteServerRefusesConfigSourced(t *testing.T) {
	cfg, _ := writeTestConfig(t)
	cwd := t.TempDir()
	if err := config.UpsertProjectMCPServer(cwd, "proj-srv", config.ProjectMCPServer{Command: "proj-mcp"}); err != nil {
		t.Fatal(err)
	}

	if err := DeleteServer(cfg, cwd, "proj-srv"); err != nil {
		t.Fatalf("delete project server: %v", err)
	}
	entries, _ := config.ReadProjectMCPFile(cwd)
	if _, ok := entries["proj-srv"]; ok {
		t.Errorf("proj-srv still present: %+v", entries)
	}

	if err := DeleteServer(cfg, cwd, "global-srv"); err == nil {
		t.Error("config-sourced server must refuse API deletion")
	}
	if err := DeleteServer(cfg, cwd, "ghost"); err == nil {
		t.Error("unknown server must error")
	}
}

func TestValidateServerName(t *testing.T) {
	for _, ok := range []string{"files", "my-server", "srv1"} {
		if err := ValidateServerName(ok); err != nil {
			t.Errorf("ValidateServerName(%q) = %v, want nil", ok, err)
		}
	}
	// "__" is the tool-namespace separator; spaces and separators break lookups.
	for _, bad := range []string{"", "a__b", "a b", "a/b", "a\\b", "  "} {
		if err := ValidateServerName(bad); err == nil {
			t.Errorf("ValidateServerName(%q) = nil, want error", bad)
		}
	}
}
