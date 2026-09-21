package session

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks/hooktest"
)

// TestHelperHook is not a real test: re-executed with the hook-helper
// positional arguments it becomes the hook process hooktest spawns.
func TestHelperHook(t *testing.T) {
	if !hooktest.Main(flag.Args()) {
		t.Skip("helper process")
	}
}

// workspaceProbeEntry declares the shared stdio stub (TestMCPSettingsHelperProcess,
// re-executed from this test binary) in a folder's project .coddy/mcp.json.
func workspaceProbeEntry() config.MCPJSONServer {
	return config.MCPJSONServer{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestMCPSettingsHelperProcess$"},
		Env:     map[string]string{reloadTestMCPHelperEnv: "1"},
	}
}

func writeProjectMCPServer(t *testing.T, dir, name string) {
	t.Helper()
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(dir), name, workspaceProbeEntry()); err != nil {
		t.Fatalf("write %s/.coddy/mcp.json: %v", dir, err)
	}
}

// workspaceTestConfig builds a config with a temp home and the given project
// trust policy, so the mcp.json layers stay inside the test's own directories.
func workspaceTestConfig(t *testing.T, projectTrust string, servers ...config.MCPServerConfig) *config.Config {
	t.Helper()
	cfg := reloadTestConfig(servers...)
	cfg.Paths.Home = t.TempDir()
	cfg.MCP.ProjectTrust = projectTrust
	return cfg
}

// newWorkspaceTestManager starts a manager with cfg and a session rooted at
// cwd, returning the live state.
func newWorkspaceTestManager(t *testing.T, cfg *config.Config, cwd string) (*Manager, *State) {
	t.Helper()
	mgr := NewManager(cfg, nil, nil, slog.Default(), t.TempDir(), nil)
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: cwd})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	st := mgr.SessionByID(created.SessionID)
	if st == nil {
		t.Fatalf("session %q was not registered", created.SessionID)
	}
	t.Cleanup(st.CloseAll)
	return mgr, st
}

func mcpClientNames(st *State) []string {
	clients := st.GetMCPClients()
	names := make([]string, 0, len(clients))
	for _, c := range clients {
		names = append(names, c.Name())
	}
	return names
}

// A workspace switch must swap the configured MCP servers: the previous
// workspace's clients are closed and the new workspace's .coddy/mcp.json is
// merged and dialed through the trust gate.
func TestSetSessionWorkspaceReconnectsConfiguredMCP(t *testing.T) {
	alpha := t.TempDir()
	beta := t.TempDir()
	writeProjectMCPServer(t, alpha, "alpha-probe")
	writeProjectMCPServer(t, beta, "beta-probe")

	mgr, st := newWorkspaceTestManager(t, workspaceTestConfig(t, config.ProjectTrustAllow), alpha)

	if got := mcpClientNames(st); !reflect.DeepEqual(got, []string{"alpha-probe"}) {
		t.Fatalf("clients at alpha = %v, want [alpha-probe]", got)
	}
	alphaClient := st.GetMCPClients()[0]

	if err := mgr.SetSessionWorkspace(context.Background(), st, beta); err != nil {
		t.Fatalf("SetSessionWorkspace: %v", err)
	}
	if got := mcpClientNames(st); !reflect.DeepEqual(got, []string{"beta-probe"}) {
		t.Fatalf("clients after switch = %v, want [beta-probe]", got)
	}
	if _, err := alphaClient.CallTool(context.Background(), "ping", "{}"); err == nil {
		t.Fatal("the previous workspace's client must be closed")
	}
}

// Switching to another spelling of the same workspace must not kill and
// respawn every configured server.
func TestSetSessionWorkspaceSamePathDoesNotRedial(t *testing.T) {
	alpha := t.TempDir()
	writeProjectMCPServer(t, alpha, "alpha-probe")

	mgr, st := newWorkspaceTestManager(t, workspaceTestConfig(t, config.ProjectTrustAllow), alpha)
	before := st.GetMCPClients()
	if len(before) != 1 {
		t.Fatalf("clients = %d, want 1", len(before))
	}

	link := filepath.Join(t.TempDir(), "alpha-link")
	if err := os.Symlink(alpha, link); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	if err := mgr.SetSessionWorkspace(context.Background(), st, link); err != nil {
		t.Fatalf("SetSessionWorkspace: %v", err)
	}
	after := st.GetMCPClients()
	if len(after) != 1 || after[0] != before[0] {
		t.Fatal("a same-workspace switch must keep the same MCP client instances")
	}
	if got := st.GetCWD(); got != link {
		t.Fatalf("cwd = %q, want the spelling the switch was given: %q", got, link)
	}
}

// Under the default ask policy the new workspace's project-local servers stay
// cold: a switch must not start an unapproved declaration. Global servers
// still re-dial without approval.
func TestSetSessionWorkspaceGatesNewProjectServers(t *testing.T) {
	alpha := t.TempDir()
	beta := t.TempDir()
	marker := filepath.Join(t.TempDir(), "marker.txt")
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(beta), "beta-probe", config.MCPJSONServer{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestHelperMCPMarkerServer"},
		Env: map[string]string{
			"GO_WANT_MCP_MARKER":    "1",
			"CODDY_MCP_MARKER_FILE": marker,
		},
	}); err != nil {
		t.Fatalf("write beta mcp.json: %v", err)
	}

	mgr, st := newWorkspaceTestManager(t,
		workspaceTestConfig(t, config.ProjectTrustAsk, reloadTestMCPServer("global-probe")), alpha)

	if got := mcpClientNames(st); !reflect.DeepEqual(got, []string{"global-probe"}) {
		t.Fatalf("clients at alpha = %v, want [global-probe]", got)
	}
	globalClient := st.GetMCPClients()[0]
	if err := mgr.SetSessionWorkspace(context.Background(), st, beta); err != nil {
		t.Fatalf("SetSessionWorkspace: %v", err)
	}
	if got := mcpClientNames(st); !reflect.DeepEqual(got, []string{"global-probe"}) {
		t.Fatalf("clients after switch = %v, want the re-dialed [global-probe] only", got)
	}
	if st.GetMCPClients()[0] == globalClient {
		t.Fatal("the global server must be re-dialed for the new workspace, not kept")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("an unapproved project server must not have started on the switch")
	}
}

// The per-turn tool filter must reflect the NEW workspace's merged server
// list, not the one the session was created in (the factory captured the old
// cwd before this fix).
func TestSetSessionWorkspaceRefreshesMCPFilter(t *testing.T) {
	alpha := t.TempDir()
	beta := t.TempDir()
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(beta), "beta-srv", config.MCPJSONServer{
		Command:       os.Args[0],
		Args:          []string{"-test.run=^TestMCPSettingsHelperProcess$"},
		Env:           map[string]string{reloadTestMCPHelperEnv: "1"},
		DisabledTools: []string{"secret"},
	}); err != nil {
		t.Fatalf("write beta mcp.json: %v", err)
	}

	mgr, st := newWorkspaceTestManager(t, workspaceTestConfig(t, config.ProjectTrustAllow), alpha)
	if filter := st.GetMCPToolFilter(); !filter("beta-srv", "secret") {
		t.Fatal("before the switch the filter knows nothing of beta's servers")
	}

	if err := mgr.SetSessionWorkspace(context.Background(), st, beta); err != nil {
		t.Fatalf("SetSessionWorkspace: %v", err)
	}
	filter := st.GetMCPToolFilter()
	if filter("beta-srv", "secret") {
		t.Fatal("disabledTools of the new workspace must be honored after the switch")
	}
	if !filter("beta-srv", "ping") {
		t.Fatal("tools not in disabledTools must stay enabled")
	}
}

// A failed dial in the new workspace must not fail the switch: the session
// moves and the unreachable server is skipped like at session creation.
func TestSetSessionWorkspaceFailedDialKeepsSwitch(t *testing.T) {
	alpha := t.TempDir()
	beta := t.TempDir()
	writeProjectMCPServer(t, alpha, "alpha-probe")
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(beta), "beta-dead", config.MCPJSONServer{
		Command: filepath.Join(beta, "no-such-binary"),
	}); err != nil {
		t.Fatalf("write beta mcp.json: %v", err)
	}

	mgr, st := newWorkspaceTestManager(t, workspaceTestConfig(t, config.ProjectTrustAllow), alpha)
	if err := mgr.SetSessionWorkspace(context.Background(), st, beta); err != nil {
		t.Fatalf("a dead server must not fail the switch: %v", err)
	}
	if got := st.GetCWD(); got != beta {
		t.Fatalf("cwd = %q, want %q", got, beta)
	}
	if got := mcpClientNames(st); len(got) != 0 {
		t.Fatalf("clients = %v, want none (the only configured server is dead)", got)
	}
}

// SessionStart hooks re-fire for the new workspace under the "workspace"
// source, so the stored hook context stops describing the old workspace; a
// hook that only matches "startup" keeps its once-per-session semantics.
func TestSetSessionWorkspaceRefiresSessionStartHooks(t *testing.T) {
	alpha := t.TempDir()
	beta := t.TempDir()
	home := t.TempDir()

	hooksFile := filepath.Join(home, "hooks.json")
	if err := hooktest.Write(hooksFile,
		hooktest.Entry{
			Event:    "SessionStart",
			Matcher:  "workspace",
			Handlers: []hooks.Handler{hooktest.Handler("context", "beta-context")},
		},
		hooktest.Entry{
			Event:    "SessionStart",
			Matcher:  "startup",
			Handlers: []hooks.Handler{hooktest.Handler("context", "startup-context")},
		},
	); err != nil {
		t.Fatalf("write hooks file: %v", err)
	}

	cfg := workspaceTestConfig(t, config.ProjectTrustAllow)
	cfg.Paths.Home = home
	cfg.Hooks.ApplyDefaults(cfg.Paths)
	cfg.Hooks.Files = append(cfg.Hooks.Files, hooksFile)

	mgr, st := newWorkspaceTestManager(t, cfg, alpha)
	if got := st.GetHookContext(); got != "startup-context" {
		t.Fatalf("context after creation = %q, want the startup hook's output", got)
	}

	if err := mgr.SetSessionWorkspace(context.Background(), st, beta); err != nil {
		t.Fatalf("SetSessionWorkspace: %v", err)
	}
	if got := st.GetHookContext(); got != "beta-context" {
		t.Fatalf("context after switch = %q, want the workspace hook's output", got)
	}
}

// While a turn holds the session lock, a workspace switch must not swap the
// configured MCP clients underneath it: the reload parks on the pending flag
// and drains when the turn releases the lock. The rest of the workspace state
// (cwd, skills, hooks) still moves immediately.
func TestSetSessionWorkspaceParksMCPReloadWhileTurnActive(t *testing.T) {
	alpha := t.TempDir()
	beta := t.TempDir()
	writeProjectMCPServer(t, alpha, "alpha-probe")
	writeProjectMCPServer(t, beta, "beta-probe")

	mgr, st := newWorkspaceTestManager(t, workspaceTestConfig(t, config.ProjectTrustAllow), alpha)
	if got := mcpClientNames(st); !reflect.DeepEqual(got, []string{"alpha-probe"}) {
		t.Fatalf("clients at alpha = %v, want [alpha-probe]", got)
	}

	// Holding the lock through acquireTurnLockWithReloadDrain makes unlock()
	// drain the parked reload, exactly like a finished prompt turn does.
	unlock, err := mgr.acquireTurnLockWithReloadDrain(st.GetID(), st)
	if err != nil {
		t.Fatalf("hold the turn lock: %v", err)
	}
	if err := mgr.SetSessionWorkspace(context.Background(), st, beta); err != nil {
		t.Fatalf("SetSessionWorkspace under a held turn lock: %v", err)
	}
	if got := st.GetCWD(); got != beta {
		t.Fatalf("cwd = %q, want %q", got, beta)
	}
	if got := mcpClientNames(st); !reflect.DeepEqual(got, []string{"alpha-probe"}) {
		t.Fatalf("clients while a turn holds the lock = %v, want the parked [alpha-probe]", got)
	}
	if !st.hasPendingMCPReload() {
		t.Fatal("the MCP reload must stay parked while a turn holds the lock")
	}

	unlock()
	if got := mcpClientNames(st); !reflect.DeepEqual(got, []string{"beta-probe"}) {
		t.Fatalf("clients after the turn released the lock = %v, want [beta-probe]", got)
	}
	if st.hasPendingMCPReload() {
		t.Fatal("the parked reload must have drained with the turn release")
	}
}

// An ACP client-supplied server is the session's own, not the workspace's: a
// workspace switch keeps it connected while the configured set is swapped.
func TestSetSessionWorkspaceKeepsSessionMCPClients(t *testing.T) {
	alpha := t.TempDir()
	beta := t.TempDir()
	writeProjectMCPServer(t, beta, "beta-probe")

	cfg := workspaceTestConfig(t, config.ProjectTrustAllow)
	mgr := NewManager(cfg, nil, nil, slog.Default(), t.TempDir(), nil)
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{
		CWD: alpha,
		MCPServers: []acp.MCPServer{{
			Type:    "stdio",
			Name:    "client-probe",
			Command: os.Args[0],
			Args:    []string{"-test.run=^TestMCPSettingsHelperProcess$"},
			Env:     []acp.EnvVariable{{Name: reloadTestMCPHelperEnv, Value: "1"}},
		}},
	})
	if err != nil {
		t.Fatalf("session/new: %v", err)
	}
	st := mgr.SessionByID(created.SessionID)
	if st == nil {
		t.Fatalf("session %q was not registered", created.SessionID)
	}
	t.Cleanup(st.CloseAll)

	if got := mcpClientNames(st); !reflect.DeepEqual(got, []string{"client-probe"}) {
		t.Fatalf("clients at alpha = %v, want [client-probe]", got)
	}
	if err := mgr.SetSessionWorkspace(context.Background(), st, beta); err != nil {
		t.Fatalf("SetSessionWorkspace: %v", err)
	}
	if got := mcpClientNames(st); !reflect.DeepEqual(got, []string{"beta-probe", "client-probe"}) {
		t.Fatalf("clients after switch = %v, want [beta-probe client-probe]", got)
	}
}
