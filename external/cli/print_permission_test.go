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

// A one-shot run is a task of its own: --permission-mode sets the mode of
// the run's session and leaves the mode new sessions start in alone.
func TestPrintPermissionModeStaysWithItsSession(t *testing.T) {
	home := t.TempDir()
	cfg := &config.Config{
		Paths:  config.Paths{Home: home, CWD: home},
		Models: []config.ModelEntry{{Model: "stub/model", MaxTokens: 100, MaxContextTokens: 1000}},
		Agent:  config.Agent{Model: "stub/model"},
	}
	ran := ""
	run := func(_ context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		ran = st.GetPermissionMode()
		return string(acp.StopReasonEndTurn), nil
	}
	var out, errOut bytes.Buffer
	mgr := session.NewManager(cfg, &printSender{out: &out, errOut: &errOut}, run, slog.New(slog.DiscardHandler), home, &session.FileStore{Root: filepath.Join(home, "sessions")})
	if err := PrintPrompt(context.Background(), mgr, PrintOptions{Prompt: "hello", PermMode: config.PermModeBypass, Out: &out, ErrOut: &errOut, Config: cfg}); err != nil {
		t.Fatal(err)
	}
	if ran != config.PermModeBypass {
		t.Fatalf("the run's session ran in %q, want bypass", ran)
	}
	if got := config.ReadDefaultPermissionMode(home); got != "" {
		t.Fatalf("a one-shot run moved the mode new sessions start in to %q", got)
	}
}

// The interactive console's --permission-mode is the same launch option: the
// session the console opens takes it, the mode new sessions start in does not.
func TestConsolePermissionModeFlagStaysWithItsSession(t *testing.T) {
	app := newReasoningApp(t)
	if err := app.ApplyStartupOptions(context.Background(), "", "", config.PermModeBypass); err != nil {
		t.Fatal(err)
	}
	if got := app.mgr.SessionByID(app.sessionID).GetPermissionMode(); got != config.PermModeBypass {
		t.Fatalf("the console's session runs in %q, want bypass", got)
	}
	if got := config.ReadDefaultPermissionMode(app.config().Paths.Home); got != "" {
		t.Fatalf("the launch flag moved the mode new sessions start in to %q", got)
	}
}
