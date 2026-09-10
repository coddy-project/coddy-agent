//go:build gateway || gateway.telegram

package telegram

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
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
	if !strings.HasPrefix(got, modelDigestMarker) {
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

// The mode keyboard shares the callback parser, so its payloads must stay
// inside the same limit and keep their action prefix.
func TestModeKeyboardPayloadsAreWellFormed(t *testing.T) {
	kb := buildModeKeyboard("agent")
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			data := *btn.CallbackData
			if len(data) > telegramCallbackDataMax {
				t.Fatalf("callback_data is %d bytes: %q", len(data), data)
			}
			action, payload, ok := strings.Cut(data, ":")
			if !ok || action != callbackActionMode || payload == "" {
				t.Fatalf("unexpected callback data %q", data)
			}
		}
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
