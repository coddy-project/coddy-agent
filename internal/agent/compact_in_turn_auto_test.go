package agent

// The automatic trigger's fallback to the in-turn fold (maybeAutoCompact) and
// what compaction.in_turn.enable: false restores.

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// overThresholdAgent is a compact test agent whose context is at 90% of a
// 10000-token window, with logs captured.
func overThresholdAgent(t *testing.T, st *session.State, comp config.Compaction, provider llm.Provider) (*Agent, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	ag := compactTestAgent(t, st, comp, provider)
	ag.log = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ag.cfg.Models[0].MaxContextTokens = 10000
	st.SetLastContextBreakdown(&session.ContextBreakdown{EstimatedTotal: 9000})
	return ag, logs
}

func TestMaybeAutoCompactFoldsTheTurnInProgressWhenNoEarlierTurnExists(t *testing.T) {
	const prompt = "Audit every module and report."
	st := inTurnSession(t, prompt, 6)
	provider := &compactCannedProvider{t: t, summary: "the first modules were audited"}
	ag, logs := overThresholdAgent(t, st, config.Compaction{}, provider)

	if !ag.maybeAutoCompact(context.Background()) {
		t.Fatalf("a turn over the threshold with six steps must be folded:\n%s", logs)
	}
	window := session.MessagesForLLM(st.GetMessages())
	if !window[0].CompactionSummary || !strings.HasPrefix(window[0].Content, prompt) {
		t.Fatalf("the window must open with the prompt: %q", window[0].Content)
	}
	// The overhead (9000 of the 10000 tokens) leaves no room under the aim, so
	// the latest step alone stays.
	if got := len(window); got != 1+2 {
		t.Fatalf("window holds %d messages, want the row and one step", got)
	}
	if !strings.Contains(logs.String(), "auto-compacted the turn in progress") || !strings.Contains(logs.String(), "keptSteps=1") {
		t.Fatalf("the fold is not logged as an in-turn one:\n%s", logs)
	}
	if strings.Contains(logs.String(), "auto-compaction skipped") {
		t.Fatalf("the skip was logged although the fold ran:\n%s", logs)
	}
}

// Earlier turns are what the regular compaction folds; the in-turn fold is only
// the fallback when there are none.
func TestMaybeAutoCompactStillFoldsEarlierTurnsFirst(t *testing.T) {
	st := seededCompactState(t, 3)
	for _, m := range fixtureTurn("current prompt", 4, 1, 600) {
		st.AddMessage(m)
	}
	keep := 1
	provider := &compactCannedProvider{t: t, summary: "the earlier turns"}
	ag, logs := overThresholdAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)

	if !ag.maybeAutoCompact(context.Background()) {
		t.Fatalf("did not compact:\n%s", logs)
	}
	row := session.MessagesForLLM(st.GetMessages())[0]
	if !strings.HasPrefix(row.Content, "The earlier conversation was compacted.") {
		t.Fatalf("an earlier turn existed, so the regular row was due: %q", firstChars(row.Content, 80))
	}
	if strings.Contains(logs.String(), "auto-compacted the turn in progress") {
		t.Fatalf("the turn in progress was folded although earlier turns could be:\n%s", logs)
	}
}

func TestMaybeAutoCompactWithInTurnOffRestoresTheSkip(t *testing.T) {
	off := false
	st := inTurnSession(t, "Audit every module.", 6)
	provider := &compactCannedProvider{t: t, summary: "must not be asked"}
	ag, logs := overThresholdAgent(t, st, config.Compaction{InTurn: config.InTurn{Enabled: &off}}, provider)

	for i := 0; i < 3; i++ {
		if ag.maybeAutoCompact(context.Background()) {
			t.Fatal("in_turn.enable: false must leave a one-turn window alone")
		}
	}
	if len(provider.requests) != 0 {
		t.Fatalf("the summarizer was asked %d times", len(provider.requests))
	}
	if got := strings.Count(logs.String(), "auto-compaction skipped"); got != 1 {
		t.Fatalf("skip logged %d times in one turn, want once:\n%s", got, logs)
	}
	for _, m := range st.GetMessages() {
		if m.CompactionSummary {
			t.Fatal("no summary row expected")
		}
	}
}

// One step is the floor: with a single step the turn has nothing earlier to
// fold, and the skip is said once, not before every step.
func TestMaybeAutoCompactLogsOnceWhenOnlyTheLatestStepIsLeft(t *testing.T) {
	st := inTurnSession(t, "Audit every module.", 1)
	provider := &compactCannedProvider{t: t, summary: "must not be asked"}
	ag, logs := overThresholdAgent(t, st, config.Compaction{}, provider)

	for i := 0; i < 3; i++ {
		if ag.maybeAutoCompact(context.Background()) {
			t.Fatal("the latest step must never be folded")
		}
	}
	if got := strings.Count(logs.String(), "auto-compaction skipped"); got != 1 {
		t.Fatalf("skip logged %d times in one turn, want once:\n%s", got, logs)
	}
}

// A summarizer that fails is reported like it is for any other compaction, and the
// turn goes on uncompacted (the fold is fail-open).
func TestMaybeAutoCompactInTurnFoldFailsOpen(t *testing.T) {
	st := inTurnSession(t, "Audit every module.", 5)
	provider := &compactCannedProvider{t: t, err: context.DeadlineExceeded}
	ag, logs := overThresholdAgent(t, st, config.Compaction{}, provider)
	before := len(st.GetMessages())

	if ag.maybeAutoCompact(context.Background()) {
		t.Fatal("a failed summarizer must report false")
	}
	if len(st.GetMessages()) != before {
		t.Fatal("history must stay untouched on failure")
	}
	if !strings.Contains(logs.String(), "auto-compaction failed") {
		t.Fatalf("the failure is not logged:\n%s", logs)
	}
}
