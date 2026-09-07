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
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
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
		Command: os.Args[0],
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
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("PUT /coddy/config status = %d", response.StatusCode)
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
		if request["method"] == "tools/list" {
			result["tools"] = []interface{}{map[string]interface{}{
				"name":        "probe",
				"description": "Reports that the settings MCP is connected.",
				"inputSchema": map[string]interface{}{"type": "object"},
			}}
		} else if request["method"] == "initialize" {
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
