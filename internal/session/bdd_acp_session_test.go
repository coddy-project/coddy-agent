package session_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/cucumber/godog"
)

const (
	testACPHelperEnv = "CODDY_TEST_ACP_HELPER"
	testMCPHelperEnv = "CODDY_TEST_MCP_HELPER"
	testSkillsDirEnv = "CODDY_TEST_SKILLS_DIR"
	testWorkspaceEnv = "CODDY_TEST_WORKSPACE"
	testHomeEnv      = "CODDY_TEST_HOME"
)

type acpSessionFeatureState struct {
	root        string
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	lines       chan []byte
	readErrs    chan error
	stderr      bytes.Buffer
	newWire     []map[string]interface{}
	modeWire    []map[string]interface{}
	mgr         *session.Manager
	sessionID   string
	activeState *session.State
}

func (s *acpSessionFeatureState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-acp-bdd-")
	if err != nil {
		return err
	}
	s.root = root
	s.newWire = nil
	s.modeWire = nil
	s.stderr.Reset()
	return nil
}

func (s *acpSessionFeatureState) close() {
	if s.activeState != nil {
		s.activeState.CloseAll()
	}
	s.activeState = nil
	s.mgr = nil
	if s.stdin != nil {
		_ = s.stdin.Close()
		s.stdin = nil
	}
	if s.cmd != nil && s.cmd.Process != nil {
		done := make(chan error, 1)
		go func() { done <- s.cmd.Wait() }()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = s.cmd.Process.Kill()
			<-done
		}
	}
	s.cmd = nil
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

func (s *acpSessionFeatureState) coddyACPHasLoadedSkill(name string) error {
	skillDir := filepath.Join(s.root, "skills", name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("---\nname: %s\ndescription: Routes context for the active workspace.\n---\n\nUse the active workspace context.\n", name)
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		return err
	}
	return s.startACP(filepath.Join(s.root, "skills"))
}

func (s *acpSessionFeatureState) anACPClientHasAnActiveSession() error {
	if err := s.startACP(""); err != nil {
		return err
	}
	return s.createACPSession()
}

func (s *acpSessionFeatureState) startACP(skillsDir string) error {
	if s.cmd != nil {
		return nil
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestACPFeatureHelperProcess$")
	cmd.Env = append(os.Environ(),
		testACPHelperEnv+"=1",
		testSkillsDirEnv+"="+skillsDir,
		testWorkspaceEnv+"="+s.root,
		testHomeEnv+"="+filepath.Join(s.root, "home"),
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = &s.stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	s.cmd = cmd
	s.stdin = stdin
	s.lines = make(chan []byte, 8)
	s.readErrs = make(chan error, 1)
	go s.scanACPOutput(stdout)
	return nil
}

func (s *acpSessionFeatureState) scanACPOutput(stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		s.lines <- line
	}
	s.readErrs <- scanner.Err()
}

func (s *acpSessionFeatureState) anACPClientCreatesASession() error {
	return s.createACPSession()
}

func (s *acpSessionFeatureState) createACPSession() error {
	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "session/new",
		"params":  map[string]interface{}{"cwd": s.root},
	}
	if err := writeJSONLine(s.stdin, request); err != nil {
		return err
	}
	wire, err := s.readMessages(2)
	if err != nil {
		return err
	}
	s.newWire = wire
	for _, msg := range wire {
		result, _ := msg["result"].(map[string]interface{})
		if id, _ := result["sessionId"].(string); id != "" {
			s.sessionID = id
		}
	}
	if s.sessionID == "" {
		return fmt.Errorf("session/new response missing in wire messages: %#v", wire)
	}
	return nil
}

func (s *acpSessionFeatureState) readMessages(count int) ([]map[string]interface{}, error) {
	messages := make([]map[string]interface{}, 0, count)
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	for len(messages) < count {
		select {
		case line := <-s.lines:
			var msg map[string]interface{}
			if err := json.Unmarshal(line, &msg); err != nil {
				return nil, fmt.Errorf("decode ACP output %q: %w", line, err)
			}
			messages = append(messages, msg)
		case err := <-s.readErrs:
			return nil, fmt.Errorf("ACP helper stopped early: %v; stderr: %s", err, s.stderr.String())
		case <-deadline.C:
			return nil, fmt.Errorf("timed out waiting for ACP output; stderr: %s", s.stderr.String())
		}
	}
	return messages, nil
}

func (s *acpSessionFeatureState) sessionNewResponsePrecedesUpdates() error {
	if len(s.newWire) < 2 {
		return fmt.Errorf("need response and update, got %#v", s.newWire)
	}
	if _, ok := s.newWire[0]["result"]; !ok {
		return fmt.Errorf("first ACP message is not session/new response: %#v", s.newWire[0])
	}
	if method, _ := s.newWire[1]["method"].(string); method != "session/update" {
		return fmt.Errorf("second ACP message is not session/update: %#v", s.newWire[1])
	}
	return nil
}

func (s *acpSessionFeatureState) availableSlashCommandsInclude(name string) error {
	for _, msg := range s.newWire {
		params, _ := msg["params"].(map[string]interface{})
		update, _ := params["update"].(map[string]interface{})
		if update["sessionUpdate"] != acp.UpdateTypeAvailableCommandsUpdate {
			continue
		}
		commands, _ := update["availableCommands"].([]interface{})
		for _, raw := range commands {
			command, _ := raw.(map[string]interface{})
			if command["name"] == name {
				return nil
			}
		}
	}
	return fmt.Errorf("slash command %q not advertised: %#v", name, s.newWire)
}

func (s *acpSessionFeatureState) clientSwitchesMode(mode string) error {
	request := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "session/set_mode",
		"params": map[string]interface{}{
			"sessionId": s.sessionID,
			"modeId":    mode,
		},
	}
	if err := writeJSONLine(s.stdin, request); err != nil {
		return err
	}
	wire, err := s.readMessages(3)
	if err != nil {
		return err
	}
	s.modeWire = wire
	return nil
}

func (s *acpSessionFeatureState) currentModeUpdateUses(field, mode string) error {
	update := findSessionUpdate(s.modeWire, acp.UpdateTypeCurrentModeUpdate)
	if update == nil {
		return fmt.Errorf("current mode update not found: %#v", s.modeWire)
	}
	if got, _ := update[field].(string); got != mode {
		return fmt.Errorf("%s = %q, want %q in %#v", field, got, mode, update)
	}
	return nil
}

func (s *acpSessionFeatureState) currentModeUpdateOmits(field string) error {
	update := findSessionUpdate(s.modeWire, acp.UpdateTypeCurrentModeUpdate)
	if update == nil {
		return fmt.Errorf("current mode update not found: %#v", s.modeWire)
	}
	if _, ok := update[field]; ok {
		return fmt.Errorf("current mode update contains deprecated %q: %#v", field, update)
	}
	return nil
}

func (s *acpSessionFeatureState) activeSessionWithoutMCP() error {
	cfg := testConfig()
	cfg.Paths.Home = filepath.Join(s.root, "home")
	mgr := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default(), s.root, nil)
	result, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: s.root})
	if err != nil {
		return err
	}
	s.mgr = mgr
	s.sessionID = result.SessionID
	s.activeState = mgr.SessionByID(result.SessionID)
	if s.activeState == nil {
		return fmt.Errorf("active session %q was not registered", result.SessionID)
	}
	if len(s.activeState.GetMCPClients()) != 0 {
		return fmt.Errorf("new session unexpectedly has MCP clients")
	}
	return nil
}

func (s *acpSessionFeatureState) settingsReloadedWithMCP(name string) error {
	next := testConfig()
	next.Paths.Home = filepath.Join(s.root, "home")
	next.MCPServers = []config.MCPServerConfig{testMCPServerConfig(name)}
	s.mgr.ReplaceConfig(next)
	return nil
}

func (s *acpSessionFeatureState) currentSessionExposesMCPTool(expected string) error {
	for _, client := range s.activeState.GetMCPClients() {
		for _, tool := range client.Tools() {
			if client.Name()+"__"+tool.Name == expected {
				return nil
			}
		}
	}
	return fmt.Errorf("MCP tool %q not attached to current session", expected)
}

func findSessionUpdate(wire []map[string]interface{}, kind string) map[string]interface{} {
	for _, msg := range wire {
		params, _ := msg["params"].(map[string]interface{})
		update, _ := params["update"].(map[string]interface{})
		if update["sessionUpdate"] == kind {
			return update
		}
	}
	return nil
}

func writeJSONLine(w io.Writer, value interface{}) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}

func testMCPServerConfig(name string) config.MCPServerConfig {
	return config.MCPServerConfig{
		Type:    "stdio",
		Name:    name,
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestMCPSettingsHelperProcess$"},
		Env:     []config.EnvVarConfig{{Name: testMCPHelperEnv, Value: "1"}},
	}
}

func initializeACPSessionScenario(sc *godog.ScenarioContext) {
	s := &acpSessionFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^Coddy ACP has loaded skill "([^"]+)"$`, s.coddyACPHasLoadedSkill)
	sc.Step(`^an ACP client creates a session$`, s.anACPClientCreatesASession)
	sc.Step(`^the session/new response is sent before its session updates$`, s.sessionNewResponsePrecedesUpdates)
	sc.Step(`^the available slash commands include "([^"]+)"$`, s.availableSlashCommandsInclude)
	sc.Step(`^an ACP client has an active session$`, s.anACPClientHasAnActiveSession)
	sc.Step(`^the client switches the mode to "([^"]+)"$`, s.clientSwitchesMode)
	sc.Step(`^the current mode update identifies "([^"]+)" with "([^"]+)"$`, func(mode, field string) error {
		return s.currentModeUpdateUses(field, mode)
	})
	sc.Step(`^the current mode update does not contain "([^"]+)"$`, s.currentModeUpdateOmits)
	sc.Step(`^an active session without configured MCP servers$`, s.activeSessionWithoutMCP)
	sc.Step(`^settings are reloaded with MCP server "([^"]+)"$`, s.settingsReloadedWithMCP)
	sc.Step(`^the current session exposes MCP tool "([^"]+)"$`, s.currentSessionExposesMCPTool)
}

func TestACPSessionIntegrationFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "acp-session-integration",
		ScenarioInitializer: initializeACPSessionScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/acp_session_integration.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("ACP session integration feature suite failed")
	}
}

func TestReplaceConfigKeepsSessionMCPAndRemovesConfiguredMCP(t *testing.T) {
	cfg := testConfig()
	mgr := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default(), t.TempDir(), nil)
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{
		CWD: t.TempDir(),
		MCPServers: []acp.MCPServer{{
			Type:    "stdio",
			Name:    "client-probe",
			Command: os.Args[0],
			Args:    []string{"-test.run=^TestMCPSettingsHelperProcess$"},
			Env:     []acp.EnvVariable{{Name: testMCPHelperEnv, Value: "1"}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	state := mgr.SessionByID(created.SessionID)
	t.Cleanup(state.CloseAll)
	assertMCPClientNames(t, state, "client-probe")

	withConfigured := testConfig()
	withConfigured.MCPServers = []config.MCPServerConfig{testMCPServerConfig("settings-probe")}
	mgr.ReplaceConfig(withConfigured)
	assertMCPClientNames(t, state, "settings-probe", "client-probe")

	mgr.ReplaceConfig(testConfig())
	assertMCPClientNames(t, state, "client-probe")
}

// TestReplaceConfigDefersMCPReloadWhileTurnHoldsLock proves a settings save that
// changes the configured MCP servers does not swap them under a running turn:
// the turn already handed the model a tool list, so replacing the clients
// mid-turn would make its next MCP call resolve to a server that no longer
// exists. The reload is parked and applied when the turn releases the lock.
func TestReplaceConfigDefersMCPReloadWhileTurnHoldsLock(t *testing.T) {
	cfg := testConfig()
	mgr := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default(), t.TempDir(), nil)
	created, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	state := mgr.SessionByID(created.SessionID)
	t.Cleanup(state.CloseAll)
	if got := len(state.GetMCPClients()); got != 0 {
		t.Fatalf("new session has %d MCP clients, want 0", got)
	}

	// A turn is in flight: it holds the exclusive per-session lock.
	unlock, err := mgr.AcquireComposerTurnLock(created.SessionID, state)
	if err != nil {
		t.Fatal(err)
	}

	// Saving settings that add a configured server must not attach it yet.
	withConfigured := testConfig()
	withConfigured.MCPServers = []config.MCPServerConfig{testMCPServerConfig("settings-probe")}
	mgr.ReplaceConfig(withConfigured)
	if got := len(state.GetMCPClients()); got != 0 {
		t.Fatalf("configured MCP server attached mid-turn: %d clients, want the reload parked until the turn ends", got)
	}

	// Releasing the lock drains the parked reload.
	unlock()
	assertMCPClientNames(t, state, "settings-probe")
}

func assertMCPClientNames(t *testing.T, state *session.State, expected ...string) {
	t.Helper()
	clients := state.GetMCPClients()
	if len(clients) != len(expected) {
		t.Fatalf("MCP client count = %d, want %d", len(clients), len(expected))
	}
	for i, name := range expected {
		if clients[i].Name() != name {
			t.Fatalf("MCP client %d = %q, want %q", i, clients[i].Name(), name)
		}
	}
}

func TestACPFeatureHelperProcess(t *testing.T) {
	if os.Getenv(testACPHelperEnv) != "1" {
		return
	}
	cfg := testConfig()
	cfg.Paths.Home = os.Getenv(testHomeEnv)
	if skillsDir := os.Getenv(testSkillsDirEnv); skillsDir != "" {
		cfg.Skills.Dirs = []string{skillsDir}
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	mgr := session.NewManager(cfg, nil, noopRunner, log, os.Getenv(testWorkspaceEnv), nil)
	server := acp.NewServer(mgr, log)
	mgr.SetServer(server)
	if err := server.Run(context.Background(), os.Stdin); err != nil {
		t.Fatal(err)
	}
}

func TestMCPSettingsHelperProcess(t *testing.T) {
	if os.Getenv(testMCPHelperEnv) != "1" {
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
		var result interface{}
		switch request["method"] {
		case "initialize":
			result = map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"serverInfo":      map[string]interface{}{"name": "settings-probe", "version": "1"},
			}
		case "tools/list":
			result = map[string]interface{}{
				"tools": []interface{}{map[string]interface{}{
					"name":        "probe",
					"description": "Reports that the settings MCP is connected.",
					"inputSchema": map[string]interface{}{"type": "object"},
				}},
			}
		default:
			result = map[string]interface{}{}
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
