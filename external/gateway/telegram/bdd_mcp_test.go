//go:build gateway || gateway.telegram

package telegram

// Godog harness for features/gateway_telegram_mcp.feature: drives /mcp and the
// tap on its keyboard through the real handlers against the fake Bot API
// (internal/tgfake), over a home and a workspace of the scenario's own. The
// global server is this test binary re-executed as a small MCP server
// (TestHelperTelegramMCPServer), so the menu shows one connected with its
// tools. No LLM and no network beyond the local httptest server.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
)

const (
	mcpFeatureChatID = int64(5252)
	mcpFeatureUserID = int64(5252)

	// mcpHelperEnv turns this test binary into the MCP server of
	// TestHelperTelegramMCPServer; mcpHelperToolsEnv lists the tools it offers.
	mcpHelperEnv      = "CODDY_TG_MCP_HELPER"
	mcpHelperToolsEnv = "CODDY_TG_MCP_HELPER_TOOLS"
)

// TestHelperTelegramMCPServer is not a test: re-executed with
// CODDY_TG_MCP_HELPER=1, the test binary becomes a minimal MCP server on
// stdio, speaking newline-delimited JSON-RPC and offering the tools
// CODDY_TG_MCP_HELPER_TOOLS names. It exits when its stdin closes, which is
// how a probe ends it.
func TestHelperTelegramMCPServer(t *testing.T) {
	if os.Getenv(mcpHelperEnv) != "1" {
		t.Skip("helper process of the /mcp feature")
	}
	tools := []map[string]any{}
	for _, name := range strings.Split(os.Getenv(mcpHelperToolsEnv), ",") {
		if name = strings.TrimSpace(name); name != "" {
			tools = append(tools, map[string]any{"name": name, "inputSchema": map[string]any{"type": "object"}})
		}
	}
	in := bufio.NewReader(os.Stdin)
	for {
		line, err := in.ReadBytes('\n')
		if err != nil {
			os.Exit(0)
		}
		var req struct {
			ID     any    `json:"id"`
			Method string `json:"method"`
		}
		if json.Unmarshal(line, &req) != nil || req.ID == nil {
			continue // a notification needs no answer
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": "telegram-mcp-helper", "version": "0.0.1"},
			}
		case "tools/list":
			result = map[string]any{"tools": tools}
		}
		out, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
		_, _ = os.Stdout.Write(append(out, '\n'))
	}
}

// mcpWorld is one scenario: a bot over a home and a workspace of its own, the
// fake Bot API in front, and the menu message the bot sent.
type mcpWorld struct {
	root   string
	home   string
	cwd    string
	runner *mcpRunner
	f      *fakeAPI
	bot    *Bot
	menuID int
}

func (w *mcpWorld) gatewayOverAWorkspace() error {
	root, err := os.MkdirTemp("", "coddy-tg-mcp-")
	if err != nil {
		return err
	}
	w.root = root
	w.home, w.cwd = filepath.Join(root, "home"), filepath.Join(root, "work")
	for _, dir := range []string{w.home, w.cwd} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	cfg := &config.Config{}
	cfg.Paths.Home = w.home
	w.runner = newMCPRunner(cfg)
	w.f = openFakeAPI(tgfake.Options{})
	base, _, err := logger.New(config.Logger{Level: config.LogLevelError, Format: config.LogFormatText, Outputs: []string{config.LogOutputStderr}})
	if err != nil {
		return err
	}
	w.bot = New(&config.TelegramGatewayConfig{
		Enabled: true, Token: "t", DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual,
	}, w.runner, w.cwd, logger.Component(base, logger.ComponentGatewayTelegram), "", nil)
	return nil
}

// globalServerOffering declares a server in <home>/mcp.json that runs this
// test binary as the helper MCP server, offering the two tools named.
func (w *mcpWorld) globalServerOffering(name, first, second string) error {
	return config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(w.home), name, config.MCPJSONServer{
		Command: os.Args[0],
		Args:    []string{"-test.run=^TestHelperTelegramMCPServer$"},
		Env:     map[string]string{mcpHelperEnv: "1", mcpHelperToolsEnv: first + "," + second},
	})
}

// projectServerNobodyApproved declares a server in the workspace's
// .coddy/mcp.json. It is never started: nobody approved it.
func (w *mcpWorld) projectServerNobodyApproved(name string) error {
	return config.UpsertMCPJSONServer(config.MCPJSONPath(w.cwd), name, config.MCPJSONServer{Command: "untrusted-command"})
}

func (w *mcpWorld) globalServerSwitchedOff(name string) error {
	return config.SetMCPJSONServerDisabled(config.GlobalMCPJSONPath(w.home), name, true)
}

// userSends types text into the chat; the bot's answer to it becomes the menu
// the next steps read.
func (w *mcpWorld) userSends(text string) error {
	msg := w.f.userMessage(mcpFeatureChatID, mcpFeatureUserID, text)
	key := sessionstore.SessionKey(adapterName, mcpFeatureChatID, mcpFeatureUserID, config.IsolationIndividual, false)
	w.bot.processMessage(context.Background(), w.f.api, msg, key)
	replies := w.f.repliesTo(mcpFeatureChatID, msg.MessageID)
	if len(replies) != 1 {
		return fmt.Errorf("%q was answered with %d messages, want 1:\n%s", text, len(replies), w.f.fake.Chat(mcpFeatureChatID).Text())
	}
	w.menuID = replies[0].MessageID
	return nil
}

func (w *mcpWorld) sentTheMenu() error { return w.userSends("/mcp") }

// userTaps presses the button a person sees under that label, on the message
// that really carries it.
func (w *mcpWorld) userTaps(label string) error {
	cbq, err := w.f.tap(mcpFeatureChatID, mcpFeatureUserID, label)
	if err != nil {
		return err
	}
	w.bot.handleCallback(context.Background(), w.f.api, cbq)
	return nil
}

func (w *mcpWorld) menu() (tgfake.MessageView, error) {
	msg, ok := w.f.message(mcpFeatureChatID, w.menuID)
	if !ok {
		return msg, fmt.Errorf("no menu message %d in the chat:\n%s", w.menuID, w.f.fake.Chat(mcpFeatureChatID).Text())
	}
	return msg, nil
}

func (w *mcpWorld) menuLists(line string) error {
	msg, err := w.menu()
	if err != nil {
		return err
	}
	if !slices.Contains(strings.Split(msg.Text, "\n"), line) {
		return fmt.Errorf("the menu does not list %q:\n%s", line, msg.Text)
	}
	return nil
}

// buttons returns the labels of the menu's keyboard.
func (w *mcpWorld) buttons() ([]string, error) {
	msg, err := w.menu()
	if err != nil {
		return nil, err
	}
	var labels []string
	for _, row := range msg.Keyboard {
		for _, b := range row {
			labels = append(labels, b.Text)
		}
	}
	return labels, nil
}

func (w *mcpWorld) menuOffersButton(label string) error {
	labels, err := w.buttons()
	if err != nil {
		return err
	}
	if !slices.Contains(labels, label) {
		return fmt.Errorf("the menu offers %q, not %q", labels, label)
	}
	return nil
}

func (w *mcpWorld) menuOffersNoButtonFor(name string) error {
	labels, err := w.buttons()
	if err != nil {
		return err
	}
	for _, label := range labels {
		if strings.HasSuffix(label, " "+name) {
			return fmt.Errorf("the menu offers %q for a server nobody approved", label)
		}
	}
	return nil
}

func (w *mcpWorld) globalFileRecords(name, state string) error {
	servers, err := config.ReadMCPJSONFile(config.GlobalMCPJSONPath(w.home))
	if err != nil {
		return err
	}
	srv, ok := servers[name]
	if !ok {
		return fmt.Errorf("the global mcp.json has no %q", name)
	}
	if srv.Disabled != (state == "disabled") {
		return fmt.Errorf("the global mcp.json records %q with disabled=%v, want it %s", name, srv.Disabled, state)
	}
	return nil
}

func (w *mcpWorld) liveSessionsToldToRefresh(name string) error {
	if got := w.runner.refreshedNames(); !slices.Equal(got, []string{name}) {
		return fmt.Errorf("the live sessions were told to refresh %q, want only %q", got, name)
	}
	return nil
}

func initializeMCPScenario(sc *godog.ScenarioContext) {
	w := &mcpWorld{}
	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if w.f != nil {
			w.f.close()
		}
		if w.root != "" {
			_ = os.RemoveAll(w.root)
		}
		return ctx, err
	})

	sc.Given(`^a telegram gateway over a workspace$`, w.gatewayOverAWorkspace)
	sc.Given(`^a global MCP server "([^"]*)" offering the tools "([^"]*)" and "([^"]*)"$`, w.globalServerOffering)
	sc.Given(`^a project MCP server "([^"]*)" that nobody approved$`, w.projectServerNobodyApproved)
	sc.Given(`^the global MCP server "([^"]*)" is switched off$`, w.globalServerSwitchedOff)
	sc.Given(`^the user has been sent the MCP menu$`, w.sentTheMenu)

	sc.When(`^the user sends "([^"]*)"$`, w.userSends)
	sc.When(`^the user taps "([^"]*)"$`, w.userTaps)

	sc.Then(`^the menu lists "([^"]*)"$`, w.menuLists)
	sc.Then(`^the menu offers the button "([^"]*)"$`, w.menuOffersButton)
	sc.Then(`^the menu offers no button for "([^"]*)"$`, w.menuOffersNoButtonFor)
	sc.Then(`^the global mcp.json records "([^"]*)" as (disabled|enabled)$`, w.globalFileRecords)
	sc.Then(`^the live sessions are told to refresh "([^"]*)"$`, w.liveSessionsToldToRefresh)
}

func TestMCPFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "gateway-telegram-mcp",
		ScenarioInitializer: initializeMCPScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../../features/gateway_telegram_mcp.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("telegram MCP feature suite failed")
	}
}
