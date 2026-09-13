//go:build cli

package cli

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func newReasoningApp(t *testing.T) *App {
	t.Helper()
	home := t.TempDir()
	cfg := &config.Config{
		Paths: config.Paths{Home: home, CWD: home},
		Providers: []config.ProviderConfig{{
			Name: "stub", Type: "openai", APIBase: "http://127.0.0.1:0", APIKey: "test",
		}},
		Models: []config.ModelEntry{{
			Model: "stub/gpt-5.6-terra", MaxTokens: 1000, MaxContextTokens: 100000, ReasoningDefault: "medium",
		}},
		Agent: config.Agent{Model: "stub/gpt-5.6-terra"},
	}
	store := &session.FileStore{Root: filepath.Join(home, "sessions")}
	term := &bddTerminal{cols: 100, rows: 35}
	late := &lateBoundSender{}
	mgr := session.NewManager(cfg, late, nil, slog.New(slog.DiscardHandler), home, store)
	app := newApp(cfg, mgr, slog.New(slog.DiscardHandler), term, "dark", true)
	late.inner = app.Sender()
	if err := app.Start(context.Background(), "", false); err != nil {
		t.Fatalf("start app: %v", err)
	}
	return app
}

func reasoningOption(t *testing.T, a *App) acp.ConfigOption {
	t.Helper()
	for _, opt := range a.configOpts {
		if opt.ID == "reasoning" {
			return opt
		}
	}
	t.Fatal("reasoning config option missing")
	return acp.ConfigOption{}
}

func waitForReasoning(t *testing.T, a *App, level string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case msg := <-a.updatesCh:
			a.applyLoopMessage(msg)
		default:
		}
		if st := a.mgr.SessionByID(a.sessionID); st != nil && st.GetSelectedReasoning() == level && reasoningOption(t, a).CurrentValue == level {
			return
		}
		time.Sleep(time.Millisecond)
	}
	st := a.mgr.SessionByID(a.sessionID)
	got := ""
	if st != nil {
		got = st.GetSelectedReasoning()
	}
	t.Fatalf("selected reasoning = %q, want %q", got, level)
}

func TestReasoningSelectorPersistsAndRefreshesFooter(t *testing.T) {
	a := newReasoningApp(t)
	a.openReasoningSelector()

	sel, ok := a.modal.(*selectorModal)
	if !ok {
		t.Fatalf("modal = %T, want reasoning selector", a.modal)
	}
	if sel.title != "Select reasoning" {
		t.Fatalf("selector title = %q", sel.title)
	}
	if got := sel.list.SelectedItem().Value; got != "medium" {
		t.Fatalf("preselected level = %q, want medium", got)
	}

	a.setReasoning("high")
	waitForReasoning(t, a, "high")
	if got := strings.Join(a.foot.Render(100), "\n"); !strings.Contains(got, "high") {
		t.Fatalf("footer %q does not show selected reasoning", got)
	}
}

func TestReasoningSlashReportsAvailableLevelsAndPreservesInvalidSelection(t *testing.T) {
	a := newTestApp(t)
	if !a.dispatchSlash("/reasoning") {
		t.Fatal("/reasoning was not handled")
	}
	if got := transcriptText(a); !strings.Contains(got, "No reasoning levels") {
		t.Fatalf("status %q does not explain absent reasoning levels", got)
	}

	a = newReasoningApp(t)
	if !a.dispatchSlash("/reasoning ultra") {
		t.Fatal("/reasoning ultra was not handled")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case msg := <-a.updatesCh:
			a.applyLoopMessage(msg)
		default:
		}
		if strings.Contains(transcriptText(a), "Valid levels:") {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if got := a.mgr.SessionByID(a.sessionID).GetSelectedReasoning(); got != "" {
		t.Fatalf("invalid reasoning changed state to %q", got)
	}
	status := transcriptText(a)
	if !strings.Contains(status, "reasoning:") || !strings.Contains(status, "Valid levels:") {
		t.Fatalf("invalid-level status %q does not include useful reasoning choices", status)
	}
}

func TestCycleReasoningUsesPersistentConfigOption(t *testing.T) {
	a := newReasoningApp(t)
	a.cycleReasoning()
	waitForReasoning(t, a, "high")
}

func TestReasoningCatalogAndRemoteOptionUpdates(t *testing.T) {
	a := newReasoningApp(t)
	found := false
	for _, item := range a.slashCatalog() {
		if item.Value == "reasoning" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("slash catalog does not include reasoning")
	}

	opts := append([]acp.ConfigOption(nil), a.configOpts...)
	for i := range opts {
		if opts[i].ID == "reasoning" {
			opts[i].CurrentValue = "high"
		}
	}
	a.applyLoopMessage(updateMsg{sessionID: a.sessionID, update: acp.ConfigOptionUpdate{ConfigOptions: opts}})
	if a.reasoning != "high" {
		t.Fatalf("reasoning after option update = %q, want high", a.reasoning)
	}

	// A remote backend has no local session state, so adoption must retain the
	// current reasoning option for its footer and selector.
	for i := range opts {
		if opts[i].ID == "reasoning" {
			opts[i].CurrentValue = "low"
		}
	}
	a.adoptSession("remote-session", nil, opts)
	if a.reasoning != "low" {
		t.Fatalf("reasoning after remote adoption = %q, want low", a.reasoning)
	}
	if got := strings.Join(a.foot.Render(100), "\n"); !strings.Contains(got, "low") {
		t.Fatalf("remote footer %q does not show adopted reasoning", got)
	}
}

func TestReasoningSelectorAndCommandAreBlockedByLocalShell(t *testing.T) {
	a := newReasoningApp(t)
	a.shellActive = true
	a.openReasoningSelector()
	if a.modal != nil {
		t.Fatal("reasoning selector opened over a local shell")
	}
	if !a.dispatchSlash("/reasoning") {
		t.Fatal("/reasoning was not handled")
	}
	if a.modal != nil {
		t.Fatal("reasoning command opened a selector over a local shell")
	}
}
