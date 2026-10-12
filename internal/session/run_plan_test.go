package session_test

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/plans"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestRunPlanSlugFromPromptMeta(t *testing.T) {
	meta := map[string]interface{}{plans.MetaRunPlanSlug: "auth-refactor"}
	if got := session.RunPlanSlugFromPromptMeta(meta); got != "auth-refactor" {
		t.Fatalf("got %q", got)
	}
	if got := session.RunPlanSlugFromPromptMeta(nil); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestExtractRunPlanSlugFromPromptText(t *testing.T) {
	got := session.ExtractRunPlanSlugFromPromptText("Please @plans/auth-refactor.plan.md")
	if got != "auth-refactor" {
		t.Fatalf("mention: %q", got)
	}
	got = session.ExtractRunPlanSlugFromPromptText("implement the plan my-feature")
	if got != "my-feature" {
		t.Fatalf("phrase: %q", got)
	}
}

func TestRunPlanDoesNotSetTodo(t *testing.T) {
	cfg := testConfig()
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default(), t.TempDir(), store)
	ctx := context.Background()
	st, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	id := st.SessionID
	state := mgr.SessionByID(id)
	if state == nil {
		t.Fatal("no state")
	}
	state.SetPlan([]acp.PlanEntry{{Content: "keep me", Status: "pending"}})
	dir, err := store.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plans.Write(dir, "run-me", plans.DefaultContent("run-me", "Run me")); err != nil {
		t.Fatal(err)
	}
	before := state.GetPlan()
	_, err = mgr.HandleSessionPrompt(ctx, acp.SessionPromptParams{
		SessionID: id,
		Meta:      map[string]interface{}{plans.MetaRunPlanSlug: "run-me"},
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "Implement the plan."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	after := state.GetPlan()
	if len(before) != len(after) || before[0].Content != after[0].Content {
		t.Fatalf("plan changed: before %+v after %+v", before, after)
	}
	if state.GetMode() != string(session.ModeAgent) {
		t.Fatalf("mode: %s", state.GetMode())
	}
}

func TestRunPlanReadFailurePersistsActivityError(t *testing.T) {
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	runs := 0
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		runs++
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(testConfig(), noopSender{}, runner, slog.Default(), "/tmp", store)
	ctx := context.Background()
	res, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: "/tmp"})
	if err != nil {
		t.Fatal(err)
	}
	state := mgr.SessionByID(res.SessionID)
	if _, err := mgr.RunPlan(ctx, res.SessionID, "missing-plan", nil); err == nil {
		t.Fatal("RunPlan unexpectedly succeeded for a missing plan")
	}
	if runs != 0 {
		t.Fatalf("runner ran %d time(s) after plan read failed", runs)
	}
	if state.GetActivitySeq() != 1 || state.GetLastErrorSeq() != 1 {
		t.Fatalf("pre-run failure counters = activity=%d lastError=%d, want 1 and 1", state.GetActivitySeq(), state.GetLastErrorSeq())
	}
	snap, err := store.ReadSnapshot(res.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.ActivitySeq != 1 || snap.Meta.LastErrorSeq != 1 {
		t.Fatalf("persisted pre-run failure counters = activity=%d lastError=%d, want 1 and 1", snap.Meta.ActivitySeq, snap.Meta.LastErrorSeq)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := mgr.RunPlan(canceled, res.SessionID, "missing-plan", nil); err == nil {
		t.Fatal("canceled RunPlan unexpectedly succeeded for a missing plan")
	}
	if state.GetActivitySeq() != 2 || state.GetLastErrorSeq() != 1 {
		t.Fatalf("canceled pre-run counters = activity=%d lastError=%d, want 2 and 1", state.GetActivitySeq(), state.GetLastErrorSeq())
	}
}

// Ask mode is read-only, so neither run-plan shortcut may start a plan run: the
// metadata one is refused, and a plan mention stays reading material for the
// ask turn instead of switching the session to agent mode.
func TestAskModeNeverRunsPlans(t *testing.T) {
	cfg := testConfig()
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	var modesSeen []string
	var prompts [][]acp.ContentBlock
	runner := func(_ context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		modesSeen = append(modesSeen, st.GetMode())
		prompts = append(prompts, prompt)
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), store)
	ctx := context.Background()
	res, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	id := res.SessionID
	state := mgr.SessionByID(id)
	if state == nil {
		t.Fatal("no state")
	}
	state.SetMode(string(session.ModeAsk))
	dir, err := store.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plans.Write(dir, "run-me", plans.DefaultContent("run-me", "Run me")); err != nil {
		t.Fatal(err)
	}

	_, err = mgr.HandleSessionPrompt(ctx, acp.SessionPromptParams{
		SessionID: id,
		Meta:      map[string]interface{}{plans.MetaRunPlanSlug: "run-me"},
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "Implement the plan."}},
	})
	if err == nil {
		t.Fatal("runPlanSlug metadata was accepted in ask mode")
	}
	if len(modesSeen) != 0 {
		t.Fatalf("a turn ran despite the refusal: %v", modesSeen)
	}
	if got := state.GetMode(); got != string(session.ModeAsk) {
		t.Fatalf("mode switched to %q", got)
	}

	_, err = mgr.HandleSessionPrompt(ctx, acp.SessionPromptParams{
		SessionID: id,
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "Please review @plans/run-me.plan.md and explain it."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(modesSeen) != 1 || modesSeen[0] != string(session.ModeAsk) {
		t.Fatalf("expected one ask-mode turn, got %v", modesSeen)
	}
	if got := state.GetMode(); got != string(session.ModeAsk) {
		t.Fatalf("mode switched to %q", got)
	}
	inlined := false
	for _, b := range prompts[0] {
		if b.Type == "resource" && b.Resource != nil && strings.Contains(b.Resource.URI, "run-me") {
			inlined = true
		}
	}
	if !inlined {
		t.Fatalf("plan mention was not inlined as reading material: %+v", prompts[0])
	}
}

// RunPlan is the shared execution entry point (prompt shortcuts, HTTP route),
// so it fails closed on its own when the session is in ask mode.
func TestRunPlanRefusesAskModeSession(t *testing.T) {
	cfg := testConfig()
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	runs := 0
	runner := func(context.Context, *session.State, []acp.ContentBlock, acp.UpdateSender) (string, error) {
		runs++
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(cfg, noopSender{}, runner, slog.Default(), t.TempDir(), store)
	ctx := context.Background()
	res, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	state := mgr.SessionByID(res.SessionID)
	state.SetMode(string(session.ModeAsk))
	dir, err := store.EnsureLayout(res.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plans.Write(dir, "run-me", plans.DefaultContent("run-me", "Run me")); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.RunPlan(ctx, res.SessionID, "run-me", nil); err == nil || !strings.Contains(err.Error(), "ask mode") {
		t.Fatalf("RunPlan in ask mode: err = %v, want the ask refusal", err)
	}
	if runs != 0 {
		t.Fatalf("the plan ran %d time(s) in ask mode", runs)
	}
	if got := state.GetMode(); got != string(session.ModeAsk) {
		t.Fatalf("mode switched to %q", got)
	}
}

// A restricted turn (a messenger user who is not the bot's admin) does not
// run a plan: running one switches the session out of plan mode, a setting
// that is the admins' to change.
func TestRestrictedTurnDoesNotRunAPlan(t *testing.T) {
	cfg := testConfig()
	root := t.TempDir()
	store := &session.FileStore{Root: filepath.Join(root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, noopRunner, slog.Default(), t.TempDir(), store)
	ctx := context.Background()
	st, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	id := st.SessionID
	state := mgr.SessionByID(id)
	state.SetMode(string(session.ModePlan))
	dir, err := store.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plans.Write(dir, "run-me", plans.DefaultContent("run-me", "Run me")); err != nil {
		t.Fatal(err)
	}
	for _, params := range []acp.SessionPromptParams{
		{SessionID: id, Meta: map[string]interface{}{plans.MetaRunPlanSlug: "run-me"}, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "go"}}},
		{SessionID: id, Prompt: []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "Implement the plan @plans/run-me.plan.md"}}},
	} {
		if _, err := mgr.HandleSessionPromptWithSender(ctx, params, noopSender{}, &session.PromptRunOpts{
			Restriction: &session.TurnRestriction{AskAlways: true, ConfineToWorkspace: true},
		}); err != nil {
			t.Fatal(err)
		}
		if state.GetMode() != string(session.ModePlan) {
			t.Fatalf("a restricted turn ran the plan and switched the mode to %s", state.GetMode())
		}
	}
}

// newRunPlanSession is a persisted agent-mode session holding the plan
// "run-me", with a runner that records the mode every turn runs in.
func newRunPlanSession(t *testing.T, sender acp.UpdateSender) (*session.Manager, *session.State, *[]string) {
	t.Helper()
	store := &session.FileStore{Root: filepath.Join(t.TempDir(), "sessions")}
	var modes []string
	runner := func(_ context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		modes = append(modes, st.EffectiveMode())
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(testConfig(), sender, runner, slog.Default(), t.TempDir(), store)
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := store.EnsureLayout(res.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plans.Write(dir, "run-me", plans.DefaultContent("run-me", "Run me")); err != nil {
		t.Fatal(err)
	}
	return mgr, mgr.SessionByID(res.SessionID), &modes
}

// armMode sends a prompt that is only a mode command, which runs no turn.
func armMode(t *testing.T, mgr *session.Manager, id, command string) {
	t.Helper()
	res, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: id,
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: command}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.SettingsNotice == "" {
		t.Fatalf("%q was not taken as a settings command", command)
	}
}

func modeOverrides(st *session.State) []session.TurnOverride {
	var out []session.TurnOverride
	for _, o := range st.TurnOverrides() {
		if o.Setting == session.SettingMode {
			out = append(out, o)
		}
	}
	return out
}

// A plan run is the switch to agent mode: an armed plan detour neither keeps
// the run in plan mode nor outlives it, and a permission answered later
// resumes the run in agent mode too. Every surface is told the detour ended.
func TestRunPlanEndsAnArmedModeDetour(t *testing.T) {
	// --once is consumed whole by the plan run's own turn, --count=3 leaves
	// turns armed after it: both have to end.
	for _, command := range []string{"/plan --once", "/plan --count=3"} {
		t.Run(command, func(t *testing.T) { testRunPlanEndsAnArmedModeDetour(t, command) })
	}
}

func testRunPlanEndsAnArmedModeDetour(t *testing.T, command string) {
	sender := &captureSender{}
	mgr, state, modes := newRunPlanSession(t, sender)
	id := state.GetID()
	armMode(t, mgr, id, command)

	if _, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: id,
		Meta:      map[string]interface{}{plans.MetaRunPlanSlug: "run-me"},
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "Implement the plan."}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(*modes) != 1 || (*modes)[0] != string(session.ModeAgent) {
		t.Fatalf("the plan run ran in %v, want one agent-mode turn", *modes)
	}
	if got := modeOverrides(state); len(got) != 0 {
		t.Fatalf("the plan detour outlived the plan run: %+v", got)
	}
	state.ResumeTurnSettings()
	if got := state.EffectiveMode(); got != string(session.ModeAgent) {
		t.Fatalf("a permission resume of the plan run would run in %q mode", got)
	}
	state.EndTurnSettings()

	published := false
	sender.mu.Lock()
	for _, u := range sender.ups {
		if up, ok := u.(acp.SessionSettingsUpdate); ok && up.Source == "plan_run" {
			published = true
			for _, o := range up.Settings.Overrides {
				if o.Setting == session.SettingMode {
					t.Errorf("the published snapshot still holds the mode override %+v", o)
				}
			}
		}
	}
	sender.mu.Unlock()
	if !published {
		t.Fatal("no settings snapshot told the surfaces that the plan detour ended")
	}

	if _, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: id,
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "and the tests"}},
	}); err != nil {
		t.Fatal(err)
	}
	if got := (*modes)[len(*modes)-1]; got != string(session.ModeAgent) {
		t.Fatalf("the turn after the plan run ran in %q mode", got)
	}
}

// The direct route (RunPlan, behind the HTTP plans endpoint) does not take the
// armed settings of a turn, but the plan run still ends them.
func TestRunPlanDirectRouteEndsAnArmedModeDetour(t *testing.T) {
	mgr, state, modes := newRunPlanSession(t, noopSender{})
	armMode(t, mgr, state.GetID(), "/plan --count=2")
	if _, err := mgr.RunPlan(context.Background(), state.GetID(), "run-me", nil); err != nil {
		t.Fatal(err)
	}
	if len(*modes) != 1 || (*modes)[0] != string(session.ModeAgent) {
		t.Fatalf("the plan run ran in %v, want one agent-mode turn", *modes)
	}
	if got := modeOverrides(state); len(got) != 0 {
		t.Fatalf("the plan detour outlived the plan run: %+v", got)
	}
}

// Running a plan is an explicit switch to agent mode, the operator's later
// word on the mode: in an agent session it ends an /ask detour as it ends a
// /plan one, on the prompt route that has already taken the detour into its
// turn and on the direct route that has not. Only an ask session refuses it.
func TestRunPlanEndsAnArmedAskDetour(t *testing.T) {
	for _, route := range []string{"prompt", "direct"} {
		t.Run(route, func(t *testing.T) {
			mgr, state, modes := newRunPlanSession(t, noopSender{})
			id := state.GetID()
			armMode(t, mgr, id, "/ask --count=2")
			var err error
			if route == "prompt" {
				_, err = mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
					SessionID: id,
					Meta:      map[string]interface{}{plans.MetaRunPlanSlug: "run-me"},
					Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "Implement the plan."}},
				})
			} else {
				_, err = mgr.RunPlan(context.Background(), id, "run-me", nil)
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(*modes) != 1 || (*modes)[0] != string(session.ModeAgent) {
				t.Fatalf("the plan run ran in %v, want one agent-mode turn", *modes)
			}
			if got := modeOverrides(state); len(got) != 0 {
				t.Fatalf("the ask detour outlived the plan run: %+v", got)
			}
		})
	}
}

// A plan that an /ask --once turn mentions is reading material for that
// turn, as in an ask session: the mention does not start a plan run.
func TestAskOnceTurnReadsAMentionedPlan(t *testing.T) {
	mgr, state, modes := newRunPlanSession(t, noopSender{})
	armMode(t, mgr, state.GetID(), "/ask --once")
	if _, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: state.GetID(),
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "Please review @plans/run-me.plan.md and explain it."}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(*modes) != 1 || (*modes)[0] != string(session.ModeAsk) {
		t.Fatalf("expected one ask-mode turn reading the plan, got %v", *modes)
	}
	if state.PendingPlanContext() != "" {
		t.Fatal("the plan mention started a plan run in an ask turn")
	}
	if got := state.GetMode(); got != string(session.ModeAgent) {
		t.Fatalf("the session mode moved to %q", got)
	}
}

// Before its turn is admitted a plan run reads the session's mode, not the
// mode another running turn holds: a plan run started while an /ask --once
// turn runs is refused because the session is busy, not because of a mode
// that belongs to that other turn.
func TestRunPlanWhileAnAskOnceTurnRunsIsRefusedAsBusy(t *testing.T) {
	store := &session.FileStore{Root: filepath.Join(t.TempDir(), "sessions")}
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	runner := func(_ context.Context, st *session.State, _ []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
		if st.EffectiveMode() == string(session.ModeAsk) {
			entered <- struct{}{}
			<-release
		}
		return string(acp.StopReasonEndTurn), nil
	}
	mgr := session.NewManager(testConfig(), noopSender{}, runner, slog.Default(), t.TempDir(), store)
	res, err := mgr.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	id := res.SessionID
	dir, err := store.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plans.Write(dir, "run-me", plans.DefaultContent("run-me", "Run me")); err != nil {
		t.Fatal(err)
	}

	askDone := make(chan error, 1)
	go func() {
		_, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
			SessionID: id,
			Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "/ask --once how does it work?"}},
		})
		askDone <- err
	}()
	<-entered
	_, err = mgr.RunPlan(context.Background(), id, "run-me", nil)
	close(release)
	if askErr := <-askDone; askErr != nil {
		t.Fatal(askErr)
	}
	if !errors.Is(err, session.ErrSessionTurnBusy) {
		t.Fatalf("a plan run during another turn: err = %v, want the busy refusal", err)
	}
	if _, err := mgr.RunPlan(context.Background(), id, "run-me", nil); err != nil {
		t.Fatalf("the plan run after the ask turn: %v", err)
	}
}

// An ask session never runs a plan, also when /agent --once lends one turn
// agent mode: a plan that turn mentions is reading material, not a run that
// the ask session would then refuse with an error.
func TestAskSessionAgentOnceTurnReadsAMentionedPlan(t *testing.T) {
	mgr, state, modes := newRunPlanSession(t, noopSender{})
	state.SetMode(string(session.ModeAsk))
	armMode(t, mgr, state.GetID(), "/agent --once")
	if _, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: state.GetID(),
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "Please review @plans/run-me.plan.md and explain it."}},
	}); err != nil {
		t.Fatalf("a plan mention in an /agent --once turn of an ask session failed: %v", err)
	}
	if len(*modes) != 1 || (*modes)[0] != string(session.ModeAgent) {
		t.Fatalf("expected one agent-mode turn reading the plan, got %v", *modes)
	}
	if state.PendingPlanContext() != "" {
		t.Fatal("the plan mention started a plan run in an ask session")
	}
	if got := state.GetMode(); got != string(session.ModeAsk) {
		t.Fatalf("the ask session moved to %q", got)
	}
}

// A plan mention that runs the plan in an agent session ends a /plan detour
// the same way the run-plan metadata does.
func TestPlanMentionRunEndsAnArmedModeDetour(t *testing.T) {
	mgr, state, modes := newRunPlanSession(t, noopSender{})
	armMode(t, mgr, state.GetID(), "/plan --count=3")
	if _, err := mgr.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{
		SessionID: state.GetID(),
		Prompt:    []acp.ContentBlock{{Type: acp.ContentTypeText, Text: "Implement the plan @plans/run-me.plan.md"}},
	}); err != nil {
		t.Fatal(err)
	}
	if len(*modes) != 1 || (*modes)[0] != string(session.ModeAgent) {
		t.Fatalf("the plan run ran in %v, want one agent-mode turn", *modes)
	}
	if got := modeOverrides(state); len(got) != 0 {
		t.Fatalf("the plan detour outlived the plan run: %+v", got)
	}
}
