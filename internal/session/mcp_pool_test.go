package session

// Tests of the shared MCP servers (the manager's pool): a global server runs
// once for every session of the process, a project server once per workspace,
// and a project server stops with the last session of its workspace. The
// gated stub of mcp_dial_test.go counts its starts, and its exits when asked
// to, in the file it is handed, so a test sees how many processes a scenario
// started and which of them stopped.

import (
	"context"
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

// newPoolTestSession opens a session of mgr in cwd and returns its state.
func newPoolTestSession(t *testing.T, mgr *Manager, cwd string) *State {
	t.Helper()
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	st := mgr.SessionByID(created.SessionID)
	if st == nil {
		t.Fatalf("session %q was not registered", created.SessionID)
	}
	t.Cleanup(st.CloseAll)
	return st
}

// countedMCPServer is the gated stub, answering at once and recording its
// exits as well as its starts.
func countedMCPServer(name, started string) config.MCPServerConfig {
	srv := gatedMCPServer(name, started, "")
	srv.Env = append(srv.Env, config.EnvVarConfig{Name: gatedMCPExitsEnv, Value: "1"})
	return srv
}

// jsonEntry is a declaration in the shape of an mcp.json entry.
func jsonEntry(srv config.MCPServerConfig) config.MCPJSONServer {
	env := make(map[string]string, len(srv.Env))
	for _, e := range srv.Env {
		env[e.Name] = e.Value
	}
	return config.MCPJSONServer{Command: srv.Command, Args: srv.Args, Env: env}
}

// newPoolTestManager builds a manager over cfg whose servers are all stopped,
// and waited for, before the test's temporary folders go.
func newPoolTestManager(t *testing.T, cfg *config.Config, runner AgentRunner) *Manager {
	t.Helper()
	mgr := NewManager(cfg, mcpTestSender{}, runner, slog.Default(), t.TempDir(), nil)
	mgr.SetMCPStopDelayForTest(0)
	t.Cleanup(mgr.CloseMCP)
	return mgr
}

// TestSessionsShareOneGlobalMCPServer: a server of the global configuration is
// one process for every session of the manager, not one per session.
func TestSessionsShareOneGlobalMCPServer(t *testing.T) {
	started := filepath.Join(t.TempDir(), "started")
	mgr := newPoolTestManager(t, reloadTestConfig(t, countedMCPServer("shared", started)), nil)
	first := newPoolTestSession(t, mgr, t.TempDir())
	second := newPoolTestSession(t, mgr, t.TempDir())
	for _, st := range []*State{first, second} {
		if got := clientNames(st); len(got) != 1 || got[0] != "shared" {
			t.Fatalf("clients = %v, want [shared]", got)
		}
	}
	if got := spawns(started); got != 1 {
		t.Fatalf("the global server was started %d times for two sessions, want once", got)
	}
}

// TestProjectMCPServerRunsOncePerWorkspace: a project server is one process
// for the sessions of its workspace, another workspace declaring the same
// command gets a process of its own, and the process stops once the last
// session of its workspace lets it go.
func TestProjectMCPServerRunsOncePerWorkspace(t *testing.T) {
	started := filepath.Join(t.TempDir(), "started")
	cfg := workspaceTestConfig(t, config.ProjectTrustAllow)
	projectA, projectB := t.TempDir(), t.TempDir()
	for _, dir := range []string{projectA, projectB} {
		if err := config.UpsertMCPJSONServer(config.MCPJSONPath(dir), "proj", jsonEntry(countedMCPServer("", started))); err != nil {
			t.Fatal(err)
		}
	}
	mgr := newPoolTestManager(t, cfg, nil)

	a1 := newPoolTestSession(t, mgr, projectA)
	a2 := newPoolTestSession(t, mgr, projectA)
	if got := spawns(started); got != 1 {
		t.Fatalf("two sessions of one workspace started the project server %d times, want once", got)
	}
	b := newPoolTestSession(t, mgr, projectB)
	if got := spawns(started); got != 2 {
		t.Fatalf("a second workspace brought the starts to %d, want 2", got)
	}
	for _, st := range []*State{a1, a2, b} {
		if got := clientNames(st); len(got) != 1 || got[0] != "proj" {
			t.Fatalf("clients = %v, want [proj]", got)
		}
	}

	a1.CloseAll()
	time.Sleep(300 * time.Millisecond)
	if got := exits(started); got != 0 {
		t.Fatalf("the project server stopped while a session of its workspace still used it (exits = %d)", got)
	}
	a2.CloseAll()
	if !waitUntil(t, 5*time.Second, func() bool { return exits(started) == 1 }) {
		t.Fatalf("the project server kept running after the last session of its workspace closed (exits = %d)", exits(started))
	}
	time.Sleep(300 * time.Millisecond)
	if got := exits(started); got != 1 {
		t.Fatalf("closing workspace A stopped %d servers, want only its own", got)
	}
	running := mgr.MCPPoolServers()
	if len(running) != 1 || running[0].Workspace != mcp.CanonicalWorkspace(projectB) {
		t.Fatalf("the pool runs %+v, want only the server of workspace B", running)
	}
	b.CloseAll()
	if !waitUntil(t, 5*time.Second, func() bool { return exits(started) == 2 }) {
		t.Fatalf("the server of workspace B kept running after its session closed (exits = %d)", exits(started))
	}
}

// TestGlobalMCPServersStartWithTheManagerAndOutliveItsSessions: a surface
// that starts the global servers has them running before any session, every
// session uses that one process, and closing the sessions leaves it up until
// the manager lets go of its servers.
func TestGlobalMCPServersStartWithTheManagerAndOutliveItsSessions(t *testing.T) {
	started := filepath.Join(t.TempDir(), "started")
	mgr := newPoolTestManager(t, reloadTestConfig(t, countedMCPServer("shared", started)), nil)
	mgr.StartGlobalMCPServers()
	if !waitUntil(t, 10*time.Second, func() bool {
		running := mgr.MCPPoolServers()
		return len(running) == 1 && running[0].Connected && running[0].Kept
	}) {
		t.Fatalf("the global server did not start with the manager: %+v", mgr.MCPPoolServers())
	}
	first := newPoolTestSession(t, mgr, t.TempDir())
	second := newPoolTestSession(t, mgr, t.TempDir())
	if got := spawns(started); got != 1 {
		t.Fatalf("starts = %d, want the one the manager made", got)
	}
	first.CloseAll()
	second.CloseAll()
	time.Sleep(300 * time.Millisecond)
	if got := exits(started); got != 0 {
		t.Fatal("the global server stopped when its sessions closed")
	}
	mgr.CloseMCP()
	if got := exits(started); got != 1 {
		t.Fatalf("exits after CloseMCP = %d, want 1", got)
	}
}

// TestKeptGlobalServerFollowsItsSwitch: switching a kept global server off
// stops its process, and switching it on starts it again with no session.
func TestKeptGlobalServerFollowsItsSwitch(t *testing.T) {
	started := filepath.Join(t.TempDir(), "started")
	cfg := workspaceTestConfig(t, config.ProjectTrustAsk)
	if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(cfg.Paths.Home), "kept", jsonEntry(countedMCPServer("", started))); err != nil {
		t.Fatal(err)
	}
	mgr := newPoolTestManager(t, cfg, nil)
	mgr.StartGlobalMCPServers()
	if !waitUntil(t, 10*time.Second, func() bool { return spawns(started) == 1 }) {
		t.Fatal("the kept server did not start")
	}
	cwd := t.TempDir()
	st := newPoolTestSession(t, mgr, cwd)
	if err := mgr.SetMCPEnabled(context.Background(), cwd, "kept", "", false); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(t, 5*time.Second, func() bool { return exits(started) == 1 }) {
		t.Fatal("switching the server off left its process running")
	}
	if got := clientNames(st); len(got) != 0 {
		t.Fatalf("the session still holds %v", got)
	}
	if err := mgr.SetMCPEnabled(context.Background(), cwd, "kept", "", true); err != nil {
		t.Fatal(err)
	}
	if got := clientNames(st); len(got) != 1 || got[0] != "kept" {
		t.Fatalf("clients after switching back on = %v, want [kept]", got)
	}
	if got := spawns(started); got != 2 {
		t.Fatalf("starts = %d, want one more for the switch", got)
	}
	st.CloseAll()
	time.Sleep(300 * time.Millisecond)
	if got := exits(started); got != 1 {
		t.Fatal("the server the process keeps stopped with its session")
	}
}

// TestAddingAServerDoesNotRestartTheOthers: an edit of <home>/mcp.json that
// adds a server reaches every session, and a server whose declaration did
// not change keeps its one process.
func TestAddingAServerDoesNotRestartTheOthers(t *testing.T) {
	dir := t.TempDir()
	stay, added := filepath.Join(dir, "stay"), filepath.Join(dir, "added")
	mgr := newPoolTestManager(t, reloadTestConfig(t, countedMCPServer("stay", stay)), nil)
	first := newPoolTestSession(t, mgr, t.TempDir())
	second := newPoolTestSession(t, mgr, t.TempDir())
	writeHomeMCP(t, mgr.activeCfg().Paths.Home, countedMCPServer("stay", stay), countedMCPServer("added", added))
	mgr.ReloadMCPDeclarations(context.Background())
	for _, st := range []*State{first, second} {
		if got := clientNames(st); len(got) != 2 {
			t.Fatalf("clients after the change = %v, want [stay added]", got)
		}
	}
	if got := spawns(stay); got != 1 {
		t.Fatalf("the unchanged server was started %d times, want once", got)
	}
	if got := exits(stay); got != 0 {
		t.Fatal("the unchanged server was stopped by the change")
	}
	if got := spawns(added); got != 1 {
		t.Fatalf("the added server was started %d times for two sessions, want once", got)
	}
}

// TestEndedSharedServerIsStartedAgainAtTheNextTurn: a shared server that
// dies leaves its sessions a dead connection only until their next turn,
// which starts it again.
func TestEndedSharedServerIsStartedAgainAtTheNextTurn(t *testing.T) {
	dir := t.TempDir()
	started, pidFile := filepath.Join(dir, "started"), filepath.Join(dir, "pid")
	srv := countedMCPServer("fragile", started)
	srv.Env = append(srv.Env, config.EnvVarConfig{Name: gatedMCPPIDEnv, Value: pidFile})
	entered := make(chan []string, 4)
	mgr := newPoolTestManager(t, reloadTestConfig(t, srv), namesRunner(entered))
	st := newPoolTestSession(t, mgr, t.TempDir())
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Kill(); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(t, 5*time.Second, func() bool { return len(st.endedConfiguredMCPServers()) == 1 }) {
		t.Fatal("the session does not see that its server died")
	}
	if names := promptNames(t, mgr, st.GetID(), entered); len(names) != 1 || names[0] != "fragile" {
		t.Fatalf("the next turn found %v, want [fragile]", names)
	}
	if got := spawns(started); got != 2 {
		t.Fatalf("starts = %d, want the server started again", got)
	}
	if ended := st.endedConfiguredMCPServers(); len(ended) != 0 {
		t.Fatalf("the turn runs on a dead connection to %v", ended)
	}
}

// TestSubagentUsesItsParentsSharedServer: a subagent spawned with MCP gets
// a lease on the server its parent runs, not a process of its own.
func TestSubagentUsesItsParentsSharedServer(t *testing.T) {
	started := filepath.Join(t.TempDir(), "started")
	cfg := reloadTestConfig(t, countedMCPServer("shared", started))
	mgr := NewManager(cfg, mcpTestSender{}, nil, slog.Default(), t.TempDir(), &FileStore{Root: t.TempDir()})
	t.Cleanup(mgr.CloseMCP)
	cwd := t.TempDir()
	parent := newPoolTestSession(t, mgr, cwd)
	child, err := mgr.CreateSubagentSession(context.Background(), SubagentSpec{
		ID: NewSessionID(), ParentSessionID: parent.GetID(), Name: "general", CWD: cwd,
		Tools: []string{"read"}, ConnectMCP: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(child.CloseAll)
	if got := clientNames(child); len(got) != 1 || got[0] != "shared" {
		t.Fatalf("the child holds %v, want [shared]", got)
	}
	if got := spawns(started); got != 1 {
		t.Fatalf("parent and child started the server %d times, want once", got)
	}
}

// TestMCPServersListingStartsNoSecondCopy: listing the servers for /mcp
// reads a running server's tools from its connection instead of spawning a
// probe of its own.
func TestMCPServersListingStartsNoSecondCopy(t *testing.T) {
	started := filepath.Join(t.TempDir(), "started")
	mgr := newPoolTestManager(t, reloadTestConfig(t, countedMCPServer("shared", started)), nil)
	cwd := t.TempDir()
	newPoolTestSession(t, mgr, cwd)
	rows, err := mgr.MCPServers(context.Background(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Status != "connected" || len(rows[0].Tools) != 1 {
		t.Fatalf("rows = %+v", rows)
	}
	if got := spawns(started); got != 1 {
		t.Fatalf("listing the servers started %d processes in all, want the session's one", got)
	}
}

// TestSubagentSharesItsParentsClientServer: a server an ACP client sent with
// session/new is one process for the session and the subagent that redials
// it, not one each.
func TestSubagentSharesItsParentsClientServer(t *testing.T) {
	started := filepath.Join(t.TempDir(), "started")
	srv := countedMCPServer("editor", started)
	decl := acp.MCPServer{Type: srv.Type, Name: srv.Name, Command: srv.Command, Args: srv.Args}
	for _, e := range srv.Env {
		decl.Env = append(decl.Env, acp.EnvVariable{Name: e.Name, Value: e.Value})
	}
	mgr := NewManager(reloadTestConfig(t), mcpTestSender{}, nil, slog.Default(), t.TempDir(), &FileStore{Root: t.TempDir()})
	mgr.SetMCPStopDelayForTest(0)
	t.Cleanup(mgr.CloseMCP)
	cwd := t.TempDir()
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd, MCPServers: []acp.MCPServer{decl}})
	if err != nil {
		t.Fatal(err)
	}
	parent := mgr.SessionByID(created.SessionID)
	t.Cleanup(parent.CloseAll)
	child, err := mgr.CreateSubagentSession(context.Background(), SubagentSpec{
		ID: NewSessionID(), ParentSessionID: parent.GetID(), Name: "general", CWD: cwd,
		Tools: []string{"read"}, ConnectMCP: true, ClientMCPServers: parent.SessionMCPDeclarations(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := clientNames(child); len(got) != 1 || got[0] != "editor" {
		t.Fatalf("the child holds %v, want [editor]", got)
	}
	if got := spawns(started); got != 1 {
		t.Fatalf("the session and its subagent started the server %d times, want once", got)
	}
	child.CloseAll()
	time.Sleep(300 * time.Millisecond)
	if got := exits(started); got != 0 {
		t.Fatal("the subagent's close stopped the server its parent still holds")
	}
	parent.CloseAll()
	if !waitUntil(t, 5*time.Second, func() bool { return exits(started) == 1 }) {
		t.Fatal("the server kept running after the session that sent it closed")
	}
}

// TestUnheldProjectServerWaitsOutTheIdleTimeout: a project server no session
// holds keeps running for mcp.idle_timeout_seconds, and a session that opens
// in the meantime takes the running process.
func TestUnheldProjectServerWaitsOutTheIdleTimeout(t *testing.T) {
	started := filepath.Join(t.TempDir(), "started")
	cfg := workspaceTestConfig(t, config.ProjectTrustAllow)
	one := 1
	cfg.MCP.IdleTimeoutSeconds = &one
	project := t.TempDir()
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(project), "proj", jsonEntry(countedMCPServer("", started))); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(cfg, mcpTestSender{}, nil, slog.Default(), t.TempDir(), nil)
	t.Cleanup(mgr.CloseMCP)
	first := newPoolTestSession(t, mgr, project)
	first.CloseAll()
	second := newPoolTestSession(t, mgr, project)
	if got := spawns(started); got != 1 {
		t.Fatalf("starts = %d, want the session that opened within the timeout to take the running server", got)
	}
	second.CloseAll()
	time.Sleep(300 * time.Millisecond)
	if got := exits(started); got != 0 {
		t.Fatal("the server stopped before the idle timeout passed")
	}
	if !waitUntil(t, 5*time.Second, func() bool { return exits(started) == 1 }) {
		t.Fatal("the server kept running after the idle timeout passed")
	}
}

// TestSwitchedOffProjectServerStopsAtOnce: the idle timeout is for a server
// the configuration still wants; one switched off stops as soon as the last
// session let it go.
func TestSwitchedOffProjectServerStopsAtOnce(t *testing.T) {
	started := filepath.Join(t.TempDir(), "started")
	cfg := workspaceTestConfig(t, config.ProjectTrustAllow)
	hour := 3600
	cfg.MCP.IdleTimeoutSeconds = &hour
	project := t.TempDir()
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(project), "proj", jsonEntry(countedMCPServer("", started))); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(cfg, mcpTestSender{}, nil, slog.Default(), t.TempDir(), nil)
	t.Cleanup(mgr.CloseMCP)
	st := newPoolTestSession(t, mgr, project)
	if got := clientNames(st); len(got) != 1 {
		t.Fatalf("clients = %v, want [proj]", got)
	}
	if err := mgr.SetMCPEnabled(context.Background(), project, "proj", "", false); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(t, 5*time.Second, func() bool { return exits(started) == 1 }) {
		t.Fatal("a switched-off server waited out the idle timeout")
	}
}

// TestProjectTrustPolicyChangeReachesLiveSessions: moving mcp.project_trust
// to deny takes a project server away from the sessions that hold it, not
// only from new ones, and the server stops.
func TestProjectTrustPolicyChangeReachesLiveSessions(t *testing.T) {
	started := filepath.Join(t.TempDir(), "started")
	cfg := workspaceTestConfig(t, config.ProjectTrustAllow)
	project := t.TempDir()
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(project), "proj", jsonEntry(countedMCPServer("", started))); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(cfg, mcpTestSender{}, nil, slog.Default(), t.TempDir(), nil)
	t.Cleanup(mgr.CloseMCP)
	first := newPoolTestSession(t, mgr, project)
	second := newPoolTestSession(t, mgr, project)
	next := *cfg
	next.MCP = config.MCP{ProjectTrust: config.ProjectTrustDeny, IdleTimeoutSeconds: cfg.MCP.IdleTimeoutSeconds}
	mgr.ReplaceConfig(&next)
	for _, st := range []*State{first, second} {
		if got := clientNames(st); len(got) != 0 {
			t.Fatalf("a live session still holds %v under project_trust: deny", got)
		}
	}
	if !waitUntil(t, 5*time.Second, func() bool { return exits(started) == 1 }) {
		t.Fatal("the project server kept running after the policy denied it")
	}
}

// TestTurnAfterAnUnfinishedWorkspaceSwitchRunsOnTheNewWorkspace: a workspace
// switch whose reload ran out of time leaves the previous folder's servers on
// the session; the next turn is handed the new folder's servers, not those.
func TestTurnAfterAnUnfinishedWorkspaceSwitchRunsOnTheNewWorkspace(t *testing.T) {
	dir := t.TempDir()
	cfg := workspaceTestConfig(t, config.ProjectTrustAllow)
	projectA, projectB := t.TempDir(), t.TempDir()
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(projectA), "a-srv", jsonEntry(countedMCPServer("", filepath.Join(dir, "a")))); err != nil {
		t.Fatal(err)
	}
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(projectB), "b-srv", jsonEntry(countedMCPServer("", filepath.Join(dir, "b")))); err != nil {
		t.Fatal(err)
	}
	entered := make(chan []string, 2)
	mgr := NewManager(cfg, mcpTestSender{}, namesRunner(entered), slog.Default(), t.TempDir(), nil)
	mgr.SetMCPStopDelayForTest(0)
	t.Cleanup(mgr.CloseMCP)
	st := newPoolTestSession(t, mgr, projectA)
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if err := mgr.SetSessionWorkspace(expired, st, projectB); err != nil {
		t.Fatal(err)
	}
	if names := promptNames(t, mgr, st.GetID(), entered); len(names) != 1 || names[0] != "b-srv" {
		t.Fatalf("the turn after the switch found %v, want [b-srv]", names)
	}
}

// TestSwitchedOffServerWaitingOutItsTimeoutStopsAtOnce: a project server no
// session holds is waiting out a long idle timeout; switching it off then
// stops it at once rather than at the end of the timeout.
func TestSwitchedOffServerWaitingOutItsTimeoutStopsAtOnce(t *testing.T) {
	started := filepath.Join(t.TempDir(), "started")
	cfg := workspaceTestConfig(t, config.ProjectTrustAllow)
	hour := 3600
	cfg.MCP.IdleTimeoutSeconds = &hour
	project := t.TempDir()
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(project), "proj", jsonEntry(countedMCPServer("", started))); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(cfg, mcpTestSender{}, nil, slog.Default(), t.TempDir(), nil)
	t.Cleanup(mgr.CloseMCP)
	newPoolTestSession(t, mgr, project).CloseAll()
	time.Sleep(200 * time.Millisecond)
	if got := exits(started); got != 0 {
		t.Fatal("the server stopped before its idle timeout while still switched on")
	}
	if err := mgr.SetMCPEnabled(context.Background(), project, "proj", "", false); err != nil {
		t.Fatal(err)
	}
	if !waitUntil(t, 5*time.Second, func() bool { return exits(started) == 1 }) {
		t.Fatal("a server switched off while waiting out its idle timeout kept running")
	}
}

// TestProjectTrustChangedInPlaceReachesLiveSessions: the MCP servers tab
// writes the policy into the live configuration before it reloads the file
// (mcp.SetProjectTrust), so the reload has to compare the new policy with
// the one the sessions run under, not with that already changed object.
func TestProjectTrustChangedInPlaceReachesLiveSessions(t *testing.T) {
	started := filepath.Join(t.TempDir(), "started")
	cfg := workspaceTestConfig(t, config.ProjectTrustAllow)
	project := t.TempDir()
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(project), "proj", jsonEntry(countedMCPServer("", started))); err != nil {
		t.Fatal(err)
	}
	mgr := NewManager(cfg, mcpTestSender{}, nil, slog.Default(), t.TempDir(), nil)
	t.Cleanup(mgr.CloseMCP)
	st := newPoolTestSession(t, mgr, project)
	mgr.Cfg().MCP.ProjectTrust = config.ProjectTrustDeny
	reloaded := *mgr.Cfg()
	mgr.ReplaceConfig(&reloaded)
	if got := clientNames(st); len(got) != 0 {
		t.Fatalf("the live session still holds %v after the policy moved to deny", got)
	}
	if !waitUntil(t, 5*time.Second, func() bool { return exits(started) == 1 }) {
		t.Fatal("the project server kept running after the policy denied it")
	}
}

// TestUnfinishedParkedReloadDropsTheOldWorkspacesServers: when the reload a
// workspace switch left for the next turn cannot finish in time either, the
// turn still does not get the previous folder's servers.
func TestUnfinishedParkedReloadDropsTheOldWorkspacesServers(t *testing.T) {
	dir := t.TempDir()
	cfg := workspaceTestConfig(t, config.ProjectTrustAllow)
	projectA, projectB := t.TempDir(), t.TempDir()
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(projectA), "a-srv", jsonEntry(countedMCPServer("", filepath.Join(dir, "a")))); err != nil {
		t.Fatal(err)
	}
	hung := gatedMCPServer("", filepath.Join(dir, "b"), filepath.Join(dir, "never"))
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(projectB), "b-srv", jsonEntry(hung)); err != nil {
		t.Fatal(err)
	}
	entered := make(chan []string, 2)
	mgr := NewManager(cfg, mcpTestSender{}, namesRunner(entered), slog.Default(), t.TempDir(), nil)
	mgr.SetMCPStopDelayForTest(0)
	mgr.SetMCPConnectTimeoutForTest(5 * time.Second)
	t.Cleanup(mgr.CloseMCP)
	st := newPoolTestSession(t, mgr, projectA)
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	if err := mgr.SetSessionWorkspace(expired, st, projectB); err != nil {
		t.Fatal(err)
	}
	prev := mcpStartTimeout
	mcpStartTimeout = 300 * time.Millisecond
	t.Cleanup(func() { mcpStartTimeout = prev })
	if names := promptNames(t, mgr, st.GetID(), entered); len(names) != 0 {
		t.Fatalf("the turn found %v, want none: a-srv belongs to the folder the session left", names)
	}
}
