//go:build gateway || gateway.telegram

package telegram

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/mcp"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
)

func TestMCPMenuTogglesTrustedServerThroughFakeBotAPI(t *testing.T) {
	ctx := context.Background()
	home, cwd := t.TempDir(), t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Home = home
	if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(home), "demo", config.MCPJSONServer{Command: "missing-mcp", Disabled: true}); err != nil {
		t.Fatal(err)
	}
	f := newFakeAPI(t, tgfake.Options{})
	bot := New(&config.TelegramGatewayConfig{DefaultAccess: config.AccessAll}, newStubRunner(cfg), cwd, slog.New(slog.DiscardHandler), "", nil)
	msg := f.userMessage(101, 202, "/mcp")
	bot.processMessage(ctx, f.api, msg, sessionstore.SessionKey(adapterName, 101, 202, config.IsolationIndividual, false))
	if !strings.Contains(f.fake.Chat(101).Text(), "demo · disabled") {
		t.Fatal("MCP status missing from Telegram chat")
	}
	tap, err := f.tap(101, 202, "Enable demo")
	if err != nil {
		t.Fatal(err)
	}
	bot.handleCallback(ctx, f.api, tap)
	rows, err := mcp.ListManagedServers(cfg, cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Config.Disabled {
		t.Fatalf("server still disabled: %+v", rows)
	}
	if !strings.Contains(f.fake.Chat(101).Text(), "demo · error") {
		t.Fatal("Telegram menu did not refresh after toggle")
	}
}

func TestMCPMenuDoesNotOfferTrustForProjectServer(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	cfg := &config.Config{}
	cfg.Paths.Home = home
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(cwd), "checkout", config.MCPJSONServer{Command: "untrusted-command"}); err != nil {
		t.Fatal(err)
	}
	f := newFakeAPI(t, tgfake.Options{})
	bot := New(&config.TelegramGatewayConfig{DefaultAccess: config.AccessAll}, newStubRunner(cfg), cwd, slog.New(slog.DiscardHandler), "", nil)
	msg := f.userMessage(101, 202, "/mcp")
	bot.processMessage(context.Background(), f.api, msg, "")
	if !strings.Contains(f.fake.Chat(101).Text(), "checkout · needs_approval") {
		t.Fatal("untrusted server not reported")
	}
	if _, err := f.tap(101, 202, "Enable checkout"); err == nil {
		t.Fatal("untrusted server has an enable button")
	}
}

// A menu with no buttons goes out with no reply_markup at all. An empty
// keyboard reaches Telegram as {"inline_keyboard":null}, which it refuses, and
// the chat would get no answer to /mcp: with no server configured, or with
// only a project server nobody approved for the workspace.
func TestMCPMenuWithoutButtonsSendsNoKeyboard(t *testing.T) {
	cases := []struct {
		name  string
		setup func(m *mcpTest)
		want  string
	}{
		{"no server configured", func(*mcpTest) {}, "No MCP servers configured."},
		{"only an unapproved project server", func(m *mcpTest) {
			m.projectServer("checkout", config.MCPJSONServer{Command: "untrusted-command"})
		}, "MCP servers:\ncheckout · needs_approval · 0 tools"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMCPTest(t)
			tc.setup(m)
			menu := m.sendMCP()
			if menu.Text != tc.want || menu.Keyboard != nil {
				t.Fatalf("menu = %q with keyboard %v, want %q without one", menu.Text, menu.Keyboard, tc.want)
			}
			for _, call := range m.f.fake.Calls("sendMessage") {
				if markup, ok := call.Params["reply_markup"]; ok {
					t.Fatalf("the menu was sent with reply_markup %s", markup)
				}
			}
		})
	}
}

// A trust verdict takes the status slot of a menu line, so a project server
// that is also switched off says "off" next to it; otherwise the operator could
// not tell from the chat that approving it would still leave it off. A server
// whose status already reads "disabled", and one that is on, read as before.
func TestMCPMenuLineShowsAServerSwitchedOffBehindATrustVerdict(t *testing.T) {
	cases := []struct {
		name   string
		policy string
		setup  func(m *mcpTest)
		want   string
	}{
		{"unapproved and off", "", func(m *mcpTest) {
			m.projectServer("checkout", config.MCPJSONServer{Command: "untrusted-command", Disabled: true})
		}, "checkout · needs_approval · off · 0 tools"},
		{"denied and off", config.ProjectTrustDeny, func(m *mcpTest) {
			m.projectServer("checkout", config.MCPJSONServer{Command: "untrusted-command", Disabled: true})
		}, "checkout · denied · off · 0 tools"},
		{"unapproved and on", "", func(m *mcpTest) {
			m.projectServer("checkout", config.MCPJSONServer{Command: "untrusted-command"})
		}, "checkout · needs_approval · 0 tools"},
		{"trusted and off", "", func(m *mcpTest) {
			m.globalServer("demo", config.MCPJSONServer{Command: "missing-mcp", Disabled: true})
		}, "demo · disabled · 0 tools"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMCPTest(t)
			m.cfg.MCP.ProjectTrust = tc.policy
			tc.setup(m)
			lines := strings.Split(m.sendMCP().Text, "\n")
			if len(lines) != 2 || lines[1] != tc.want {
				t.Fatalf("menu lines = %q, want the server line %q", lines, tc.want)
			}
		})
	}
}

// A switch that lands is handed to the live sessions by the name of the server
// it changed, so the manager reconciles that one server and leaves the other
// clients alone.
func TestMCPTapRefreshesTheServerItSwitched(t *testing.T) {
	m := newMCPTest(t)
	m.globalServer("demo", config.MCPJSONServer{Command: "missing-mcp", Disabled: true})
	m.globalServer("other", config.MCPJSONServer{Command: "missing-mcp", Disabled: true})
	m.sendMCP()
	m.tap("Enable demo")
	if got := m.runner.refreshedNames(); len(got) != 1 || got[0] != "demo" {
		t.Fatalf("live sessions were asked to refresh %q, want only demo", got)
	}
}

// A tap the menu cannot carry out says why on the first line of the menu
// message, above the menu as it is now. The query keeps its one answer, the
// acknowledgement sent as the tap arrives: Telegram refuses a second answer,
// so an alert would never reach the person. When the menu cannot be drawn
// either, the failure stands alone and the old buttons go with it, because
// they describe the servers as they were.
func TestMCPTapFailuresEditTheMenu(t *testing.T) {
	cases := []struct {
		name string
		// arrange declares the servers the menu is sent over; spoil changes
		// the world between the menu and the tap.
		arrange, spoil func(m *mcpTest)
		tap            string
		// wantFailure starts the first line; wantMenu follows it after a
		// blank line, and is empty when the failure stands alone.
		wantFailure, wantMenu string
		// wantButton is a button the edited menu still offers; empty means
		// the message is left without a keyboard.
		wantButton string
	}{
		{
			name: "server no longer approved",
			arrange: func(m *mcpTest) {
				m.projectServer("checkout", config.MCPJSONServer{Command: "untrusted-command", Disabled: true})
				m.approve("checkout")
			},
			spoil: func(m *mcpTest) {
				if _, err := mcp.NewTrustGate(m.cfg).Revoke(m.cwd, "checkout"); err != nil {
					m.t.Fatal(err)
				}
			},
			tap:         "Enable checkout",
			wantFailure: "❌ This server needs approval outside Telegram.",
			wantMenu:    "MCP servers:\ncheckout · needs_approval · off · 0 tools",
		},
		{
			name: "server removed since the menu was sent",
			arrange: func(m *mcpTest) {
				m.globalServer("demo", config.MCPJSONServer{Command: "missing-mcp", Disabled: true})
			},
			spoil: func(m *mcpTest) {
				if _, err := config.DeleteMCPJSONServer(config.GlobalMCPJSONPath(m.home), "demo"); err != nil {
					m.t.Fatal(err)
				}
			},
			tap:         "Enable demo",
			wantFailure: "❌ MCP server no longer exists.",
			wantMenu:    "No MCP servers configured.",
		},
		{
			name: "switch cannot be saved",
			arrange: func(m *mcpTest) {
				m.projectServer("checkout", config.MCPJSONServer{Command: "untrusted-command", Disabled: true})
				m.approve("checkout")
			},
			spoil: func(m *mcpTest) {
				// A project switch is written through a temporary file next
				// to the overrides; a directory in its place fails the write
				// and nothing else.
				if err := os.Mkdir(filepath.Join(m.home, "mcp-overrides.json.tmp"), 0o700); err != nil {
					m.t.Fatal(err)
				}
			},
			tap:         "Enable checkout",
			wantFailure: "❌ MCP: ",
			wantMenu:    "MCP servers:\ncheckout · disabled · 0 tools",
			wantButton:  "Enable checkout",
		},
		{
			name: "server list cannot be read",
			arrange: func(m *mcpTest) {
				m.globalServer("demo", config.MCPJSONServer{Command: "missing-mcp", Disabled: true})
			},
			spoil: func(m *mcpTest) {
				if err := os.WriteFile(filepath.Join(m.home, "mcp-overrides.json"), []byte("{"), 0o600); err != nil {
					m.t.Fatal(err)
				}
			},
			tap:         "Enable demo",
			wantFailure: "❌ MCP: parse MCP overrides",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newMCPTest(t)
			tc.arrange(m)
			menu := m.sendMCP()
			tc.spoil(m)
			cbq := m.tap(tc.tap)

			got := m.menu(menu.MessageID)
			failure, rest, _ := strings.Cut(got.Text, "\n\n")
			if !strings.HasPrefix(failure, tc.wantFailure) || strings.Contains(failure, "\n") || rest != tc.wantMenu {
				t.Fatalf("menu after the tap = %q, want the failure %q, then %q", got.Text, tc.wantFailure, tc.wantMenu)
			}
			switch {
			case tc.wantButton == "" && got.Keyboard != nil:
				t.Fatalf("the menu kept a keyboard: %v", got.Keyboard)
			case tc.wantButton != "":
				if _, _, ok := m.f.fake.Chat(mcpTestChatID).FindButton(tc.wantButton); !ok {
					t.Fatalf("the menu lost its button %q: %v", tc.wantButton, got.Keyboard)
				}
			}
			requireOneAnswer(t, m.f, cbq.ID)
		})
	}
}

// A switch that lands but whose menu cannot be drawn again leaves no button
// behind: the old keyboard would still offer "Enable demo", and a tap on it now
// would switch the server back off. The message says what failed instead.
func TestMCPTapThatLandsButCannotRedrawDropsTheStaleButtons(t *testing.T) {
	m := newMCPTest(t)
	m.globalServer("demo", config.MCPJSONServer{Command: "missing-mcp", Disabled: true})
	menu := m.sendMCP()
	// The server list breaks between the switch and the redraw, at the moment
	// the live sessions are told about the change.
	m.runner.onRefresh = func(string) {
		path := config.MCPJSONPath(m.cwd)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Error(err)
		}
		if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
			t.Error(err)
		}
	}
	cbq := m.tap("Enable demo")

	servers, err := config.ReadMCPJSONFile(config.GlobalMCPJSONPath(m.home))
	if err != nil || servers["demo"].Disabled {
		t.Fatalf("the switch did not land: %+v, %v", servers, err)
	}
	got := m.menu(menu.MessageID)
	if !strings.HasPrefix(got.Text, "❌ MCP: ") || strings.Contains(got.Text, "\n") || got.Keyboard != nil {
		t.Fatalf("menu after a failed redraw = %q with keyboard %v, want the failure alone and no buttons", got.Text, got.Keyboard)
	}
	requireOneAnswer(t, m.f, cbq.ID)
}

// mcpRunner is the session side of the /mcp tests: the stub runner of the
// model switch spec, plus the hook the menu calls after a switch so that live
// sessions reconcile the server that changed. It records the names it is
// given, and onRefresh lets a test act at that moment.
type mcpRunner struct {
	*stubRunner

	refreshMu sync.Mutex
	refreshed []string
	onRefresh func(name string)
}

func newMCPRunner(cfg *config.Config) *mcpRunner {
	return &mcpRunner{stubRunner: newStubRunner(cfg)}
}

func (r *mcpRunner) RefreshMCPServer(_ context.Context, name string) {
	r.refreshMu.Lock()
	r.refreshed = append(r.refreshed, name)
	hook := r.onRefresh
	r.refreshMu.Unlock()
	if hook != nil {
		hook(name)
	}
}

func (r *mcpRunner) refreshedNames() []string {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()
	return append([]string(nil), r.refreshed...)
}

const (
	mcpTestChatID = int64(101)
	mcpTestUserID = int64(202)
)

// mcpTest is a bot over a home and a workspace of its own, with the fake Bot
// API in front of it.
type mcpTest struct {
	t      *testing.T
	home   string
	cwd    string
	cfg    *config.Config
	runner *mcpRunner
	f      *fakeAPI
	bot    *Bot
}

func newMCPTest(t *testing.T) *mcpTest {
	t.Helper()
	m := &mcpTest{t: t, home: t.TempDir(), cwd: t.TempDir(), cfg: &config.Config{}}
	m.cfg.Paths.Home = m.home
	m.runner = newMCPRunner(m.cfg)
	m.f = newFakeAPI(t, tgfake.Options{})
	m.bot = New(&config.TelegramGatewayConfig{DefaultAccess: config.AccessAll}, m.runner, m.cwd, slog.New(slog.DiscardHandler), "", nil)
	return m
}

// globalServer declares a server in <home>/mcp.json, which needs no approval.
func (m *mcpTest) globalServer(name string, srv config.MCPJSONServer) {
	m.t.Helper()
	if err := config.UpsertMCPJSONServer(config.GlobalMCPJSONPath(m.home), name, srv); err != nil {
		m.t.Fatal(err)
	}
}

// projectServer declares a server in the workspace's .coddy/mcp.json, which
// runs only once the operator approved it for the workspace.
func (m *mcpTest) projectServer(name string, srv config.MCPJSONServer) {
	m.t.Helper()
	if err := config.UpsertMCPJSONServer(config.MCPJSONPath(m.cwd), name, srv); err != nil {
		m.t.Fatal(err)
	}
}

// approve records the operator's approval of a project server, as the console
// or the web UI does after showing the declaration.
func (m *mcpTest) approve(name string) {
	m.t.Helper()
	rows, err := mcp.ListManagedServers(m.cfg, m.cwd)
	if err != nil {
		m.t.Fatal(err)
	}
	for _, row := range rows {
		if row.Config.Name == name {
			if err := mcp.NewTrustGate(m.cfg).Approve(m.cwd, row); err != nil {
				m.t.Fatal(err)
			}
			return
		}
	}
	m.t.Fatalf("no MCP server %q to approve", name)
}

// sendMCP sends /mcp and returns the menu the bot answered with.
func (m *mcpTest) sendMCP() tgfake.MessageView {
	m.t.Helper()
	msg := m.f.userMessage(mcpTestChatID, mcpTestUserID, "/mcp")
	m.bot.processMessage(context.Background(), m.f.api, msg, "")
	replies := m.f.repliesTo(mcpTestChatID, msg.MessageID)
	if len(replies) != 1 {
		m.t.Fatalf("/mcp was answered with %d messages, want 1:\n%s", len(replies), m.f.fake.Chat(mcpTestChatID).Text())
	}
	return replies[0]
}

// tap presses a button of the menu and returns the query it sent.
func (m *mcpTest) tap(label string) *tgbotapi.CallbackQuery {
	m.t.Helper()
	cbq, err := m.f.tap(mcpTestChatID, mcpTestUserID, label)
	if err != nil {
		m.t.Fatal(err)
	}
	m.bot.handleCallback(context.Background(), m.f.api, cbq)
	return cbq
}

// menu returns the menu message as the chat shows it now.
func (m *mcpTest) menu(id int) tgfake.MessageView {
	m.t.Helper()
	msg, ok := m.f.message(mcpTestChatID, id)
	if !ok {
		m.t.Fatalf("no message %d in the chat", id)
	}
	return msg
}
