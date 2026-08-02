package session

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

func TestReplaceGlobalMCPClientsPreservesSessionClients(t *testing.T) {
	globalBefore := &mcp.Client{}
	sessionClient := &mcp.Client{}
	globalAfter := &mcp.Client{}
	st := &State{}
	st.AddMCPClient(globalBefore, true)
	st.AddMCPClient(sessionClient, false)

	old := st.ReplaceGlobalMCPClients([]*mcp.Client{globalAfter})
	if len(old) != 1 || old[0] != globalBefore {
		t.Fatalf("old global clients = %+v", old)
	}
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
	st := &State{ID: "reload-mcp", CWD: dir, Mode: ModeAgent}
	reloadCtx, cancelReload := context.WithCancel(context.Background())
	warnings, err := mgr.ReloadConfigForSession(reloadCtx, st)
	if err != nil {
		t.Fatal(err)
	}
	defer st.CloseAll()
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
	cancelReload()
	for i := 0; i < 20; i++ {
		callCtx, cancelCall := context.WithTimeout(context.Background(), 100*time.Millisecond)
		got, callErr := clients[0].CallTool(callCtx, "ping", `{}`)
		cancelCall()
		if callErr != nil {
			t.Fatalf("MCP should outlive the config_set turn context: %v", callErr)
		}
		if got != "pong" {
			t.Fatalf("ping result = %q", got)
		}
	}
}

func TestConfigReloadMCPHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CONFIG_RELOAD_MCP") != "1" {
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
