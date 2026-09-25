package session

// Tests of the concurrent, bounded MCP dial (mcp_dial.go): a gated stdio
// stub, TestGatedMCPHelperProcess, that announces itself and answers
// initialize only once a release file exists, so a test can hold two
// servers in their handshake at once, let one hang, or spawn nothing under
// an expired context.

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

// mcpTestSender is the update sender of the MCP tests: it drops updates and
// answers no prompt.
type mcpTestSender struct{}

func (mcpTestSender) SendSessionUpdate(string, interface{}) error { return nil }
func (mcpTestSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "cancelled"}, nil
}
func (mcpTestSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

const (
	gatedMCPStartedEnv = "CODDY_TEST_GATED_MCP_STARTED"
	gatedMCPReleaseEnv = "CODDY_TEST_GATED_MCP_RELEASE"
)

// gatedMCPServer declares a stdio server run by TestGatedMCPHelperProcess:
// it writes started as soon as it runs and answers initialize only once
// release exists. A release that never appears is a server that hangs in its
// handshake.
func gatedMCPServer(name, started, release string) config.MCPServerConfig {
	return config.MCPServerConfig{
		Type:    "stdio",
		Name:    name,
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestGatedMCPHelperProcess$"},
		Env: []config.EnvVarConfig{
			{Name: reloadTestMCPHelperEnv, Value: "1"},
			{Name: gatedMCPStartedEnv, Value: started},
			{Name: gatedMCPReleaseEnv, Value: release},
		},
	}
}

// TestGatedMCPHelperProcess is the stdio MCP stub behind gatedMCPServer.
func TestGatedMCPHelperProcess(t *testing.T) {
	started, release := os.Getenv(gatedMCPStartedEnv), os.Getenv(gatedMCPReleaseEnv)
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
			for release != "" {
				if _, err := os.Stat(release); err == nil {
					break
				}
				time.Sleep(20 * time.Millisecond)
			}
			result = map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"serverInfo":      map[string]interface{}{"name": "gated-probe", "version": "1"},
			}
		case "tools/list":
			result["tools"] = []interface{}{
				map[string]interface{}{"name": "probe", "description": "probe", "inputSchema": map[string]interface{}{"type": "object"}},
			}
		}
		if err := encoder.Encode(map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": result}); err != nil {
			return
		}
	}
	os.Exit(0)
}

func waitForFile(t *testing.T, path string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func clientNames(st *State) []string {
	var names []string
	for _, c := range st.GetMCPClients() {
		names = append(names, c.Name())
	}
	return names
}

// TestConfiguredServersDialConcurrently: both servers are running before
// either has answered. A dial that took them one at a time would not start
// the second before the first was released.
func TestConfiguredServersDialConcurrently(t *testing.T) {
	dir := t.TempDir()
	a := gatedMCPServer("a", filepath.Join(dir, "a-started"), filepath.Join(dir, "a-release"))
	b := gatedMCPServer("b", filepath.Join(dir, "b-started"), filepath.Join(dir, "b-release"))
	mgr := NewManager(reloadTestConfig(a, b), mcpTestSender{}, nil, slog.Default(), t.TempDir(), nil)

	type created struct {
		res *acp.SessionNewResult
		err error
	}
	done := make(chan created, 1)
	go func() {
		res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
		done <- created{res, err}
	}()
	if !waitForFile(t, filepath.Join(dir, "a-started"), 5*time.Second) || !waitForFile(t, filepath.Join(dir, "b-started"), 5*time.Second) {
		t.Fatal("both servers should be running before either is released")
	}
	select {
	case <-done:
		t.Fatal("session/new returned before the servers were released")
	case <-time.After(100 * time.Millisecond):
	}
	for _, name := range []string{"a-release", "b-release"} {
		_ = os.WriteFile(filepath.Join(dir, name), []byte("1"), 0o644)
	}
	var c created
	select {
	case c = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("session/new did not return after the release")
	}
	if c.err != nil {
		t.Fatal(c.err)
	}
	st := mgr.SessionByID(c.res.SessionID)
	t.Cleanup(st.CloseAll)
	if got := clientNames(st); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("clients = %v, want [a b] in config order", got)
	}
}

// TestHungServerIsBoundedPerServer: a server that never answers initialize
// costs its own timeout and nothing else; the healthy one is connected.
func TestHungServerIsBoundedPerServer(t *testing.T) {
	dir := t.TempDir()
	hung := gatedMCPServer("hung", filepath.Join(dir, "hung-started"), filepath.Join(dir, "never"))
	good := reloadTestMCPServer("good")
	mgr := NewManager(reloadTestConfig(hung, good), mcpTestSender{}, nil, slog.Default(), t.TempDir(), nil)
	mgr.SetMCPConnectTimeoutForTest(300 * time.Millisecond)

	begin := time.Now()
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(begin); took > 5*time.Second {
		t.Fatalf("session/new took %s with one hung server", took)
	}
	st := mgr.SessionByID(res.SessionID)
	t.Cleanup(st.CloseAll)
	if got := clientNames(st); len(got) != 1 || got[0] != "good" {
		t.Fatalf("clients = %v, want [good]", got)
	}
}

// TestExpiredContextSpawnsNothing: a dial whose budget is already gone starts
// no process.
func TestExpiredContextSpawnsNothing(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	srv := gatedMCPServer("gated", started, "")
	mgr := NewManager(reloadTestConfig(srv), mcpTestSender{}, nil, slog.Default(), t.TempDir(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := mgr.dialConfiguredMCPServers(ctx, t.TempDir()); len(got) != 0 {
		t.Fatalf("expired dial returned %d clients", len(got))
	}
	if waitForFile(t, started, 300*time.Millisecond) {
		t.Fatal("the server was spawned under an expired context")
	}
}

// TestReloadWarningsNameEveryServerThatDidNotStart: the reload keeps one
// warning per server, in the wording the settings save reports.
func TestReloadWarningsNameEveryServerThatDidNotStart(t *testing.T) {
	dir := t.TempDir()
	hung := gatedMCPServer("hung", filepath.Join(dir, "hung-started"), filepath.Join(dir, "never"))
	broken := config.MCPServerConfig{Type: "stdio", Name: "broken", Command: filepath.Join(dir, "missing-binary")}
	mgr := NewManager(reloadTestConfig(), mcpTestSender{}, nil, slog.Default(), t.TempDir(), nil)
	mgr.SetMCPConnectTimeoutForTest(200 * time.Millisecond)
	clients, warnings := mgr.dialConfiguredFor(context.Background(), reloadTestConfig(hung, broken, reloadTestMCPServer("good")), t.TempDir())
	t.Cleanup(func() {
		for _, c := range clients {
			_ = c.Close()
		}
	})
	if len(clients) != 1 || clients[0].Name() != "good" {
		t.Fatalf("clients = %v, want [good]", clients)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want one for hung and one for broken", warnings)
	}
	for i, prefix := range []string{"connect MCP hung: ", "connect MCP broken: "} {
		if len(warnings[i]) < len(prefix) || warnings[i][:len(prefix)] != prefix {
			t.Fatalf("warning %d = %q, want prefix %q", i, warnings[i], prefix)
		}
	}
}
