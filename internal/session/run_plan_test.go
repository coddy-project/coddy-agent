package session_test

import (
	"context"
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
