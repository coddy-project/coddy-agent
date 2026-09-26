//go:build gateway || gateway.telegram

package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
)

// An id that fills callback_data exactly travels as itself; one byte more and
// it has to become a digest, because a truncated id resolves to nothing.
func TestModelCallbackValueSwitchesToADigestAtTheLimit(t *testing.T) {
	prefix := len(callbackActionModel) + 1 // "model:"
	exact := strings.Repeat("m", telegramCallbackDataMax-prefix)
	if got := modelCallbackValue(exact); got != exact {
		t.Fatalf("id that fits was rewritten: %q", got)
	}
	over := exact + "m"
	got := modelCallbackValue(over)
	if got == over {
		t.Fatal("id over the limit travelled verbatim")
	}
	if !strings.HasPrefix(got, callbackDigestMarker) {
		t.Fatalf("digest form %q does not carry the marker", got)
	}
	if prefix+len(got) > telegramCallbackDataMax {
		t.Fatalf("digest payload is %d bytes with the prefix, over the limit", prefix+len(got))
	}
}

func TestResolveModelCallback(t *testing.T) {
	long := strings.Repeat("n", 80)
	otherLong := strings.Repeat("o", 80)
	models := []config.ModelEntry{
		{Model: "openai/gpt-4o"},
		{Model: long},
		{Model: otherLong},
	}

	cases := []struct {
		name    string
		payload string
		want    string
		wantOK  bool
	}{
		{"configured id", "openai/gpt-4o", "openai/gpt-4o", true},
		{"digest of a long id", modelCallbackValue(long), long, true},
		{"digest of another long id", modelCallbackValue(otherLong), otherLong, true},
		{"id that is not configured", "openai/gpt-4o-mini", "", false},
		// A truncated id is what the old keyboard sent; nothing must match it.
		{"truncated id", long[:57], "", false},
		{"digest of a model since removed", modelCallbackValue(strings.Repeat("z", 80)), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveModelCallback(models, tc.payload)
			if ok != tc.wantOK || got != tc.want {
				t.Fatalf("resolveModelCallback(%q) = (%q, %v), want (%q, %v)", tc.payload, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

// Every button the keyboard offers has to be accepted back, whatever the id
// length, and none may exceed what Telegram will carry.
func TestModelKeyboardButtonsRoundTrip(t *testing.T) {
	models := []config.ModelEntry{
		{Model: "openai/gpt-4o"},
		{Model: "neuraldeep/qwen3-235b-a22b-instruct-2507-fp8-extended-context-preview"},
	}
	kb := buildModelKeyboard(models, models[0].Model)

	seen := 0
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			if btn.CallbackData == nil {
				t.Fatal("button without callback data")
			}
			data := *btn.CallbackData
			if len(data) > telegramCallbackDataMax {
				t.Fatalf("callback_data is %d bytes: %q", len(data), data)
			}
			action, payload, ok := strings.Cut(data, ":")
			if !ok || action != callbackActionModel {
				t.Fatalf("unexpected callback data %q", data)
			}
			model, resolved := resolveModelCallback(models, payload)
			if !resolved {
				t.Fatalf("payload %q does not resolve back to a configured model", payload)
			}
			if want := strings.TrimPrefix(btn.Text, "✓ "); model != want {
				t.Fatalf("button %q carries model %q", btn.Text, model)
			}
			seen++
		}
	}
	if seen != len(models) {
		t.Fatalf("keyboard offered %d buttons, want %d", seen, len(models))
	}
}

// failingRunner is the stub runner with the failures a model tap can meet on
// the session side, switched on once the keyboard is in the chat.
type failingRunner struct {
	*stubRunner
	ensureErr error
	setErr    error
}

func (r *failingRunner) EnsureHTTPSession(ctx context.Context, sessionID, cwd string) (*session.State, error) {
	if r.ensureErr != nil {
		return nil, r.ensureErr
	}
	return r.stubRunner.EnsureHTTPSession(ctx, sessionID, cwd)
}

func (r *failingRunner) HandleSessionSetConfigOption(ctx context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	if r.setErr != nil {
		return nil, r.setErr
	}
	return r.stubRunner.HandleSessionSetConfigOption(ctx, params)
}

// A model tap that fails says why in the chat, as a reply to the keyboard it
// came from. The query already has its one answer, the acknowledgement sent
// as the tap arrives, and Telegram refuses a second: an alert would never be
// seen.
func TestModelTapFailuresReplyInTheChat(t *testing.T) {
	cases := []struct {
		name  string
		spoil func(r *failingRunner)
		want  string
	}{
		{"session cannot be loaded", func(r *failingRunner) { r.ensureErr = errors.New("bundle unreadable") },
			"❌ Session error: bundle unreadable"},
		{"model dropped from the configuration", func(r *failingRunner) { r.cfg.Models = r.cfg.Models[:1] },
			"❌ That model is no longer configured."},
		{"manager refuses the model", func(r *failingRunner) { r.setErr = errors.New("model is not available") },
			"❌ model is not available"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t, tgfake.Options{})
			runner := &failingRunner{stubRunner: newStubRunner(&config.Config{
				Models: []config.ModelEntry{{Model: "openai/gpt-4o"}, {Model: "rpa/qwen3.6-35b-a3b"}},
				Agent:  config.Agent{Model: "openai/gpt-4o"},
			})}
			bot := New(&config.TelegramGatewayConfig{DefaultAccess: config.AccessAll}, runner, t.TempDir(),
				slog.New(slog.DiscardHandler), "", nil)
			key := sessionstore.SessionKey(adapterName, modelSwitchChatID, modelSwitchUserID, config.IsolationIndividual, false)
			bot.processMessage(t.Context(), f.api, f.userMessage(modelSwitchChatID, modelSwitchUserID, "/model"), key)
			tc.spoil(runner)
			cbq, err := f.tap(modelSwitchChatID, modelSwitchUserID, "rpa/qwen3.6-35b-a3b")
			if err != nil {
				t.Fatal(err)
			}
			bot.handleCallback(t.Context(), f.api, cbq)

			replies := f.repliesTo(modelSwitchChatID, cbq.Message.MessageID)
			if len(replies) != 1 || replies[0].Text != tc.want {
				t.Fatalf("replies to the keyboard = %+v, want one saying %q:\n%s", replies, tc.want, f.fake.Chat(modelSwitchChatID).Text())
			}
			requireOneAnswer(t, f, cbq.ID)
		})
	}
}

// The adapter's logger has to arrive tagged, or logger.levels naming
// gateway.telegram scopes nothing and the debug trail stays invisible.
func TestBotLoggerCarriesTheTelegramComponent(t *testing.T) {
	var buf bytes.Buffer
	base := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	b := New(&config.TelegramGatewayConfig{}, nil, "",
		logger.Component(base, logger.ComponentGatewayTelegram), "", nil)

	b.log.Debug("probe")

	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("log line is not JSON: %v\n%s", err, buf.String())
	}
	if got := rec[logger.ComponentKey]; got != logger.ComponentGatewayTelegram {
		t.Fatalf("component = %v, want %q", got, logger.ComponentGatewayTelegram)
	}
}

// newOriginTestBot builds a Bot over the scripted runner, with a store of its
// own, for the origin checks below.
func newOriginTestBot(t *testing.T) (*Bot, *scriptedRunner) {
	t.Helper()
	runner := newScriptedRunner()
	base, _, err := logger.New(config.Logger{
		Level:   config.LogLevelError,
		Format:  config.LogFormatText,
		Outputs: []string{config.LogOutputStderr},
	})
	if err != nil {
		t.Fatal(err)
	}
	bot := New(&config.TelegramGatewayConfig{
		Enabled: true, Token: "t",
		DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual,
	}, runner, t.TempDir(), logger.Component(base, logger.ComponentGatewayTelegram), "", nil)
	return bot, runner
}

// A gateway names only the conversations it starts. A chat key the store has
// seen before belongs to a session that was stamped when it began, and an id
// this gateway did not mint is somebody else's conversation to name - an empty
// origin means "not recorded", not "opened on this host", so the write-once
// guard in SetOrigin cannot tell those apart on its own.
func TestEnsureSessionStampsOnlyTheChatsItStarts(t *testing.T) {
	bot, _ := newOriginTestBot(t)

	first, err := bot.ensureSession(t.Context(), "chat:1")
	if err != nil {
		t.Fatal(err)
	}
	if got := first.GetOrigin(); got != session.GatewayOrigin("telegram") {
		t.Fatalf("a chat the gateway started has origin %q", got)
	}

	// A session that already exists under a key the gateway did not mint keeps
	// whatever it was: reaching it again must not relabel it.
	adopted := bot.store.Get("chat:2")
	local, err := bot.runner.EnsureHTTPSession(t.Context(), adopted, bot.cwd)
	if err != nil {
		t.Fatal(err)
	}
	if got := local.GetOrigin(); got != "" {
		t.Fatalf("a session created outside the gateway starts with origin %q", got)
	}
	again, err := bot.ensureSession(t.Context(), "chat:2")
	if err != nil {
		t.Fatal(err)
	}
	if got := again.GetOrigin(); got != "" {
		t.Fatalf("the gateway relabelled a session it did not start: origin %q", got)
	}
}
