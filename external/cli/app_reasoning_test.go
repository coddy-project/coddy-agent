//go:build cli

package cli

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type blockedReasoningBackend struct {
	backend
	started chan string
	release chan error
}

func (b *blockedReasoningBackend) HandleSessionSetConfigOption(ctx context.Context, params acp.SessionSetConfigOptionParams) (*acp.SessionSetConfigOptionResult, error) {
	if params.ConfigID != "reasoning" {
		return b.backend.HandleSessionSetConfigOption(ctx, params)
	}
	b.started <- params.Value
	if err := <-b.release; err != nil {
		return nil, err
	}
	return b.backend.HandleSessionSetConfigOption(ctx, params)
}

// over puts b in front of inner, in the shape newReasoningAppOver takes.
func (b *blockedReasoningBackend) over(inner backend) backend {
	b.backend = inner
	return b
}

func waitForReasoningCall(t *testing.T, b *blockedReasoningBackend) string {
	t.Helper()
	select {
	case level := <-b.started:
		return level
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reasoning backend call")
		return ""
	}
}

func newReasoningApp(t *testing.T) *App {
	t.Helper()
	return newReasoningAppOver(t, nil)
}

// newReasoningAppOver builds the app over wrap(manager) when wrap is set. The
// wrapper has to be in place before Start: Start launches workers that read the
// backend (the task list read), so swapping it afterwards races with them.
func newReasoningAppOver(t *testing.T, wrap func(backend) backend) *App {
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
	var be backend = mgr
	if wrap != nil {
		be = wrap(mgr)
	}
	app := newApp(cfg, be, slog.New(slog.DiscardHandler), term, "dark", true)
	late.inner = app.Sender()
	if err := app.Start(context.Background(), "", false); err != nil {
		t.Fatalf("start app: %v", err)
	}
	// Registered after TempDir, so it runs first: a settings change is
	// written by a worker after the value the test waits for is visible,
	// and removing the home under that write fails on macOS and Windows.
	t.Cleanup(func() { app.JoinWorkers(5 * time.Second) })
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

func TestReasoningSelectionsAreAppliedInInvocationOrder(t *testing.T) {
	b := &blockedReasoningBackend{
		started: make(chan string, 2),
		release: make(chan error, 2),
	}
	a := newReasoningAppOver(t, b.over)

	a.setReasoning("low")
	if got := waitForReasoningCall(t, b); got != "low" {
		t.Fatalf("first reasoning call = %q, want low", got)
	}
	a.setReasoning("high")
	select {
	case got := <-b.started:
		t.Fatalf("second reasoning call %q started before the first completed", got)
	case <-time.After(25 * time.Millisecond):
	}

	b.release <- nil
	if got := waitForReasoningCall(t, b); got != "high" {
		t.Fatalf("second reasoning call = %q, want high", got)
	}
	b.release <- nil
	waitForReasoning(t, a, "high")
}

func TestReasoningFailureUsesLevelsAtInvocation(t *testing.T) {
	b := &blockedReasoningBackend{
		started: make(chan string, 1),
		release: make(chan error, 1),
	}
	a := newReasoningAppOver(t, b.over)
	originalLevels := a.reasoningLevels()

	a.setReasoning("invalid")
	if got := waitForReasoningCall(t, b); got != "invalid" {
		t.Fatalf("reasoning call = %q, want invalid", got)
	}
	for i := range a.configOpts {
		if a.configOpts[i].ID == "reasoning" {
			a.configOpts[i].Options = []acp.ConfigOptionValue{{Value: "replacement"}}
		}
	}
	a.modelID = "replacement-model"
	b.release <- errors.New("invalid reasoning level")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case msg := <-a.updatesCh:
			a.applyLoopMessage(msg)
		default:
		}
		if strings.Contains(transcriptText(a), "offered:") {
			break
		}
		time.Sleep(time.Millisecond)
	}
	status := transcriptText(a)
	wantLevels := strings.Join(originalLevels, ", ")
	if !strings.Contains(status, "Valid levels: "+wantLevels) {
		t.Fatalf("failure status %q does not retain original levels %q", status, wantLevels)
	}
	if strings.Contains(status, "replacement") {
		t.Fatalf("failure status %q used levels changed after invocation", status)
	}
}

func TestReasoningSelectorFiltersAndSelectsHigh(t *testing.T) {
	a := newReasoningApp(t)
	a.openReasoningSelector()

	sel, ok := a.modal.(*selectorModal)
	if !ok {
		t.Fatalf("modal = %T, want reasoning selector", a.modal)
	}
	a.dispatchInput([]byte("hi"))
	if got := sel.list.SelectedItem(); got == nil || got.Value != "high" {
		t.Fatalf("filtered selection = %v, want high", got)
	}
	rows := strings.Join(sel.list.Render(100), "\n")
	if !strings.Contains(rows, "high") || strings.Contains(rows, "minimal") || strings.Contains(rows, "medium") || strings.Contains(rows, "low") {
		t.Fatalf("filtered rows = %q, want only high", rows)
	}

	a.dispatchInput([]byte("\r"))
	waitForReasoning(t, a, "high")
}

func TestReasoningSlashSetsHigh(t *testing.T) {
	a := newReasoningApp(t)
	if !a.dispatchSlash("/reasoning high") {
		t.Fatal("/reasoning high was not handled")
	}
	if a.modal != nil {
		t.Fatalf("modal = %T after /reasoning high, want no selector", a.modal)
	}
	waitForReasoning(t, a, "high")
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
	// The settings command answers with the levels the model offers.
	status := strings.Join(strings.Fields(transcriptText(a)), " ")
	if !strings.Contains(status, `reasoning "ultra" is not offered`) || !strings.Contains(status, "minimal, low, medium, high") {
		t.Fatalf("invalid-level status %q does not include useful reasoning choices", status)
	}
}

func TestCycleReasoningUsesPersistentConfigOption(t *testing.T) {
	a := newReasoningApp(t)
	a.cycleReasoning()
	waitForReasoning(t, a, "high")
}

func TestShiftTabCyclesReasoningThroughWrap(t *testing.T) {
	a := newReasoningApp(t)
	if !a.handleGlobalKey([]byte("\x1b[Z")) {
		t.Fatal("Shift+Tab was not handled")
	}
	waitForReasoning(t, a, "high")

	if !a.handleGlobalKey([]byte("\x1b[Z")) {
		t.Fatal("Shift+Tab was not handled after selecting high")
	}
	waitForReasoning(t, a, "minimal")
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

func TestSettingsCommandsInTheConsole(t *testing.T) {
	a := newReasoningApp(t)
	// A command followed by a message is the agent's: the manager takes it
	// before the turn that message starts.
	if a.dispatchSlash("/model stub/gpt-5.6-terra --once hello") {
		t.Fatal("a settings command with a message was taken by the console")
	}

	if !a.dispatchSlash("/permissions bypass") {
		t.Fatal("/permissions bypass was not handled")
	}
	pumpUntil(t, a, "the bypass in the footer", func() bool { return a.foot.permission == "bypass" })
	footer := strings.Join(a.foot.Render(100), "\n")
	if !strings.Contains(footer, "bypass") {
		t.Fatalf("footer does not name the permission mode:\n%s", footer)
	}
	if got := transcriptText(a); !strings.Contains(got, "Permission mode: bypass for this session") {
		t.Fatalf("no notice for the change: %q", got)
	}

	if !a.dispatchSlash("/plan --once") {
		t.Fatal("/plan --once was not handled")
	}
	pumpUntil(t, a, "the armed plan mode", func() bool {
		return len(a.foot.overrides) == 1 && a.foot.overrides[0].Value == "plan"
	})
	if st := a.mgr.SessionByID(a.sessionID); st.GetMode() != "agent" {
		t.Fatalf("--once changed the session mode to %q", st.GetMode())
	}
	if ov := a.foot.overridesText(); !strings.Contains(ov, "next turn: mode plan") {
		t.Fatalf("overrides line = %q", ov)
	}

	// A bare /permissions opens the picker; /mode is gone.
	a.dispatchSlash("/permissions")
	if a.modal == nil {
		t.Fatal("/permissions opened no picker")
	}
	a.closeModal()
	if a.dispatchSlash("/mode plan") {
		t.Fatal("/mode is still a console command")
	}
}
