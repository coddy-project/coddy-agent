//go:build gateway || gateway.telegram

package telegram

import (
	"context"
	"log/slog"
	"strings"
	"testing"

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
