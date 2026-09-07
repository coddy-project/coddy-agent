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

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// reloadTestMCPHelperEnv reuses the stdio MCP stub defined by the external test
// package; both compile into the same test binary.
const reloadTestMCPHelperEnv = "CODDY_TEST_MCP_HELPER"

func reloadTestConfig(servers ...config.MCPServerConfig) *config.Config {
	return &config.Config{
		Providers:  []config.ProviderConfig{{Name: "p1", Type: "openai", APIKey: "k"}},
		Models:     []config.ModelEntry{{Model: "p1/gpt-4o"}},
		Agent:      config.Agent{Model: "p1/gpt-4o"},
		MCPServers: servers,
	}
}

func reloadTestMCPServer(name string) config.MCPServerConfig {
	return config.MCPServerConfig{
		Type:    "stdio",
		Name:    name,
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestMCPSettingsHelperProcess$"},
		Env:     []config.EnvVarConfig{{Name: reloadTestMCPHelperEnv, Value: "1"}},
	}
}

func newReloadTestManager(t *testing.T, cfg *config.Config) (*Manager, *State) {
	t.Helper()
	mgr := NewManager(cfg, nil, nil, slog.Default(), t.TempDir(), nil)
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	state := mgr.SessionByID(created.SessionID)
	if state == nil {
		t.Fatalf("session %q was not registered", created.SessionID)
	}
	t.Cleanup(state.CloseAll)
	return mgr, state
}

// TestReloadKeepsClientsWhenContextExpired covers the fan-out shape of a
// settings save: every active session dials under one shared deadline, so a
// slow server in the session dialed first can leave nothing for the rest. A
// dial that only failed because the budget was gone must not be installed --
// wiping a healthy session's servers would silently strip its MCP tools.
func TestReloadKeepsClientsWhenContextExpired(t *testing.T) {
	mgr, state := newReloadTestManager(t, reloadTestConfig(reloadTestMCPServer("settings-probe")))
	if got := len(state.GetMCPClients()); got != 1 {
		t.Fatalf("session started with %d MCP clients, want 1", got)
	}

	expired, cancel := context.WithCancel(context.Background())
	cancel()
	mgr.reloadConfiguredMCPServers(expired)

	clients := state.GetMCPClients()
	if len(clients) != 1 || clients[0].Name() != "settings-probe" {
		t.Fatalf("MCP clients after an aborted reload = %#v, want the previous settings-probe kept", clients)
	}
	if !state.hasPendingMCPReload() {
		t.Fatal("an aborted reload must stay parked so a later turn retries it")
	}
}

// TestReloadStillAppliesAnEmptyServerList guards the fix above from over-reach:
// removing every configured server is a legitimate reload whose result is an
// empty client list, and it must still be applied.
func TestReloadStillAppliesAnEmptyServerList(t *testing.T) {
	mgr, state := newReloadTestManager(t, reloadTestConfig(reloadTestMCPServer("settings-probe")))
	if got := len(state.GetMCPClients()); got != 1 {
		t.Fatalf("session started with %d MCP clients, want 1", got)
	}

	mgr.ReplaceConfig(reloadTestConfig())

	if got := len(state.GetMCPClients()); got != 0 {
		t.Fatalf("MCP clients after removing every configured server = %d, want 0", got)
	}
	if state.hasPendingMCPReload() {
		t.Fatal("an applied reload must not stay parked")
	}
}

// TestReloadDrainsAReloadParkedByAnEarlierReload reproduces two overlapping
// settings saves. The first save holds the turn lock while it dials, so the
// second parks its reload and returns without applying it. Releasing the first
// save's lock has to drain that parked reload, otherwise an idle session keeps
// serving the superseded configuration until its next turn.
func TestReloadDrainsAReloadParkedByAnEarlierReload(t *testing.T) {
	startedPath := filepath.Join(t.TempDir(), "slow-mcp-started")
	slow := reloadTestMCPServer("slow-probe")
	slow.Args = []string{"-test.run=^TestSlowMCPHelperProcess$"}
	slow.Env = append(slow.Env, config.EnvVarConfig{Name: slowMCPStartedFileEnv, Value: startedPath})

	mgr, state := newReloadTestManager(t, reloadTestConfig())
	if got := len(state.GetMCPClients()); got != 0 {
		t.Fatalf("session started with %d MCP clients, want 0", got)
	}

	saveA := make(chan struct{})
	go func() {
		defer close(saveA)
		mgr.ReplaceConfig(reloadTestConfig(slow))
	}()

	// Wait until save A is inside the handshake, so it provably holds the lock.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(startedPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slow MCP helper never started")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// Save B lands mid-dial: it stores its configuration, parks its reload, and
	// returns because save A holds the lock.
	mgr.ReplaceConfig(reloadTestConfig(reloadTestMCPServer("settings-probe")))
	<-saveA

	clients := state.GetMCPClients()
	if len(clients) != 1 || clients[0].Name() != "settings-probe" {
		t.Fatalf("MCP clients = %#v, want the newest save applied to the idle session", clients)
	}
	if state.hasPendingMCPReload() {
		t.Fatal("the parked reload must be cleared once applied")
	}
}

// slowMCPStartedFileEnv names the file the slow stub touches once it is running,
// so a test can tell that the dial is genuinely in flight.
const slowMCPStartedFileEnv = "CODDY_TEST_SLOW_MCP_STARTED"

// TestSlowMCPHelperProcess is a stdio MCP stub that announces itself and then
// stalls, keeping a settings reload inside its handshake long enough for a
// second save to race it.
func TestSlowMCPHelperProcess(t *testing.T) {
	started := os.Getenv(slowMCPStartedFileEnv)
	if os.Getenv(reloadTestMCPHelperEnv) != "1" || started == "" {
		return
	}
	_ = os.WriteFile(started, []byte("1"), 0o644)
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
		case "initialize":
			time.Sleep(300 * time.Millisecond)
			result = map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"serverInfo":      map[string]interface{}{"name": "slow-probe", "version": "1"},
			}
		case "tools/list":
			result["tools"] = []interface{}{}
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
