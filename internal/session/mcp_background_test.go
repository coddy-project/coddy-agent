package session

// Tests of the background MCP connect (mcp_background.go): session/new
// returning before the servers answer, the control updates the console
// listens for, a turn waiting for the tool list, Stop ending that wait, a
// reload superseding a pending dial, and a teardown releasing it.

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
)

// controlCapture is a sender that also listens for control updates, the way
// the console does.
type controlCapture struct {
	mcpTestSender
	mu      sync.Mutex
	updates []MCPConnectUpdate
}

func (c *controlCapture) SendControlUpdate(_ string, update any) error {
	if u, ok := update.(MCPConnectUpdate); ok {
		c.mu.Lock()
		c.updates = append(c.updates, u)
		c.mu.Unlock()
	}
	return nil
}

func (c *controlCapture) last() (MCPConnectUpdate, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.updates) == 0 {
		return MCPConnectUpdate{}, false
	}
	return c.updates[len(c.updates)-1], true
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// backgroundFixture is a manager that defers connects, with one gated server
// the test releases when it wants the dial to settle.
type backgroundFixture struct {
	mgr     *Manager
	st      *State
	sender  *controlCapture
	release string
	started string
}

// setup, when given, adjusts the manager before the session is created - the
// dial reads the manager's settings from its worker, so a test changes them
// here and never afterwards.
func newBackgroundFixture(t *testing.T, runner AgentRunner, setup func(*Manager)) *backgroundFixture {
	t.Helper()
	dir := t.TempDir()
	f := &backgroundFixture{
		sender:  &controlCapture{},
		release: filepath.Join(dir, "release"),
		started: filepath.Join(dir, "started"),
	}
	f.mgr = NewManager(reloadTestConfig(gatedMCPServer("gated", f.started, f.release)), f.sender, runner, slog.Default(), t.TempDir(), nil)
	f.mgr.SetBackgroundMCPConnect(true)
	if setup != nil {
		setup(f.mgr)
	}
	begin := time.Now()
	res, err := f.mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if took := time.Since(begin); took > 2*time.Second {
		t.Fatalf("session/new took %s while a server was still connecting", took)
	}
	f.st = f.mgr.SessionByID(res.SessionID)
	t.Cleanup(f.st.CloseAll)
	return f
}

func (f *backgroundFixture) releaseServer() {
	_ = os.WriteFile(f.release, []byte("1"), 0o644)
}

// TestBackgroundConnectReturnsBeforeServersAnswer: session/new returns while
// the server is still in its handshake, the snapshot says so, and the client
// is installed once the server answers.
func TestBackgroundConnectReturnsBeforeServersAnswer(t *testing.T) {
	f := newBackgroundFixture(t, nil, nil)
	if got := f.st.GetMCPClients(); len(got) != 0 {
		t.Fatalf("session/new installed %d clients before the server answered", len(got))
	}
	snap, recorded := f.st.MCPConnectSnapshot()
	if !recorded || snap.Done || len(snap.Servers) != 1 || snap.Servers[0].State != MCPConnectStateConnecting {
		t.Fatalf("snapshot = %+v (recorded %v), want one server connecting", snap, recorded)
	}
	if connected, total := snap.Counts(); connected != 0 || total != 1 {
		t.Fatalf("counts = %d/%d, want 0/1", connected, total)
	}
	f.releaseServer()
	if !waitUntil(t, 10*time.Second, func() bool { s, _ := f.st.MCPConnectSnapshot(); return s.Done }) {
		t.Fatal("the dial never settled after the release")
	}
	snap, _ = f.st.MCPConnectSnapshot()
	if snap.Servers[0].State != MCPConnectStateConnected || snap.Servers[0].Tools != 1 {
		t.Fatalf("server after release = %+v, want connected with 1 tool", snap.Servers[0])
	}
	if got := clientNames(f.st); len(got) != 1 || got[0] != "gated" {
		t.Fatalf("clients = %v, want [gated]", got)
	}
	last, ok := f.sender.last()
	if !ok || !last.Done {
		t.Fatalf("last control update = %+v (%v), want done", last, ok)
	}
}

// TestTurnWaitsForBackgroundConnect: a prompt sent while the server connects
// starts only once the dial settled, with the server's tools in place.
func TestTurnWaitsForBackgroundConnect(t *testing.T) {
	entered := make(chan int, 1)
	runner := func(_ context.Context, st *State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		entered <- len(st.GetMCPClients())
		return string(acp.StopReasonEndTurn), nil
	}
	f := newBackgroundFixture(t, runner, nil)
	turn := make(chan error, 1)
	go func() {
		_, err := f.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: f.st.GetID(), Prompt: []acp.ContentBlock{{Type: "text", Text: "use the tool"}},
		})
		turn <- err
	}()
	select {
	case n := <-entered:
		t.Fatalf("the turn started with %d clients before the dial settled", n)
	case <-time.After(300 * time.Millisecond):
	}
	f.releaseServer()
	select {
	case n := <-entered:
		if n != 1 {
			t.Fatalf("the turn saw %d clients, want 1", n)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the turn never started after the release")
	}
	if err := <-turn; err != nil {
		t.Fatal(err)
	}
}

// TestCancelDuringWaitEndsTurn: Stop while the turn waits for the dial ends
// the turn without running it.
func TestCancelDuringWaitEndsTurn(t *testing.T) {
	entered := make(chan struct{}, 1)
	runner := func(context.Context, *State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		entered <- struct{}{}
		return string(acp.StopReasonEndTurn), nil
	}
	f := newBackgroundFixture(t, runner, nil)
	turn := make(chan error, 1)
	go func() {
		_, err := f.mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: f.st.GetID(), Prompt: []acp.ContentBlock{{Type: "text", Text: "hi"}},
		})
		turn <- err
	}()
	if !waitUntil(t, 5*time.Second, func() bool { return f.mgr.SessionTurnActiveInProcess(f.st.GetID()) }) {
		t.Fatal("the turn never became active")
	}
	f.mgr.HandleSessionCancel(acp.SessionCancelParams{SessionID: f.st.GetID()})
	select {
	case <-turn:
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled turn did not end")
	}
	select {
	case <-entered:
		t.Fatal("the runner ran although the turn was cancelled while waiting")
	default:
	}
	f.releaseServer()
}

// TestReloadDuringPendingConnectLeavesNoDuplicates: a settings change while
// the dial is pending supersedes it; the late result is closed, not installed.
func TestReloadDuringPendingConnectLeavesNoDuplicates(t *testing.T) {
	f := newBackgroundFixture(t, nil, func(m *Manager) { m.SetMCPConnectTimeoutForTest(300 * time.Millisecond) })
	next := reloadTestConfig(gatedMCPServer("gated", f.started, f.release), reloadTestMCPServer("good"))
	f.mgr.ReplaceConfig(next)
	f.releaseServer()
	if !waitUntil(t, 10*time.Second, func() bool { s, _ := f.st.MCPConnectSnapshot(); return s.Done }) {
		t.Fatal("the superseded dial never settled")
	}
	time.Sleep(200 * time.Millisecond) // a late install would land here
	names := clientNames(f.st)
	seen := map[string]int{}
	for _, n := range names {
		seen[n]++
	}
	for name, n := range seen {
		if n > 1 {
			t.Fatalf("server %q installed %d times: %v", name, n, names)
		}
	}
	if seen["good"] != 1 {
		t.Fatalf("clients = %v, want the reloaded server good", names)
	}
}

// TestCloseAllUnblocksWait: tearing the session down releases a turn waiting
// for its dial.
func TestCloseAllUnblocksWait(t *testing.T) {
	f := newBackgroundFixture(t, nil, nil)
	waited := make(chan error, 1)
	go func() { waited <- f.st.WaitMCPConnect(context.Background()) }()
	select {
	case <-waited:
		t.Fatal("the wait ended before the dial settled")
	case <-time.After(200 * time.Millisecond):
	}
	f.st.CloseAll()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CloseAll did not release the wait")
	}
	f.releaseServer()
}

// TestForegroundConnectRecordsNothing: without the flag the session connects
// in session/new and has no background record.
func TestForegroundConnectRecordsNothing(t *testing.T) {
	mgr := NewManager(reloadTestConfig(reloadTestMCPServer("good")), mcpTestSender{}, nil, slog.Default(), t.TempDir(), nil)
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(res.SessionID)
	t.Cleanup(st.CloseAll)
	if _, recorded := st.MCPConnectSnapshot(); recorded {
		t.Fatal("a foreground connect recorded a background snapshot")
	}
	if err := st.WaitMCPConnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := clientNames(st); len(got) != 1 {
		t.Fatalf("clients = %v, want [good]", got)
	}
}

// TestHeldServerIsReportedNotDialed: a project declaration the gate holds
// shows as held in the snapshot and is not counted as connectable.
func TestHeldServerIsReportedNotDialed(t *testing.T) {
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".coddy"), 0o755); err != nil {
		t.Fatal(err)
	}
	project := `{"mcpServers":{"project-tool":{"command":"` + os.Args[0] + `","args":["-test.run=^TestGatedMCPHelperProcess$"]}}}`
	if err := os.WriteFile(filepath.Join(cwd, ".coddy", "mcp.json"), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := reloadTestConfig(reloadTestMCPServer("good"))
	cfg.MCP.ProjectTrust = config.ProjectTrustAsk
	cfg.Paths.Home = t.TempDir()
	mgr := NewManager(cfg, mcpTestSender{}, nil, slog.Default(), cwd, nil)
	mgr.SetBackgroundMCPConnect(true)
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(res.SessionID)
	t.Cleanup(st.CloseAll)
	if !waitUntil(t, 10*time.Second, func() bool { s, _ := st.MCPConnectSnapshot(); return s.Done }) {
		t.Fatal("the dial never settled")
	}
	snap, _ := st.MCPConnectSnapshot()
	var held *MCPServerConnect
	for i := range snap.Servers {
		if snap.Servers[i].Name == "project-tool" {
			held = &snap.Servers[i]
		}
	}
	if held == nil || held.State != MCPConnectStateHeld || held.Hint == "" {
		t.Fatalf("project server = %+v, want held with a hint", held)
	}
	if connected, total := snap.Counts(); connected != 1 || total != 1 {
		t.Fatalf("counts = %d/%d, want 1/1 (the held server is not dialed)", connected, total)
	}
}

// TestSnapshotGenerationMovesWithTheDial: a superseded dial's snapshot is
// older than the replacement's, which is what lets a surface drop it.
func TestSnapshotGenerationMovesWithTheDial(t *testing.T) {
	f := newBackgroundFixture(t, nil, func(m *Manager) { m.SetMCPConnectTimeoutForTest(300 * time.Millisecond) })
	first, _ := f.st.MCPConnectSnapshot()
	if first.Generation == 0 {
		t.Fatal("the first snapshot carries no generation")
	}
	f.mgr.ReplaceConfig(reloadTestConfig(reloadTestMCPServer("good")))
	after, _ := f.st.MCPConnectSnapshot()
	if after.Generation <= first.Generation {
		t.Fatalf("generation after the reload = %d, want above %d", after.Generation, first.Generation)
	}
	f.releaseServer()
}

// TestApprovedProjectServerConnectsInTheBackground: a project declaration the
// operator approved is dialed like a configured one, through the gate, and
// installed when it answers.
func TestApprovedProjectServerConnectsInTheBackground(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".coddy"), 0o755); err != nil {
		t.Fatal(err)
	}
	started := filepath.Join(t.TempDir(), "started")
	entry := config.MCPJSONServer{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestGatedMCPHelperProcess$"},
		Env:     map[string]string{reloadTestMCPHelperEnv: "1", gatedMCPStartedEnv: started},
	}
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(cwd), "project-tool", entry); err != nil {
		t.Fatal(err)
	}
	cfg := reloadTestConfig()
	cfg.MCP.ProjectTrust = config.ProjectTrustAsk
	cfg.Paths.Home = home
	servers, err := mcp.ListManagedServers(cfg, cwd)
	if err != nil {
		t.Fatal(err)
	}
	for _, srv := range servers {
		if srv.Config.Name == "project-tool" {
			if err := mcp.NewTrustStore(home).Approve(cwd, config.MCPJSONPath(cwd), srv.Config); err != nil {
				t.Fatal(err)
			}
		}
	}
	mgr := NewManager(cfg, mcpTestSender{}, nil, slog.Default(), cwd, nil)
	mgr.SetBackgroundMCPConnect(true)
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatal(err)
	}
	st := mgr.SessionByID(res.SessionID)
	t.Cleanup(st.CloseAll)
	if !waitUntil(t, 10*time.Second, func() bool { s, _ := st.MCPConnectSnapshot(); return s.Done }) {
		t.Fatal("the dial never settled")
	}
	snap, _ := st.MCPConnectSnapshot()
	if len(snap.Servers) != 1 || snap.Servers[0].Name != "project-tool" || snap.Servers[0].State != MCPConnectStateConnected {
		t.Fatalf("snapshot = %+v, want project-tool connected", snap.Servers)
	}
	if got := clientNames(st); len(got) != 1 || got[0] != "project-tool" {
		t.Fatalf("clients = %v, want [project-tool]", got)
	}
	if _, err := os.Stat(started); err != nil {
		t.Fatal("the approved server was never spawned")
	}
}
