//go:build cli

package cli

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A turn from the console carries the terminal's language only when the
// documentation is written in it: a German terminal leaves the turn to the
// language of the prompt, so a Cyrillic prompt reads Russian, instead of
// clamping every turn to English.
func TestConsoleTurnLanguageFallsBackToThePrompt(t *testing.T) {
	for _, tc := range []struct {
		locale, prompt, want string
	}{
		{"de_DE.UTF-8", "объясни режимы разрешений", "ru"},
		{"de_DE.UTF-8", "explain the permission modes", "en"},
		{"ru_RU.UTF-8", "explain the permission modes", "ru"},
		{"en_US.UTF-8", "объясни режимы разрешений", "en"},
	} {
		t.Setenv("CODDY_LANG", "")
		t.Setenv("LC_ALL", "")
		t.Setenv("LC_MESSAGES", "")
		t.Setenv("LANG", tc.locale)
		home := t.TempDir()
		cfg := &config.Config{
			Paths:  config.Paths{Home: home, CWD: home},
			Models: []config.ModelEntry{{Model: "stub/model", MaxTokens: 100, MaxContextTokens: 1000}},
			Agent:  config.Agent{Model: "stub/model"},
		}
		seen := ""
		run := func(_ context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
			seen = st.GetTurnLang()
			return string(acp.StopReasonEndTurn), nil
		}
		mgr := session.NewManager(cfg, nil, run, slog.New(slog.DiscardHandler), home, &session.FileStore{Root: filepath.Join(home, "sessions")})
		var out, errOut bytes.Buffer
		if err := PrintPrompt(context.Background(), mgr, PrintOptions{Prompt: tc.prompt, Out: &out, ErrOut: &errOut, Config: cfg}); err != nil {
			t.Fatalf("%s %q: %v", tc.locale, tc.prompt, err)
		}
		if seen != tc.want {
			t.Errorf("LANG=%s, prompt %q: turn language %q, want %q", tc.locale, tc.prompt, seen, tc.want)
		}
	}
}
