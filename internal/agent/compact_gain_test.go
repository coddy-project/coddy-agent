package agent

// The gain check before an automatic fold inside the turn (issue #490): a fold
// that cannot bring the request under the threshold is skipped, because the next
// step would fold again, a summarizer call per step and a summary of a summary.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// gainSkipLog is the line the check logs when it skips a fold.
const gainSkipLog = "auto-compaction skipped: folding the earlier steps of this turn cannot bring the request under the threshold"

// A window of 16000 tokens at the threshold of 80% triggers at 12800. With 13000
// tokens of overhead, nothing the fold can do gets under it.
func TestAutomaticInTurnFoldIsSkippedWhenTheOverheadAloneReachesTheThreshold(t *testing.T) {
	st := inTurnSession(t, "Audit every module and report.", 6)
	provider := &compactCannedProvider{t: t, summary: "must not be asked"}
	ag, logs := windowAgent(t, st, config.Compaction{}, provider, 16000, 13000, 1000)
	before := len(st.GetMessages())

	for i := 0; i < 3; i++ {
		if ag.maybeAutoCompact(context.Background()) {
			t.Fatal("a fold that cannot get under the threshold ran")
		}
	}
	if len(provider.requests) != 0 {
		t.Fatalf("the summarizer was asked %d times", len(provider.requests))
	}
	if len(st.GetMessages()) != before {
		t.Fatal("the history was touched")
	}
	if got := strings.Count(logs.String(), gainSkipLog); got != 1 {
		t.Fatalf("the skip was logged %d times in one turn, want once:\n%s", got, logs)
	}
	for _, want := range []string{"thresholdTokens=12800", "overheadTokens=13000", "smallestRequestTokens="} {
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("the log lacks %q:\n%s", want, logs)
		}
	}
	if strings.Contains(logs.String(), "auto-compaction skipped: no earlier turn") {
		t.Fatalf("the skip was reported as having nothing to fold:\n%s", logs)
	}
}

// The check runs before the hooks, like the choice of the boundary: a PreCompact
// hook is not woken for a fold that will not happen.
func TestFutileInTurnFoldReturnsNothingToCompactBeforeTheHooks(t *testing.T) {
	record := filepath.Join(t.TempDir(), "pre-compact.json")
	provider := &compactCannedProvider{t: t, summary: "must not be asked"}
	st := inTurnSession(t, "Audit every module and report.", 6)
	ag := hookedCompactAgent(t, st, provider, record)
	ag.cfg.Models[0].MaxContextTokens = 16000
	st.SetLastContextBreakdown(&session.ContextBreakdown{EstimatedTotal: 14000, Conversation: 1000})

	_, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true})
	var futile *foldFutileError
	if !errors.Is(err, ErrNothingToCompact) || !errors.As(err, &futile) {
		t.Fatalf("err = %v, want ErrNothingToCompact carrying the numbers", err)
	}
	if futile.overhead != 13000 || futile.threshold != 12800 || futile.estimate < futile.threshold {
		t.Fatalf("numbers = %+v", futile)
	}
	if len(provider.requests) != 0 {
		t.Fatal("the summarizer was called")
	}
	if _, statErr := os.Stat(record); statErr == nil {
		t.Fatal("a PreCompact hook ran for a fold that was skipped")
	}
}

// Where the floor fits under the threshold the fold runs as before.
func TestAutomaticInTurnFoldRunsWhenTheFloorFitsUnderTheThreshold(t *testing.T) {
	st := inTurnSession(t, "Audit every module and report.", 6)
	provider := &compactCannedProvider{t: t, summary: "the first modules were audited"}
	// 11000 of overhead and 2000 of conversation reach 12800; the latest step and the
	// prefix come to a few hundred tokens more, still well under the threshold.
	ag, logs := windowAgent(t, st, config.Compaction{}, provider, 16000, 11000, 2000)

	if !ag.maybeAutoCompact(context.Background()) {
		t.Fatalf("the fold was skipped although the smallest request fits:\n%s", logs)
	}
	if strings.Contains(logs.String(), gainSkipLog) {
		t.Fatalf("the skip was logged although the fold ran:\n%s", logs)
	}
}

// The summary the window opens with is in the next request as well, and the next
// summary will be about as long.
func TestGainCheckCountsTheSummaryTheWindowOpensWith(t *testing.T) {
	const prompt = "Audit every module and report."
	build := func(t *testing.T, summary string) *session.State {
		t.Helper()
		row := session.NewInTurnCompactionSummaryMessage(session.TurnSourceUser, prompt, summary, "m")
		return stateOpenedAt(t, 0, []llm.Message{stamped(prompt, openedAt)}, steps("a", 4), []llm.Message{row}, steps("b", 3))
	}
	// 9500 of overhead; the fold's request is the overhead, the prefix, the summary
	// and the latest step. A 12000-character summary is about 4000 tokens.
	longSummary := strings.Repeat("a", 12000)

	t.Run("a long summary makes the fold futile", func(t *testing.T) {
		st := build(t, longSummary)
		provider := &compactCannedProvider{t: t, summary: "must not be asked"}
		ag, logs := windowAgent(t, st, config.Compaction{}, provider, 16000, 9500, 4500)

		if ag.maybeAutoCompact(context.Background()) {
			t.Fatal("the fold ran although the next summary alone would fill the window")
		}
		if !strings.Contains(logs.String(), gainSkipLog) || !strings.Contains(logs.String(), "summaryTokens=4000") {
			t.Fatalf("the skip does not name the summary:\n%s", logs)
		}
	})

	t.Run("the same window with a short summary folds", func(t *testing.T) {
		st := build(t, "short")
		provider := &compactCannedProvider{t: t, summary: "audited"}
		ag, logs := windowAgent(t, st, config.Compaction{}, provider, 16000, 9500, 4500)

		if !ag.maybeAutoCompact(context.Background()) {
			t.Fatalf("the fold was skipped:\n%s", logs)
		}
	})
}

// The recovery from a refused request is one-shot and aims at the refusal's
// limit: it is not gated.
func TestRecoveryIsNotGatedByTheGainCheck(t *testing.T) {
	st := inTurnSession(t, "Audit every module and report.", 6)
	provider := &compactCannedProvider{t: t, summary: "the first modules were audited"}
	ag, _ := windowAgent(t, st, config.Compaction{}, provider, 16000, 13000, 1000)

	res, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true, Recovery: true, LimitTokens: 16000})
	if err != nil {
		t.Fatalf("the recovery was refused: %v", err)
	}
	if !res.InTurn || len(provider.requests) != 1 {
		t.Fatalf("result = %+v after %d summarizer calls, want a fold", res, len(provider.requests))
	}
}
