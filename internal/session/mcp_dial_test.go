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
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
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
	// gatedMCPPIDEnv names a file the stub writes its pid to, so a test can
	// end the process the way a crash would.
	gatedMCPPIDEnv = "CODDY_TEST_GATED_MCP_PID"
	// gatedMCPExitsEnv asks the stub to record its exit as well. Only a test
	// that waits for the exits it causes sets it: a record written after the
	// test ended would land in a temporary folder that is being removed.
	gatedMCPExitsEnv = "CODDY_TEST_GATED_MCP_EXITS"
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
	// One line per spawn, so a test can count how often the server was
	// started.
	if f, err := os.OpenFile(started, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		_, _ = f.WriteString("started\n")
		_ = f.Close()
	}
	if pidFile := os.Getenv(gatedMCPPIDEnv); pidFile != "" {
		_ = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644)
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
	// Stdin closed: the client let the server go. One line per exit, so a
	// test can tell a server that was stopped from one still running.
	if os.Getenv(gatedMCPExitsEnv) == "1" {
		if f, err := os.OpenFile(started, os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.WriteString("exited\n")
			_ = f.Close()
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

// spawns counts how often the gated server behind started was started.
func spawns(started string) int {
	data, err := os.ReadFile(started)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "started\n")
}

// exits counts how often the gated server behind started saw its stdin close
// and exited.
func exits(started string) int {
	data, err := os.ReadFile(started)
	if err != nil {
		return 0
	}
	return strings.Count(string(data), "exited\n")
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
	mgr := NewManager(reloadTestConfig(t, a, b), mcpTestSender{}, nil, slog.Default(), t.TempDir(), nil)

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
	mgr := NewManager(reloadTestConfig(t, hung, good), mcpTestSender{}, nil, slog.Default(), t.TempDir(), nil)
	mgr.SetMCPConnectTimeoutForTest(2 * time.Second)

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
	mgr := NewManager(reloadTestConfig(t, srv), mcpTestSender{}, nil, slog.Default(), t.TempDir(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, _ := mgr.dialConfigured(ctx, mgr.activeCfg(), t.TempDir())
	if got := connectedClients(results); len(got) != 0 {
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
	mgr := NewManager(reloadTestConfig(t), mcpTestSender{}, nil, slog.Default(), t.TempDir(), nil)
	mgr.SetMCPConnectTimeoutForTest(2 * time.Second)
	results, held := mgr.dialConfigured(context.Background(), reloadTestConfig(t, hung, broken, reloadTestMCPServer("good")), t.TempDir())
	clients, warnings := connectedClients(results), dialWarnings(results, held)
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
	// The servers of an mcp.json are dialed in name order.
	for i, prefix := range []string{"connect MCP broken: ", "connect MCP hung: "} {
		if len(warnings[i]) < len(prefix) || warnings[i][:len(prefix)] != prefix {
			t.Fatalf("warning %d = %q, want prefix %q", i, warnings[i], prefix)
		}
	}
}

// promptNames runs one turn on the session and returns the MCP clients the
// turn found when it started.
func promptNames(t *testing.T, mgr *Manager, sessionID string, entered <-chan []string) []string {
	t.Helper()
	if _, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: sessionID, Prompt: []acp.ContentBlock{{Type: "text", Text: "go"}},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case names := <-entered:
		return names
	case <-time.After(10 * time.Second):
		t.Fatal("the turn never reached its runner")
		return nil
	}
}

// namesRunner is a turn that reports the MCP clients it found.
func namesRunner(entered chan<- []string) AgentRunner {
	return func(_ context.Context, st *State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		entered <- clientNames(st)
		return string(acp.StopReasonEndTurn), nil
	}
}

// TestSlowFirstStartIsTriedOnceMoreAtTheNextTurn: a server that outlasts the
// per-server bound on its first start - an npx package still installing -
// is dialed once more when the session's next turn starts, and connects.
func TestSlowFirstStartIsTriedOnceMoreAtTheNextTurn(t *testing.T) {
	dir := t.TempDir()
	started, release := filepath.Join(dir, "started"), filepath.Join(dir, "release")
	entered := make(chan []string, 4)
	mgr := NewManager(reloadTestConfig(t, gatedMCPServer("slow", started, release)), mcpTestSender{}, namesRunner(entered), slog.Default(), t.TempDir(), nil)
	mgr.SetMCPConnectTimeoutForTest(2 * time.Second)
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(res.SessionID)
	t.Cleanup(st.CloseAll)
	if names := clientNames(st); len(names) != 0 {
		t.Fatalf("clients after the first start = %v, want none", names)
	}
	// The install finished meanwhile: the server answers from now on.
	if err := os.WriteFile(release, []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if names := promptNames(t, mgr, res.SessionID, entered); len(names) != 1 || names[0] != "slow" {
		t.Fatalf("the first turn found %v, want [slow]", names)
	}
	if n := spawns(started); n != 2 {
		t.Fatalf("the server was started %d times, want 2", n)
	}
}

// TestServerThatNeverAnswersIsTriedTwiceAtMost: a server that never answers
// costs the session start and the next turn their bound, and no turn after
// that; its switch gives it a fresh dial.
func TestServerThatNeverAnswersIsTriedTwiceAtMost(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	entered := make(chan []string, 4)
	mgr := NewManager(reloadTestConfig(t, gatedMCPServer("hung", started, filepath.Join(dir, "never")), reloadTestMCPServer("good")),
		mcpTestSender{}, namesRunner(entered), slog.Default(), t.TempDir(), nil)
	mgr.SetMCPConnectTimeoutForTest(2 * time.Second)
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(res.SessionID)
	t.Cleanup(st.CloseAll)
	for turn := 1; turn <= 3; turn++ {
		if names := promptNames(t, mgr, res.SessionID, entered); len(names) != 1 || names[0] != "good" {
			t.Fatalf("turn %d found %v, want [good]", turn, names)
		}
	}
	if n := spawns(started); n != 2 {
		t.Fatalf("the server that never answers was started %d times over the start and three turns, want 2", n)
	}
	mgr.RefreshMCPServer(context.Background(), "hung")
	if n := spawns(started); n != 3 {
		t.Fatalf("after its switch the server was started %d times in all, want 3", n)
	}
}

// TestBackgroundNoAnswerIsTriedOnceMoreAtTheFirstPrompt: the console's
// background connect says a server that did not answer in time is tried
// once more, and the first prompt does try it.
func TestBackgroundNoAnswerIsTriedOnceMoreAtTheFirstPrompt(t *testing.T) {
	entered := make(chan []string, 4)
	f := newBackgroundFixture(t, namesRunner(entered), func(m *Manager) { m.SetMCPConnectTimeoutForTest(2 * time.Second) })
	if !waitUntil(t, 10*time.Second, func() bool { s, _ := f.st.MCPConnectSnapshot(); return s.Done }) {
		t.Fatal("the dial never settled")
	}
	snap, _ := f.st.MCPConnectSnapshot()
	if len(snap.Servers) != 1 || snap.Servers[0].State != MCPConnectStateFailed || !strings.Contains(snap.Servers[0].Hint, "once more") {
		t.Fatalf("snapshot = %+v, want the server failed with the retry hint", snap.Servers)
	}
	f.releaseServer()
	if names := promptNames(t, f.mgr, f.st.GetID(), entered); len(names) != 1 || names[0] != "gated" {
		t.Fatalf("the first prompt found %v, want [gated]", names)
	}
}

// TestStopDuringTheOneMoreTryEndsTheTurnAtOnce: the dial a turn's start makes
// for a server's one more try runs on the turn's context, so Stop ends it at
// once: the runner is entered with the cancelled context, and the server,
// cut short rather than failed, is parked for the next turn.
func TestStopDuringTheOneMoreTryEndsTheTurnAtOnce(t *testing.T) {
	dir := t.TempDir()
	started := filepath.Join(dir, "started")
	entered := make(chan error, 2)
	runner := func(ctx context.Context, _ *State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		entered <- ctx.Err()
		return string(acp.StopReasonCancelled), nil
	}
	mgr := NewManager(reloadTestConfig(t, gatedMCPServer("hung", started, filepath.Join(dir, "never"))), mcpTestSender{}, runner, slog.Default(), t.TempDir(), nil)
	mgr.SetMCPConnectTimeoutForTest(2 * time.Second)
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(res.SessionID)
	t.Cleanup(st.CloseAll)
	go func() {
		_, _ = mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: res.SessionID, Prompt: []acp.ContentBlock{{Type: "text", Text: "go"}},
		})
	}()
	if !waitUntil(t, 5*time.Second, func() bool { return spawns(started) == 2 }) {
		t.Fatalf("the one more try never started (spawns %d)", spawns(started))
	}
	stopped := time.Now()
	mgr.HandleSessionCancel(acp.SessionCancelParams{SessionID: res.SessionID})
	select {
	case err := <-entered:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("the runner was entered with %v, want context.Canceled", err)
		}
		if took := time.Since(stopped); took > time.Second {
			t.Fatalf("the stopped turn reached its runner %s after Stop, want at once", took)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stopped turn never reached its runner")
	}
	if !waitUntil(t, 2*time.Second, st.hasPendingMCPServers) {
		t.Fatal("the server the Stop cut short was not parked for the next turn")
	}
}

// TestAFailureIsNotCutShortByALaterDeadline: in one start, a server that
// fails at once is not parked for the next turn because another one then ran
// the shared deadline out; only the one cut short is.
func TestAFailureIsNotCutShortByALaterDeadline(t *testing.T) {
	prev := mcpStartTimeout
	mcpStartTimeout = 400 * time.Millisecond
	t.Cleanup(func() { mcpStartTimeout = prev })
	dir := t.TempDir()
	hung := gatedMCPServer("hung", filepath.Join(dir, "started"), filepath.Join(dir, "never"))
	broken := config.MCPServerConfig{Type: "stdio", Name: "broken", Command: filepath.Join(dir, "missing-binary")}
	mgr := NewManager(reloadTestConfig(t, broken, hung), mcpTestSender{}, nil, slog.Default(), t.TempDir(), nil)
	mgr.SetMCPConnectTimeoutForTest(10 * time.Second)
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(res.SessionID)
	t.Cleanup(st.CloseAll)
	if got := st.takeMCPServersPending(); len(got) != 1 || got[0] != "hung" {
		t.Fatalf("parked = %v, want only the server the deadline cut short", got)
	}
}

// TestExpiredBudgetDialsNothing: a single-server dial under a context that
// has already ended does not call the server at all.
func TestExpiredBudgetDialsNothing(t *testing.T) {
	mgr := NewManager(reloadTestConfig(t), mcpTestSender{}, nil, slog.Default(), t.TempDir(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	_, err := mgr.dialOne(ctx, mcpDialTarget{Connect: func(context.Context) (*mcp.Client, error) {
		called = true
		return nil, nil
	}})
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("dialOne under an ended context = %v, called %v; want context.Canceled and no call", err, called)
	}
}
