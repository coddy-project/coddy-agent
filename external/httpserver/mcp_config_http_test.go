//go:build http

package httpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const httpSettingsMCPHelperEnv = "CODDY_TEST_HTTP_SETTINGS_MCP_HELPER"

func TestCoddyConfigPutConnectsMCPToActiveSession(t *testing.T) {
	home := t.TempDir()
	// The helper MCP server is this test binary, named by an absolute path so
	// the server process finds it from any working directory.
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The home of whoever runs the tests, with a global mcp.json of its own:
	// a server declared there is not one this test configured.
	operatorHome := t.TempDir()
	operatorMCP, err := json.Marshal(map[string]interface{}{"mcpServers": map[string]interface{}{
		"operator-server": map[string]interface{}{
			"command": helper,
			"args":    []string{"-test.run=^TestHTTPSettingsMCPHelperProcess$"},
			"env":     map[string]string{httpSettingsMCPHelperEnv: "1"},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.GlobalMCPJSONPath(operatorHome), operatorMCP, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODDY_HOME", operatorHome)
	configPath := filepath.Join(home, "config.yaml")
	initial := `
providers:
  - name: openai
    type: openai
    api_key: k
models:
  - model: openai/gpt-4o
    max_tokens: 4096
agent:
  model: openai/gpt-4o
`
	if err := os.WriteFile(configPath, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	// The test's own home, not the one CODDY_HOME names: the global mcp.json
	// and .env are read from it.
	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: home, Config: configPath})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Paths.Home != home {
		t.Fatalf("config home = %q, want the test's %q", cfg.Paths.Home, home)
	}
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), home, nil)
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: home})
	if err != nil {
		t.Fatal(err)
	}
	state := mgr.SessionByID(created.SessionID)
	t.Cleanup(state.CloseAll)

	srv := New(cfg, mgr, slog.Default(), home)
	httpServer := httptest.NewServer(srv.Handler())
	defer httpServer.Close()

	dto := config.ConfigToJSONDTO(cfg)
	dto.MCPServers = []config.MCPServerJSON{{
		Type:    "stdio",
		Name:    "settings-probe",
		Command: helper,
		Args:    []string{"-test.run=^TestHTTPSettingsMCPHelperProcess$"},
		Env:     []config.EnvVarJSON{{Name: httpSettingsMCPHelperEnv, Value: "1"}},
	}}
	body, err := json.Marshal(dto)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPut, httpServer.URL+"/coddy/config", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("PUT /coddy/config status = %d", response.StatusCode)
	}

	// The save went to the test's own home: the operator's file is untouched.
	if got, err := os.ReadFile(config.GlobalMCPJSONPath(operatorHome)); err != nil || string(got) != string(operatorMCP) {
		t.Fatalf("operator mcp.json after the save = %q, %v; want it unchanged", got, err)
	}

	clients := state.GetMCPClients()
	if len(clients) != 1 || clients[0].Name() != "settings-probe" {
		t.Fatalf("active session MCP clients = %#v, want settings-probe", clients)
	}
	tools := clients[0].Tools()
	if len(tools) != 1 || tools[0].Name != "probe" {
		t.Fatalf("active session MCP tools = %#v, want probe", tools)
	}
}

func TestHTTPSettingsMCPHelperProcess(t *testing.T) {
	if os.Getenv(httpSettingsMCPHelperEnv) != "1" {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request map[string]interface{}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			continue
		}
		id, hasID := request["id"]
		if !hasID {
			continue
		}
		result := map[string]interface{}{}
		switch request["method"] {
		case "tools/list":
			result["tools"] = []interface{}{map[string]interface{}{
				"name":        "probe",
				"description": "Reports that the settings MCP is connected.",
				"inputSchema": map[string]interface{}{"type": "object"},
			}}
		case "initialize":
			result = map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"serverInfo":      map[string]interface{}{"name": "settings-probe", "version": "1"},
			}
		}
		if err := encoder.Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"result":  result,
		}); err != nil {
			return
		}
	}
	os.Exit(0)
}
