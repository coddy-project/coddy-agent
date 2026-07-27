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

// writeTestConfig writes a config.yaml with one server and loads it, pinning
// Paths.Home to the temp dir so the global <home>/mcp.json lands there too.
func writeTestConfig(t *testing.T) (*config.Config, string, string) {
	t.Helper()
	home := t.TempDir()
	cfgPath := home + "/config.yaml"
	yaml := `
mcp_servers:
  - name: cfg-srv
    command: cfg-mcp
`
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	cfg.Paths.Home = home
	return cfg, cfgPath, home
}

func TestListManagedServersScopesAndOrigins(t *testing.T) {
	cfg, _, home := writeTestConfig(t)
	cwd := t.TempDir()
	globalPath := config.GlobalMCPJSONPath(home)
	projectPath := config.MCPJSONPath(cwd)

	// Global file adds home-srv and overrides cfg-srv; project file adds
	// proj-srv and overrides home-srv.
	for name, srv := range map[string]config.MCPJSONServer{
		"home-srv": {Command: "home-mcp"},
		"cfg-srv":  {Command: "home-override"},
	} {
		if err := config.UpsertMCPJSONServer(globalPath, name, srv); err != nil {
			t.Fatal(err)
		}
	}
	for name, srv := range map[string]config.MCPJSONServer{
		"proj-srv": {Command: "proj-mcp"},
		"home-srv": {Command: "proj-override"},
	} {
		if err := config.UpsertMCPJSONServer(projectPath, name, srv); err != nil {
			t.Fatal(err)
		}
	}

	servers, err := ListManagedServers(cfg, cwd)
	if err != nil {
		t.Fatalf("ListManagedServers: %v", err)
	}
	if len(servers) != 3 {
		t.Fatalf("servers = %+v, want 3", servers)
	}
	type so struct{ scope, origin, command string }
	got := map[string]so{}
	for _, s := range servers {
		got[s.Config.Name] = so{s.Scope, s.Origin, s.Config.Command}
	}
	if got["cfg-srv"] != (so{ScopeGlobal, OriginHome, "home-override"}) {
		t.Errorf("cfg-srv = %+v, want global/home with home override", got["cfg-srv"])
	}
	if got["home-srv"] != (so{ScopeLocal, OriginProject, "proj-override"}) {
		t.Errorf("home-srv = %+v, want local/project with project override", got["home-srv"])
	}
	if got["proj-srv"] != (so{ScopeLocal, OriginProject, "proj-mcp"}) {
		t.Errorf("proj-srv = %+v, want local/project", got["proj-srv"])
	}

	// Without any overrides the config.yaml entry stays config-owned/global.
	if _, err := config.DeleteMCPJSONServer(globalPath, "cfg-srv"); err != nil {
		t.Fatal(err)
	}
	servers, _ = ListManagedServers(cfg, cwd)
	for _, s := range servers {
		if s.Config.Name == "cfg-srv" && (s.Scope != ScopeGlobal || s.Origin != OriginConfig) {
			t.Errorf("cfg-srv = %s/%s, want global/config", s.Scope, s.Origin)
		}
	}
}

func TestSetServerDisabledPersistsToOwningFile(t *testing.T) {
	cfg, cfgPath, home := writeTestConfig(t)
	cwd := t.TempDir()
	if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(home), "home-srv", config.MCPJSONServer{Command: "home-mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(cwd), "proj-srv", config.MCPJSONServer{Command: "proj-mcp"}); err != nil {
		t.Fatal(err)
	}

	// Project-owned toggle lands in <cwd>/.coddy/mcp.json.
	if err := SetServerDisabled(cfg, cwd, "proj-srv", true); err != nil {
		t.Fatalf("disable project server: %v", err)
	}
	entries, _ := config.ReadMCPJSONFile(config.MCPJSONPath(cwd))
	if !entries["proj-srv"].Disabled {
		t.Errorf("proj-srv not disabled in project mcp.json: %+v", entries)
	}

	// Home-owned toggle lands in <home>/mcp.json.
	if err := SetServerDisabled(cfg, cwd, "home-srv", true); err != nil {
		t.Fatalf("disable home server: %v", err)
	}
	entries, _ = config.ReadMCPJSONFile(config.GlobalMCPJSONPath(home))
	if !entries["home-srv"].Disabled {
		t.Errorf("home-srv not disabled in global mcp.json: %+v", entries)
	}

	// Config-owned toggle lands in config.yaml.
	if err := SetServerDisabled(cfg, cwd, "cfg-srv", true); err != nil {
		t.Fatalf("disable config server: %v", err)
	}
	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.MCPServers) != 1 || !reloaded.MCPServers[0].Disabled {
		t.Errorf("config.yaml servers = %+v, want cfg-srv disabled", reloaded.MCPServers)
	}

	if err := SetServerDisabled(cfg, cwd, "ghost", true); err == nil {
		t.Error("unknown server must error")
	}
}

func TestSetToolDisabledPersistsToOwningFile(t *testing.T) {
	cfg, cfgPath, home := writeTestConfig(t)
	cwd := t.TempDir()
	if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(home), "home-srv", config.MCPJSONServer{Command: "home-mcp"}); err != nil {
		t.Fatal(err)
	}

	if err := SetToolDisabled(cfg, cwd, "home-srv", "echo", true); err != nil {
		t.Fatalf("disable home tool: %v", err)
	}
	entries, _ := config.ReadMCPJSONFile(config.GlobalMCPJSONPath(home))
	if got := entries["home-srv"].DisabledTools; len(got) != 1 || got[0] != "echo" {
		t.Errorf("home-srv disabledTools = %v, want [echo]", got)
	}

	if err := SetToolDisabled(cfg, cwd, "cfg-srv", "reverse", true); err != nil {
		t.Fatalf("disable config tool: %v", err)
	}
	reloaded, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := reloaded.MCPServers[0].DisabledTools; len(got) != 1 || got[0] != "reverse" {
		t.Errorf("config.yaml disabled_tools = %v, want [reverse]", got)
	}

	// Re-enable removes the entry again.
	if err := SetToolDisabled(cfg, cwd, "cfg-srv", "reverse", false); err != nil {
		t.Fatal(err)
	}
	reloaded, _ = config.Load(cfgPath)
	if got := reloaded.MCPServers[0].DisabledTools; len(got) != 0 {
		t.Errorf("config.yaml disabled_tools = %v, want empty", got)
	}
}

func TestUpsertServerScopes(t *testing.T) {
	cfg, _, home := writeTestConfig(t)
	cwd := t.TempDir()

	if err := UpsertServer(cfg, cwd, "glob", ScopeGlobal, config.MCPJSONServer{Command: "glob-mcp"}); err != nil {
		t.Fatalf("upsert global: %v", err)
	}
	entries, _ := config.ReadMCPJSONFile(config.GlobalMCPJSONPath(home))
	if entries["glob"].Command != "glob-mcp" {
		t.Errorf("global mcp.json = %+v, want glob", entries)
	}

	if err := UpsertServer(cfg, cwd, "loc", ScopeLocal, config.MCPJSONServer{Command: "loc-mcp"}); err != nil {
		t.Fatalf("upsert local: %v", err)
	}
	entries, _ = config.ReadMCPJSONFile(config.MCPJSONPath(cwd))
	if entries["loc"].Command != "loc-mcp" {
		t.Errorf("project mcp.json = %+v, want loc", entries)
	}

	if err := UpsertServer(cfg, cwd, "x", "nope", config.MCPJSONServer{Command: "x"}); err == nil {
		t.Error("unknown scope must error")
	}
}

func TestDeleteServerPerOrigin(t *testing.T) {
	cfg, _, home := writeTestConfig(t)
	cwd := t.TempDir()
	if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(home), "home-srv", config.MCPJSONServer{Command: "home-mcp"}); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(cwd), "proj-srv", config.MCPJSONServer{Command: "proj-mcp"}); err != nil {
		t.Fatal(err)
	}

	if err := DeleteServer(cfg, cwd, "proj-srv"); err != nil {
		t.Fatalf("delete project server: %v", err)
	}
	if err := DeleteServer(cfg, cwd, "home-srv"); err != nil {
		t.Fatalf("delete home server: %v", err)
	}
	entries, _ := config.ReadMCPJSONFile(config.GlobalMCPJSONPath(home))
	if _, ok := entries["home-srv"]; ok {
		t.Errorf("home-srv still present: %+v", entries)
	}

	if err := DeleteServer(cfg, cwd, "cfg-srv"); err == nil {
		t.Error("config-defined server must refuse API deletion")
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
