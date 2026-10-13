package agent

// Unit tests of the fold that stays inside the turn being answered
// (compact_in_turn.go, the in-turn branch of CompactSession and the automatic
// trigger's fallback to it in maybeAutoCompact): where it cuts, what the summary
// row carries in front of the summary, how the opening prompt is told from the
// follow-ups the user queued during the turn, the guard that stops a turn from
// folding at every step, and the rules a row's prompt delivers. The happy path is
// in features/context_in_turn_compaction.feature.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks/hooktest"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/mention"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// --- fixtures ----------------------------------------------------------------

// fixtureStep is one step of a turn: an assistant message issuing calls
// parallel reads, with a result of resultChars characters for each.
func fixtureStep(id string, calls, resultChars int) []llm.Message {
	asst := llm.Message{Role: llm.RoleAssistant}
	var results []llm.Message
	for k := 0; k < calls; k++ {
		cid := fmt.Sprintf("%s_%d", id, k)
		asst.ToolCalls = append(asst.ToolCalls, llm.ToolCall{ID: cid, Name: "read", InputJSON: `{"path":"f.go"}`})
		results = append(results, llm.Message{Role: llm.RoleTool, ToolCallID: cid, Content: strings.Repeat("x", resultChars)})
	}
	return append([]llm.Message{asst}, results...)
}

// fixtureTurn is a prompt followed by n steps of calls parallel reads.
func fixtureTurn(prompt string, n, calls, resultChars int) []llm.Message {
	msgs := []llm.Message{{Role: llm.RoleUser, Content: prompt}}
	for i := 0; i < n; i++ {
		msgs = append(msgs, fixtureStep(fmt.Sprintf("s%d", i), calls, resultChars)...)
	}
	return msgs
}

// steps is n steps of one read each, ids made of prefix.
func steps(prefix string, n int) []llm.Message {
	var out []llm.Message
	for i := 0; i < n; i++ {
		out = append(out, fixtureStep(fmt.Sprintf("%s%d", prefix, i), 1, 600)...)
	}
	return out
}

func concatMsgs(parts ...[]llm.Message) []llm.Message {
	var out []llm.Message
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// userText is a user message as the turn's opening prompt is stored.
func userText(text string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: text}
}

// queuedFollowUp is a user message the user queued while the turn ran, marked the
// way readQueuedMessages marks it.
func queuedFollowUp(text string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: text, FollowUp: true}
}

// compactContextCall is the assistant message of a compact_context call that is
// being executed: its result is appended after the compaction returns.
func compactContextCall() llm.Message {
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_compact", Name: "compact_context", InputJSON: "{}"}}}
}

// stateOf is a session whose transcript is the given parts, in order.
func stateOf(t *testing.T, msgs ...[]llm.Message) *session.State {
	t.Helper()
	st := &session.State{ID: "sess_in_turn_unit", CWD: t.TempDir(), Mode: session.ModeAgent}
	for _, part := range msgs {
		for _, m := range part {
			st.AddMessage(m)
		}
	}
	return st
}

func windowOf(st *session.State) []llm.Message { return session.MessagesForLLM(st.GetMessages()) }

func summaryRowsOf(st *session.State) int {
	n := 0
	for _, m := range st.GetMessages() {
		if m.CompactionSummary {
			n++
		}
	}
	return n
}

// inTurnSession is one turn in progress: a prompt and n steps of one read each.
func inTurnSession(t *testing.T, prompt string, n int) *session.State {
	t.Helper()
	return stateOf(t, fixtureTurn(prompt, n, 1, 600))
}

// inTurnAgent is a compact test agent. The window is left to the default
// (128000), so the target is large and inTurnKeepRecentSteps decides how much
// stays.
func inTurnAgent(t *testing.T, st *session.State, comp config.Compaction, provider llm.Provider) *Agent {
	t.Helper()
	return compactTestAgent(t, st, comp, provider)
}

// windowAgent is a compact test agent on a window of the given size whose last
// request measured overhead tokens besides the conversation and conversation
// tokens of it, with logs captured. The overhead is recorded as the system
// prompt's share, so it survives the recount a compaction makes of the
// conversation, as it does in a real session.
func windowAgent(t *testing.T, st *session.State, comp config.Compaction, provider llm.Provider, window, overhead, conversation int) (*Agent, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	ag := compactTestAgent(t, st, comp, provider)
	ag.log = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ag.cfg.Models[0].MaxContextTokens = window
	st.SetLastContextBreakdown(&session.ContextBreakdown{SystemPrompt: overhead, EstimatedTotal: overhead + conversation, Conversation: conversation})
	return ag, logs
}

// overThresholdAgent is a compact test agent whose context is at 90% of a
// 10000-token window, with logs captured: 2000 tokens of conversation and 7000
// that every request carries besides it, so that the smallest request a fold can
// produce is still under the threshold of 8000.
func overThresholdAgent(t *testing.T, st *session.State, comp config.Compaction, provider llm.Provider) (*Agent, *bytes.Buffer) {
	t.Helper()
	return windowAgent(t, st, comp, provider, 10000, 7000, 2000)
}

func tailTokens(msgs []llm.Message, idx int) int {
	return conversationTokens(msgs[idx:], false)
}

func splitRequest(msgs []llm.Message, mod func(*inTurnSplitRequest)) inTurnSplitRequest {
	r := inTurnSplitRequest{
		msgs:         msgs,
		visibleStart: 0,
		projected:    msgs,
		keepSteps:    inTurnKeepRecentSteps,
		budget:       1 << 30,
	}
	if mod != nil {
		mod(&r)
	}
	return r
}

// --- where the fold cuts -------------------------------------------------------

func TestChooseInTurnSplit(t *testing.T) {
	// A prompt and eight steps of two parallel results of 3000 characters.
	turn := fixtureTurn("do the audit", 8, 2, 3000)
	at := func(keep int) int {
		idx, ok := session.TurnStepSplitIndex(turn, keep)
		if !ok {
			t.Fatalf("fixture has no split keeping %d steps", keep)
		}
		return idx
	}

	t.Run("a budget that holds two steps keeps two", func(t *testing.T) {
		got, err := chooseInTurnSplit(splitRequest(turn, func(r *inTurnSplitRequest) { r.budget = tailTokens(turn, at(2)) }))
		if err != nil {
			t.Fatal(err)
		}
		if got.keptSteps != 2 || got.idx != at(2) || got.regular {
			t.Fatalf("got %+v, want two steps kept at %d", got, at(2))
		}
	})

	t.Run("a budget one token short of two steps keeps one", func(t *testing.T) {
		got, err := chooseInTurnSplit(splitRequest(turn, func(r *inTurnSplitRequest) { r.budget = tailTokens(turn, at(2)) - 1 }))
		if err != nil {
			t.Fatal(err)
		}
		if got.keptSteps != 1 || got.idx != at(1) {
			t.Fatalf("got %+v, want one step kept at %d", got, at(1))
		}
	})

	t.Run("an unlimited budget is capped at inTurnKeepRecentSteps", func(t *testing.T) {
		got, err := chooseInTurnSplit(splitRequest(turn, nil))
		if err != nil {
			t.Fatal(err)
		}
		if got.keptSteps != inTurnKeepRecentSteps || got.idx != at(inTurnKeepRecentSteps) {
			t.Fatalf("got %+v, want the cap of %d steps at %d", got, inTurnKeepRecentSteps, at(inTurnKeepRecentSteps))
		}
	})

	t.Run("a cap above the steps keeps all but the first", func(t *testing.T) {
		small := fixtureTurn("p", 3, 1, 100)
		got, err := chooseInTurnSplit(splitRequest(small, nil))
		if err != nil {
			t.Fatal(err)
		}
		if got.keptSteps != 2 {
			t.Fatalf("got %+v, want two of three steps kept", got)
		}
	})

	t.Run("a cap of a billion is not walked step by step", func(t *testing.T) {
		got, err := chooseInTurnSplit(splitRequest(turn, func(r *inTurnSplitRequest) { r.keepSteps = 1 << 30 }))
		if err != nil {
			t.Fatal(err)
		}
		if got.keptSteps != 7 || got.idx != at(7) {
			t.Fatalf("got %+v, want seven of the eight steps kept", got)
		}
	})

	t.Run("the automatic floor keeps the latest step whatever it costs", func(t *testing.T) {
		got, err := chooseInTurnSplit(splitRequest(turn, func(r *inTurnSplitRequest) { r.budget = 0 }))
		if err != nil {
			t.Fatal(err)
		}
		if got.keptSteps != 1 || got.idx != at(1) {
			t.Fatalf("got %+v, want the floor of one step", got)
		}
		// A negative budget (the overhead alone is over the target) is the same.
		got, err = chooseInTurnSplit(splitRequest(turn, func(r *inTurnSplitRequest) { r.budget = -5000 }))
		if err != nil || got.keptSteps != 1 {
			t.Fatalf("got %+v err %v, want the floor of one step", got, err)
		}
	})

	t.Run("a recovery goes down to keeping nothing", func(t *testing.T) {
		got, err := chooseInTurnSplit(splitRequest(turn, func(r *inTurnSplitRequest) { r.recovery = true; r.budget = 0 }))
		if err != nil {
			t.Fatal(err)
		}
		if got.keptSteps != 0 || got.idx != len(turn) || got.regular {
			t.Fatalf("got %+v, want everything folded", got)
		}
	})

	t.Run("a recovery keeps what fits", func(t *testing.T) {
		got, err := chooseInTurnSplit(splitRequest(turn, func(r *inTurnSplitRequest) { r.recovery = true; r.budget = tailTokens(turn, at(1)) }))
		if err != nil {
			t.Fatal(err)
		}
		if got.keptSteps != 1 || got.idx != at(1) {
			t.Fatalf("got %+v, want one step kept", got)
		}
	})

	t.Run("the automatic trigger with one step has nothing to fold", func(t *testing.T) {
		_, err := chooseInTurnSplit(splitRequest(fixtureTurn("p", 1, 2, 3000), nil))
		if !errors.Is(err, ErrNothingToCompact) {
			t.Fatalf("err = %v, want ErrNothingToCompact: the latest step always stays", err)
		}
	})

	t.Run("a turn with no step has nothing to fold", func(t *testing.T) {
		for _, recovery := range []bool{false, true} {
			_, err := chooseInTurnSplit(splitRequest(fixtureTurn("p", 0, 0, 0), func(r *inTurnSplitRequest) { r.recovery = recovery }))
			if !errors.Is(err, ErrNothingToCompact) {
				t.Fatalf("recovery=%v: err = %v, want ErrNothingToCompact", recovery, err)
			}
		}
	})

	t.Run("a recovery with one step folds it", func(t *testing.T) {
		one := fixtureTurn("p", 1, 2, 3000)
		got, err := chooseInTurnSplit(splitRequest(one, func(r *inTurnSplitRequest) { r.recovery = true; r.budget = 0 }))
		if err != nil || got.keptSteps != 0 || got.idx != len(one) {
			t.Fatalf("got %+v err %v, want the single step folded", got, err)
		}
	})

	t.Run("the tail is measured on the projection the fold summarises", func(t *testing.T) {
		// Same shape, but every old result is a placeholder after eviction: the
		// kept steps cost almost nothing, so a budget the raw messages would
		// fail at keeps the cap.
		projected := make([]llm.Message, len(turn))
		copy(projected, turn)
		for i := range projected {
			if projected[i].Role == llm.RoleTool {
				projected[i].Content = "[evicted: read f.go]"
			}
		}
		budget := tailTokens(projected, at(4))
		if tailTokens(turn, at(4)) <= budget {
			t.Fatal("fixture: the raw tail should be larger than the projected one")
		}
		got, err := chooseInTurnSplit(splitRequest(turn, func(r *inTurnSplitRequest) { r.projected = projected; r.budget = budget }))
		if err != nil {
			t.Fatal(err)
		}
		if got.keptSteps != 4 {
			t.Fatalf("got %+v, want four steps: the projection fits the budget", got)
		}
	})

	t.Run("the replay window may start after the first message", func(t *testing.T) {
		// An earlier fold left a summary row: indexes are absolute, the
		// projection starts at the window.
		old := []llm.Message{{Role: llm.RoleUser, Content: "p"}, {Role: llm.RoleAssistant, Content: "early"}}
		row := session.NewInTurnCompactionSummaryMessage("p", "earlier steps", "m")
		rest := fixtureTurn("ignored", 4, 1, 500)[1:]
		msgs := concatMsgs(old, []llm.Message{row}, rest)
		visibleStart := len(old)
		got, err := chooseInTurnSplit(splitRequest(msgs, func(r *inTurnSplitRequest) {
			r.visibleStart = visibleStart
			r.projected = msgs[visibleStart:]
			r.keepSteps = 2
		}))
		if err != nil {
			t.Fatal(err)
		}
		want, _ := session.TurnStepSplitIndex(msgs, 2)
		if got.keptSteps != 2 || got.idx != want || got.idx < visibleStart {
			t.Fatalf("got %+v, want two steps at the absolute index %d", got, want)
		}
	})
}

// A recovery prefers the split the regular compaction would make, so the prompt
// stays an ordinary message, while the turn in progress fits what is left.
func TestChooseInTurnSplitRecoveryPrefersTheRegularSplit(t *testing.T) {
	earlier := []llm.Message{
		{Role: llm.RoleUser, Content: "first question"}, {Role: llm.RoleAssistant, Content: strings.Repeat("a", 6000)},
		{Role: llm.RoleUser, Content: "second question"}, {Role: llm.RoleAssistant, Content: strings.Repeat("b", 6000)},
	}
	current := fixtureTurn("the current prompt", 3, 1, 1500)
	msgs := concatMsgs(earlier, current)
	promptIdx := len(earlier)
	currentTokens := tailTokens(msgs, promptIdx)

	t.Run("the current turn fits: fold what came before it", func(t *testing.T) {
		got, err := chooseInTurnSplit(splitRequest(msgs, func(r *inTurnSplitRequest) {
			r.recovery = true
			r.budget = 0
			r.regularBudget = currentTokens
		}))
		if err != nil {
			t.Fatal(err)
		}
		if !got.regular || got.idx != promptIdx {
			t.Fatalf("got %+v, want the regular split at the current prompt (%d)", got, promptIdx)
		}
	})

	t.Run("the current turn does not fit: fold inside it", func(t *testing.T) {
		got, err := chooseInTurnSplit(splitRequest(msgs, func(r *inTurnSplitRequest) {
			r.recovery = true
			r.budget = 0
			r.regularBudget = currentTokens - 1
		}))
		if err != nil {
			t.Fatal(err)
		}
		if got.regular || got.keptSteps != 0 || got.idx != len(msgs) {
			t.Fatalf("got %+v, want a step split keeping nothing", got)
		}
	})

	t.Run("the automatic trigger never takes the regular split", func(t *testing.T) {
		// The regular compaction already ran and found nothing it may fold; the
		// in-turn fold is only asked for the turn's own steps.
		got, err := chooseInTurnSplit(splitRequest(msgs, func(r *inTurnSplitRequest) { r.regularBudget = 1 << 30 }))
		if err != nil {
			t.Fatal(err)
		}
		if got.regular {
			t.Fatalf("got %+v: the automatic path took a regular split", got)
		}
	})

	t.Run("a follow-up of the turn is no turn the regular split may cut at", func(t *testing.T) {
		// With a queued follow-up after the prompt, a regular split at the
		// follow-up would fold the opening prompt into a plain summary: the
		// regular candidate stays the prompt itself.
		withFollowUp := concatMsgs(msgs, []llm.Message{queuedFollowUp("also check the tests")}, steps("f", 1))
		got, err := chooseInTurnSplit(splitRequest(withFollowUp, func(r *inTurnSplitRequest) {
			r.recovery = true
			r.budget = 0
			r.regularBudget = 1 << 30
		}))
		if err != nil {
			t.Fatal(err)
		}
		if !got.regular || got.idx != promptIdx {
			t.Fatalf("got %+v, want the regular split at the opening prompt (%d), not at the follow-up", got, promptIdx)
		}
	})

	t.Run("a request refused before the first step folds what came before it", func(t *testing.T) {
		noSteps := concatMsgs(earlier, []llm.Message{userText("the current prompt")})
		got, err := chooseInTurnSplit(splitRequest(noSteps, func(r *inTurnSplitRequest) { r.recovery = true; r.budget = 0; r.regularBudget = 0 }))
		if err != nil {
			t.Fatal(err)
		}
		if !got.regular || got.idx != len(earlier) {
			t.Fatalf("got %+v, want the regular split before the prompt", got)
		}
	})

	t.Run("no steps and no earlier turn is nothing to fold", func(t *testing.T) {
		_, err := chooseInTurnSplit(splitRequest([]llm.Message{userText("only")}, func(r *inTurnSplitRequest) { r.recovery = true }))
		if !errors.Is(err, ErrNothingToCompact) {
			t.Fatalf("err = %v, want ErrNothingToCompact", err)
		}
	})
}

func TestInTurnTarget(t *testing.T) {
	if got := inTurnTargetTokens(49152, 70); got != 17203 {
		t.Fatalf("target = %d, want 17203: half of 70%% of the window", got)
	}
	if got := inTurnTargetTokens(100000, 80); got != 40000 {
		t.Fatalf("target = %d, want 40000", got)
	}
	if got := inTurnTargetTokens(0, 80); got != 0 {
		t.Fatalf("target = %d for an unknown limit, want 0", got)
	}
}

// --- the prompt in front of the summary ----------------------------------------

func TestInTurnPromptPrefix(t *testing.T) {
	t.Run("a prompt within its share is kept verbatim", func(t *testing.T) {
		prompt := "Fix the failing build.\n\nThen explain why it broke."
		if got := inTurnPromptPrefix(prompt, 49152); got != prompt {
			t.Fatalf("prefix = %q, want the prompt verbatim", got)
		}
	})

	t.Run("a huge prompt is cut in the middle with its own marker", func(t *testing.T) {
		prompt := "START OF THE REQUEST " + strings.Repeat("a long pasted log line. ", 4000) + " END OF THE REQUEST"
		const limit = 20000
		got := inTurnPromptPrefix(prompt, limit)
		if !strings.HasPrefix(got, "START OF THE REQUEST") || !strings.HasSuffix(got, "END OF THE REQUEST") {
			t.Fatalf("the ends of the request were lost: %q ... %q", got[:40], got[len(got)-40:])
		}
		if tokens, share := session.EstimateContextTokens(got), limit*inTurnPromptSharePercent/100; tokens > share {
			t.Fatalf("the prefix is %d tokens, over its share of %d", tokens, share)
		}
		if !strings.Contains(got, "omitted") || !strings.Contains(got, "request") {
			t.Fatalf("the cut says nothing about what went: %q", got)
		}
		if strings.Contains(got, "summarization request") {
			t.Fatalf("the cut reuses the summarizer's marker, which would misdescribe it: %q", got)
		}
	})

	t.Run("the share never drops below a few hundred tokens", func(t *testing.T) {
		prompt := strings.Repeat("word ", 600) // about 1000 tokens
		got := inTurnPromptPrefix(prompt, 1000)
		if tokens := session.EstimateContextTokens(got); tokens < inTurnPromptMinTokens-50 || tokens > inTurnPromptMinTokens+20 {
			t.Fatalf("prefix = %d tokens, want about the floor of %d", tokens, inTurnPromptMinTokens)
		}
	})
}

// The summarizer of a fold inside a turn is told the work is still going on and
// that the message the turn started with stays in front of its summary. The note
// names no author: a goal turn's instruction or a woken turn's notice is not the
// user's request, and the note must not call it so.
func TestInTurnSummaryInstructions(t *testing.T) {
	got := withInTurnSummaryInstructions("")
	for _, want := range []string{"middle of the agent's work", "stays verbatim in front of your summary", "what remains to be done"} {
		if !strings.Contains(got, want) {
			t.Fatalf("note %q does not say %q", got, want)
		}
	}
	if strings.Contains(got, "user's") {
		t.Fatalf("the note calls the opening message the user's: %q", got)
	}

	got = withInTurnSummaryInstructions("keep the file paths")
	if !strings.HasPrefix(got, "keep the file paths\n\n") || !strings.Contains(got, "stays verbatim") {
		t.Fatalf("the operator's instructions must come first, then the note: %q", got)
	}
}

// --- CompactSession with InTurn ------------------------------------------------

func TestCompactSessionInTurnFoldsEarlierStepsBehindThePrompt(t *testing.T) {
	const prompt = "Audit every module.\n\nReport each finding with its file."
	const total = inTurnKeepRecentSteps + 3
	st := inTurnSession(t, prompt, total)
	before := st.GetMessages()
	provider := &compactCannedProvider{t: t, summary: "the first steps read the first modules"}
	ag := inTurnAgent(t, st, config.Compaction{}, provider)

	res, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true})
	if err != nil {
		t.Fatal(err)
	}
	// The default window leaves the budget room for every step: the cap decides.
	if !res.InTurn || res.KeptSteps != inTurnKeepRecentSteps {
		t.Fatalf("result = %+v, want an in-turn fold keeping %d steps", res, inTurnKeepRecentSteps)
	}
	// Prompt + the folded steps (assistant + result each) are the head.
	if want := 1 + (total-inTurnKeepRecentSteps)*2; res.CompactedMessages != want || res.KeptMessages != inTurnKeepRecentSteps*2 {
		t.Fatalf("counts = %d folded / %d kept, want %d / %d", res.CompactedMessages, res.KeptMessages, want, inTurnKeepRecentSteps*2)
	}
	if !strings.Contains(compactionOutcomeText(res), "The request being answered starts the summary.") {
		t.Fatalf("the outcome does not say where the request went: %q", compactionOutcomeText(res))
	}

	after := st.GetMessages()
	if len(after) != len(before)+1 {
		t.Fatalf("a fold adds one row and removes none: %d messages, was %d", len(after), len(before))
	}
	window := session.MessagesForLLM(after)
	if !window[0].CompactionSummary || !strings.HasPrefix(window[0].Content, prompt+"\n\nThe text above is the message this turn started with, verbatim.") {
		t.Fatalf("the window must open with the prompt verbatim and the neutral preamble, got %q", firstChars(window[0].Content, 200))
	}
	if !strings.HasSuffix(window[0].Content, "Summary of the compacted part:\n\nthe first steps read the first modules") {
		t.Fatalf("the summary is missing from the end of the row: %q", window[0].Content)
	}
	if got := strings.Count(transcriptText(window), prompt); got != 1 {
		t.Fatalf("the window holds the prompt %d times, want once", got)
	}
	// The kept steps are the latest ones, byte for byte.
	for i, m := range window[1:] {
		want := before[len(before)-inTurnKeepRecentSteps*2+i]
		if m.Role != want.Role || m.Content != want.Content || m.ToolCallID != want.ToolCallID || len(m.ToolCalls) != len(want.ToolCalls) {
			t.Fatalf("kept message %d changed: %+v, was %+v", i, m, want)
		}
	}
	if issues := session.ValidateToolPairing(window); len(issues) > 0 {
		t.Fatalf("the window has a malformed tool-call history: %v", issues)
	}
	// The summarizer was told the transcript stops in the middle of the work.
	if len(provider.requests) != 1 {
		t.Fatalf("summarizer called %d times, want 1", len(provider.requests))
	}
	req := transcriptText(provider.requests[0])
	if !strings.Contains(req, "middle of") || !strings.Contains(req, "stays verbatim") {
		t.Fatalf("the summarizer was not told the work is still going on:\n%s", req)
	}
	if !strings.Contains(req, "Audit every module.") {
		t.Fatalf("the summarizer cannot see the request it is summarizing for:\n%s", req)
	}
}

// The budget keeps fewer steps than the cap when they would not fit: the fold
// aims the next request at half of the threshold's share of the window.
func TestCompactSessionInTurnKeepsFewerStepsThanTheCapWhenTheyDoNotFit(t *testing.T) {
	const prompt = "Audit every module and report."
	st := inTurnSession(t, prompt, 6)
	msgs := st.GetMessages()
	two, _ := session.TurnStepSplitIndex(msgs, 2)
	three, _ := session.TurnStepSplitIndex(msgs, 3)
	prefixTokens := session.EstimateContextTokens(session.NewInTurnCompactionSummaryMessage(prompt, "", "").Content)
	need := tailTokens(msgs, two) + prefixTokens
	if tailTokens(msgs, three)+prefixTokens <= need+1 {
		t.Fatal("fixture: a third step must not fit the room two leave")
	}
	// The smallest window whose target (threshold share, halved) holds the prompt
	// and two steps, and not three.
	perWindow := config.CompactionDefaultThresholdPercent * inTurnAimPercent
	window := (need*10000 + perWindow - 1) / perWindow

	provider := &compactCannedProvider{t: t, summary: "summary"}
	ag := inTurnAgent(t, st, config.Compaction{}, provider)
	ag.cfg.Models[0].MaxContextTokens = window

	res, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.InTurn || res.KeptSteps != 2 {
		t.Fatalf("result = %+v on a window of %d, want two steps kept: the budget holds two, the cap is %d", res, window, inTurnKeepRecentSteps)
	}
}

func TestCompactSessionInTurnNeedsAtLeastTwoSteps(t *testing.T) {
	for _, n := range []int{0, 1} {
		st := inTurnSession(t, "p", n)
		provider := &compactCannedProvider{t: t, summary: "unused"}
		ag := inTurnAgent(t, st, config.Compaction{}, provider)
		_, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true})
		if !errors.Is(err, ErrNothingToCompact) {
			t.Fatalf("%d step(s): err = %v, want ErrNothingToCompact", n, err)
		}
		if len(provider.requests) != 0 {
			t.Fatalf("%d step(s): the summarizer was called", n)
		}
	}
}

// Folding twice keeps one copy of the prompt: the second row's head holds the
// first row, whose prompt is not written again beside the new one.
func TestChainedInTurnFoldsKeepOnePromptCopy(t *testing.T) {
	const prompt = "UNIQUE-REQUEST-TEXT: audit every module and report."
	st := inTurnSession(t, prompt, 10)
	provider := &compactCannedProvider{t: t, summary: "SUMMARY-OF-EARLIER-WORK"}
	ag := inTurnAgent(t, st, config.Compaction{}, provider)

	if _, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true}); err != nil {
		t.Fatal(err)
	}
	// The turn goes on: three more steps, then a second fold.
	for _, m := range steps("later", 3) {
		st.AddMessage(m)
	}
	if _, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true}); err != nil {
		t.Fatal(err)
	}

	window := windowOf(st)
	if !window[0].CompactionSummary || !strings.HasPrefix(window[0].Content, prompt+"\n\n") {
		t.Fatalf("the window must open with the prompt: %q", firstChars(window[0].Content, 120))
	}
	if got := strings.Count(transcriptText(window), prompt); got != 1 {
		t.Fatalf("the window holds the prompt %d times after two folds, want once", got)
	}
	if rows := summaryRowsOf(st); rows != 2 {
		t.Fatalf("the transcript holds %d summary rows, want 2 (the first stays for the UI)", rows)
	}
	if issues := session.ValidateToolPairing(window); len(issues) > 0 {
		t.Fatalf("malformed tool-call history after two folds: %v", issues)
	}
	// The second summarization request saw the first row: that is where the
	// earlier work is.
	if len(provider.requests) != 2 || !strings.Contains(transcriptText(provider.requests[1]), "SUMMARY-OF-EARLIER-WORK") {
		t.Fatalf("the second fold did not summarize the first row: %d requests", len(provider.requests))
	}
}

func TestCompactSessionInTurnCapsAHugePromptAtItsShare(t *testing.T) {
	prompt := "HEAD-OF-REQUEST " + strings.Repeat("pasted log line that goes on. ", 6000) + " TAIL-OF-REQUEST"
	st := inTurnSession(t, prompt, 4)
	provider := &compactCannedProvider{t: t, summary: "summary"}
	ag := inTurnAgent(t, st, config.Compaction{}, provider)

	if _, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true}); err != nil {
		t.Fatal(err)
	}
	row := windowOf(st)[0]
	if !strings.HasPrefix(row.Content, "HEAD-OF-REQUEST") || !strings.Contains(row.Content, "TAIL-OF-REQUEST") {
		t.Fatalf("the row lost an end of the request: %q ... %q", firstChars(row.Content, 60), row.Content[len(row.Content)-120:])
	}
	if !strings.Contains(row.Content, "omitted from the middle of this request") {
		t.Fatalf("the cut does not say what it is: %q", firstChars(row.Content, 200))
	}
	window, _ := ag.contextWindow()
	if got, max := session.EstimateContextTokens(row.Content), window*inTurnPromptSharePercent/100+600; got > max {
		t.Fatalf("the row is %d tokens, the request alone must stay within its %d%% share (%d)", got, inTurnPromptSharePercent, max)
	}
	// The original message in the transcript is whole.
	if st.GetMessages()[0].Content != prompt {
		t.Fatal("the stored prompt was changed")
	}
}

// A history whose opening message carries no text (one that starts mid-turn)
// gets a plain summary row.
func TestCompactSessionInTurnWithoutAPromptWritesAPlainRow(t *testing.T) {
	st := stateOf(t, []llm.Message{userText("   ")}, steps("s", 4))
	provider := &compactCannedProvider{t: t, summary: "summary"}
	ag := inTurnAgent(t, st, config.Compaction{}, provider)

	if _, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true}); err != nil {
		t.Fatal(err)
	}
	row := windowOf(st)[0]
	if !strings.HasPrefix(row.Content, "The earlier conversation was compacted.") {
		t.Fatalf("a history with no prompt must get the regular row, got %q", row.Content)
	}
}

// --- the plain row: /compact between turns and compact_context ----------------

// A /compact between turns is no turn being answered: the last prompt folded with
// the rest, in front of a plain row, would ask the model to answer it again.
func TestManualCompactBetweenTurnsWritesAPlainRow(t *testing.T) {
	st := seededCompactState(t, 3)
	zero := 0
	provider := &compactCannedProvider{t: t, summary: "all three exchanges"}
	ag := inTurnAgent(t, st, config.Compaction{KeepRecentTurns: &zero}, provider)

	res, err := ag.CompactSession(context.Background(), CompactOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.InTurn {
		t.Fatalf("result = %+v: a manual compaction between turns is not an in-turn fold", res)
	}
	row := windowOf(st)[0]
	if !strings.HasPrefix(row.Content, "The earlier conversation was compacted.") || strings.Contains(row.Content, "question 3") {
		t.Fatalf("a /compact must write the plain row, got %q", row.Content)
	}
	if strings.Contains(transcriptText(provider.requests[0]), "stays verbatim") {
		t.Fatal("the summarizer was told a request stays in front of its summary")
	}
}

// A compact_context call is a compaction the model asked for, and it writes the
// plain row whatever its head holds: the prompt being answered, when the head
// holds it, is summarized with the rest. Only the in-turn fold puts the prompt
// in front of the summary.
func TestCompactContextWritesThePlainRow(t *testing.T) {
	const prompt = "Audit every module.\n\nReport each finding with its file."
	st := stateOf(t, []llm.Message{userText(prompt)}, steps("s", 3), []llm.Message{compactContextCall()})
	provider := &compactCannedProvider{t: t, summary: "the first three reads"}
	ag := inTurnAgent(t, st, config.Compaction{}, provider)

	res, err := ag.CompactSession(context.Background(), CompactOptions{Force: true, FromTool: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.InTurn || res.KeptSteps != 0 {
		t.Fatalf("result = %+v: a compact_context call is no in-turn fold", res)
	}
	// The call in flight stays; everything before it is the head.
	if res.CompactedMessages != 1+3*2 || res.KeptMessages != 1 {
		t.Fatalf("counts = %d folded / %d kept, want 7 / 1", res.CompactedMessages, res.KeptMessages)
	}
	if got := compactionOutcomeText(res); !strings.HasPrefix(got, "Context compacted: 7 message(s) summarized, 1 kept verbatim.") {
		t.Fatalf("the tool result = %q, want the plain outcome", got)
	}
	window := windowOf(st)
	if len(window) != 2 || !window[0].CompactionSummary || len(window[1].ToolCalls) != 1 {
		t.Fatalf("the window must be the row and the compact_context call, got %s", transcriptText(window))
	}
	if !strings.HasPrefix(window[0].Content, "The earlier conversation was compacted. Summary of the compacted part:\n\nthe first three reads") {
		t.Fatalf("a compact_context call must write the plain row, got %q", firstChars(window[0].Content, 160))
	}
	if strings.Contains(window[0].Content, prompt) {
		t.Fatalf("the plain row carries the prompt: %q", window[0].Content)
	}
	req := transcriptText(provider.requests[0])
	if strings.Contains(req, "stays verbatim") {
		t.Fatalf("the summarizer was told a request stays in front of its summary:\n%s", req)
	}
	if !strings.Contains(req, "Audit every module.") {
		t.Fatalf("the prompt the head holds was kept from the summarizer:\n%s", req)
	}
	if issues := session.ValidateToolPairing(append(window, llm.Message{Role: llm.RoleTool, ToolCallID: "call_compact", Content: "ok"})); len(issues) > 0 {
		t.Fatalf("malformed tool-call history: %v", issues)
	}
}

// When the opening prompt is in the kept tail it stays an ordinary message and
// the row has nothing in front of it.
func TestCompactContextKeepingThePromptWritesAPlainRow(t *testing.T) {
	const prompt = "Audit every module."
	st := stateOf(t, seededCompactState(t, 3).GetMessages(), []llm.Message{userText(prompt)}, steps("s", 2), []llm.Message{compactContextCall()})
	provider := &compactCannedProvider{t: t, summary: "the earlier exchanges"}
	ag := inTurnAgent(t, st, config.Compaction{}, provider)

	res, err := ag.CompactSession(context.Background(), CompactOptions{Force: true, FromTool: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.InTurn {
		t.Fatalf("result = %+v: the prompt was in the kept tail", res)
	}
	window := windowOf(st)
	if !strings.HasPrefix(window[0].Content, "The earlier conversation was compacted.") {
		t.Fatalf("want the plain row, got %q", firstChars(window[0].Content, 100))
	}
	if got := strings.Count(transcriptText(window), prompt); got != 1 {
		t.Fatalf("the window holds the prompt %d times, want once, as an ordinary message", got)
	}
}

// --- follow-ups and the opening prompt ----------------------------------------

const followUpPrompt = "Audit every module.\n\nReport each finding with its file."

// followUpTurn is a turn that went on through a queued follow-up, a Stop-hook
// follow-up and a second queued follow-up that falls into the kept tail:
//
//	0 opening, 1-4 two steps, 5 queued, 6-9 two steps, 10 Stop hook, 11-14 two steps,
//	15-16 a step, 17 queued (tail), 18-19 a step
//
// marked says whether the queued ones carry the FollowUp marker, as they do since
// readQueuedMessages sets it; a transcript written before the marker existed has
// none.
func followUpTurn(marked bool) []llm.Message {
	queued := func(text string) llm.Message {
		m := userText(text)
		m.FollowUp = marked
		return m
	}
	return concatMsgs(
		[]llm.Message{userText(followUpPrompt)}, steps("a", 2),
		[]llm.Message{queued("Also keep the tests green.")}, steps("b", 2),
		[]llm.Message{userText(stopHookPrefix + "run the linter before you finish")}, steps("c", 2),
		steps("d", 1),
		[]llm.Message{queued("And leave the docs alone.")}, steps("e", 1),
	)
}

// A follow-up in the folded head is summarized like any step: the row carries
// the opening prompt only, and the follow-up in the kept tail stays where it was.
func TestInTurnFoldSummarizesTheFollowUpsOfItsHead(t *testing.T) {
	st := stateOf(t, followUpTurn(true))
	provider := &compactCannedProvider{t: t, summary: "audited the first modules"}
	ag := inTurnAgent(t, st, config.Compaction{}, provider)

	res, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true})
	if err != nil {
		t.Fatal(err)
	}
	// Eight steps after the opening prompt, the follow-ups among them: the turn is
	// measured from the prompt, not from the last follow-up.
	if !res.InTurn || res.KeptSteps != inTurnKeepRecentSteps || res.CompactedMessages != 11 {
		t.Fatalf("result = %+v, want an in-turn fold of the first 11 messages keeping %d steps", res, inTurnKeepRecentSteps)
	}
	window := windowOf(st)
	row := window[0]
	if !row.CompactionSummary || !strings.HasPrefix(row.Content, followUpPrompt+"\n\nThe text above is") {
		t.Fatalf("the row must begin with the opening prompt and then the preamble, got %q", firstChars(row.Content, 220))
	}
	for _, folded := range []string{"Also keep the tests green.", "run the linter"} {
		if strings.Contains(row.Content, folded) {
			t.Fatalf("a follow-up of the head was carried verbatim in the row instead of summarized: %q", row.Content)
		}
		if !strings.Contains(transcriptText(provider.requests[0]), folded) {
			t.Fatalf("the follow-up %q was kept out of the summarization request", folded)
		}
	}
	// The one in the tail stays where it was, as an ordinary message, once.
	if got := strings.Count(transcriptText(window), "And leave the docs alone."); got != 1 {
		t.Fatalf("the window holds the tail's follow-up %d times, want once", got)
	}
	if issues := session.ValidateToolPairing(window); len(issues) > 0 {
		t.Fatalf("malformed tool-call history: %v", issues)
	}
}

// The prompt and a queued follow-up with the same words: the marker, not the
// text, tells them apart, so the fold sees a turn of five steps and not of two.
func TestInTurnFoldFindsThePromptWhenAFollowUpRepeatsIt(t *testing.T) {
	st := stateOf(t, []llm.Message{userText("continue")}, steps("a", 3), []llm.Message{queuedFollowUp("continue")}, steps("b", 2))
	provider := &compactCannedProvider{t: t, summary: "the first step"}
	ag := inTurnAgent(t, st, config.Compaction{}, provider)

	res, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true})
	if err != nil {
		t.Fatalf("the turn has five steps and the fold keeps four: %v", err)
	}
	if !res.InTurn || res.KeptSteps != inTurnKeepRecentSteps || res.CompactedMessages != 3 {
		t.Fatalf("result = %+v, want the prompt and its first step folded, %d steps kept", res, inTurnKeepRecentSteps)
	}
	window := windowOf(st)
	if !strings.HasPrefix(window[0].Content, "continue\n\nThe text above is") {
		t.Fatalf("the row must begin with the prompt, got %q", firstChars(window[0].Content, 120))
	}
	followUps := 0
	for _, m := range window[1:] {
		if m.Role == llm.RoleUser && m.FollowUp && m.Content == "continue" {
			followUps++
		}
	}
	if followUps != 1 {
		t.Fatalf("the follow-up must stay in the kept tail as an ordinary message, found %d", followUps)
	}
}

// The marker travels with the transcript: a turn resumed after a restart reads
// its messages back from disk, without anything the process that started the
// turn held in memory, and a fresh Agent over them still finds the opening
// prompt rather than the follow-up queued after it.
func TestOpeningPromptSurvivesARestart(t *testing.T) {
	const prompt = "Audit every module and report."
	msgs := fixtureTurn(prompt, 3, 1, 600)
	msgs = append(msgs, queuedFollowUp("also check the tests"))
	for i := 0; i < 3; i++ {
		msgs = append(msgs, fixtureStep(fmt.Sprintf("t%d", i), 1, 600)...)
	}
	// What the session bundle stores and what a new process reads back.
	raw, err := json.Marshal(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"follow_up":true`) {
		t.Fatalf("the marker is not written with the message: %s", firstChars(string(raw), 400))
	}
	var loaded []llm.Message
	if err := json.Unmarshal(raw, &loaded); err != nil {
		t.Fatal(err)
	}
	st := stateOf(t, loaded)
	provider := &compactCannedProvider{t: t, summary: "the first modules were audited"}
	ag := inTurnAgent(t, st, config.Compaction{}, provider)

	res, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.InTurn || res.KeptSteps != inTurnKeepRecentSteps {
		t.Fatalf("result = %+v, want an in-turn fold over the six steps since the prompt, keeping %d", res, inTurnKeepRecentSteps)
	}
	row := windowOf(st)[0]
	if !strings.HasPrefix(row.Content, prompt+"\n\nThe text above is") {
		t.Fatalf("the row must begin with the opening prompt, not the follow-up: %q", firstChars(row.Content, 160))
	}
	if strings.HasPrefix(row.Content, "also check the tests") {
		t.Fatalf("the follow-up was taken for the opening prompt: %q", firstChars(row.Content, 160))
	}
}

// Without the marker - a transcript written before it existed - the opening
// prompt is the last user message that is not a Stop-hook follow-up.
func TestInTurnPrefixFallsBackToTheLastUserMessageWithoutMarkers(t *testing.T) {
	st := stateOf(t, followUpTurn(false))
	ag := inTurnAgent(t, st, config.Compaction{}, &compactCannedProvider{t: t, summary: "unused"})
	msgs := st.GetMessages()
	prefix, ok := ag.inTurnPrefix(msgs, len(msgs), 128000)
	if !ok || prefix != "And leave the docs alone." {
		t.Fatalf("prefix = %q ok = %v, want the last unmarked user message standing in for the prompt", firstChars(prefix, 80), ok)
	}

	marked := stateOf(t, followUpTurn(true)).GetMessages()
	prefix, ok = ag.inTurnPrefix(marked, len(marked), 128000)
	if !ok || prefix != followUpPrompt {
		t.Fatalf("prefix = %q ok = %v, want the opening prompt once the follow-ups are marked", firstChars(prefix, 80), ok)
	}
}

// A Stop hook's follow-up is hook output, not the request: the opening prompt
// is looked for past it.
func TestInTurnFoldSkipsStopHookFollowUpsWhenLookingForThePrompt(t *testing.T) {
	turn := concatMsgs(
		[]llm.Message{userText(followUpPrompt)}, steps("a", 3),
		[]llm.Message{userText(stopHookPrefix + "run the linter")}, steps("b", 3),
	)
	st := stateOf(t, turn)
	provider := &compactCannedProvider{t: t, summary: "summary"}
	ag := inTurnAgent(t, st, config.Compaction{}, provider)

	if _, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true}); err != nil {
		t.Fatal(err)
	}
	row := windowOf(st)[0]
	if !strings.HasPrefix(row.Content, followUpPrompt+"\n\nThe text above is") {
		t.Fatalf("the row must begin with the prompt, not the Stop-hook follow-up: %q", firstChars(row.Content, 160))
	}
	if strings.Contains(row.Content, "run the linter") {
		t.Fatalf("the Stop-hook follow-up leaked into the prefix: %q", row.Content)
	}
}

// readQueuedMessages is what marks a follow-up: every message the user queued
// while the turn ran enters the conversation, and the transcript, with the marker.
func TestReadQueuedMessagesMarksEveryMessageAsAFollowUp(t *testing.T) {
	st := stateOf(t, []llm.Message{userText(followUpPrompt)}, steps("a", 1))
	st.OpenMessageQueue()
	for _, text := range []string{"also check the tests", "and the docs"} {
		if _, err := st.EnqueueMessage(text); err != nil {
			t.Fatal(err)
		}
	}
	ag := inTurnAgent(t, st, config.Compaction{}, &compactCannedProvider{t: t, summary: "unused"})

	var outgoing []llm.Message
	if !ag.readQueuedMessages(&outgoing) {
		t.Fatal("the queued messages were not read")
	}
	if len(outgoing) != 2 {
		t.Fatalf("%d messages appended, want 2", len(outgoing))
	}
	for i, m := range outgoing {
		if m.Role != llm.RoleUser || !m.FollowUp {
			t.Fatalf("appended message %d = %+v, want a user message marked as a follow-up", i, m)
		}
	}
	stored := st.GetMessages()
	for _, m := range stored[len(stored)-2:] {
		if !m.FollowUp {
			t.Fatalf("the transcript stored a queued message without the marker: %+v", m)
		}
	}
	if open, ok := session.OpeningPromptIndex(stored); !ok || stored[open].Content != followUpPrompt {
		t.Fatalf("the opening prompt is no longer found after the follow-ups were read: %d %v", open, ok)
	}
}

// Only the user messages up to the opening prompt are turn boundaries for the
// automatic split: folding the turns before the prompt leaves it, and the
// follow-ups, as they were.
func TestAutomaticSplitDoesNotCutAtAFollowUpOfTheTurnInProgress(t *testing.T) {
	earlier := seededCompactState(t, 2).GetMessages()
	turn := concatMsgs([]llm.Message{userText(followUpPrompt)}, steps("a", 2), []llm.Message{queuedFollowUp("Also keep the tests green.")}, steps("b", 2))
	st := stateOf(t, earlier, turn)
	keep := 1
	provider := &compactCannedProvider{t: t, summary: "the two earlier exchanges"}
	ag := inTurnAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)

	res, err := ag.CompactSession(context.Background(), CompactOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if res.CompactedMessages != len(earlier) || res.KeptMessages != len(turn) {
		t.Fatalf("counts = %d folded / %d kept, want %d / %d: only the earlier exchanges fold", res.CompactedMessages, res.KeptMessages, len(earlier), len(turn))
	}
	window := windowOf(st)
	if !window[0].CompactionSummary || window[1].Content != followUpPrompt {
		t.Fatalf("the prompt must follow the row as an ordinary message, got %s", transcriptText(window[:2]))
	}
}

// A turn that is one prompt, its steps and a follow-up has nothing the regular
// split may fold: the follow-up is not a turn that ended before the prompt.
func TestAutomaticSplitOfAOneTurnSessionWithAFollowUpHasNothingToFold(t *testing.T) {
	turn := concatMsgs([]llm.Message{userText(followUpPrompt)}, steps("a", 2), []llm.Message{queuedFollowUp("Also keep the tests green.")}, steps("b", 2))
	st := stateOf(t, turn)
	provider := &compactCannedProvider{t: t, summary: "must not be asked"}
	ag := inTurnAgent(t, st, config.Compaction{}, provider)

	_, err := ag.CompactSession(context.Background(), CompactOptions{})
	if !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("err = %v, want ErrNothingToCompact: the prompt would have been folded into a plain summary", err)
	}
	if len(provider.requests) != 0 {
		t.Fatal("the summarizer was called")
	}
}

// A regular compaction of the turns before the prompt puts a row in front of it,
// and the next fold of the turn still finds the prompt, not the follow-up that
// came after it.
func TestOpeningPromptIsFoundAfterARegularCompactionBeforeIt(t *testing.T) {
	earlier := seededCompactState(t, 2).GetMessages()
	turn := concatMsgs([]llm.Message{userText(followUpPrompt)}, steps("a", 3), []llm.Message{queuedFollowUp("Also keep the tests green.")}, steps("b", 3))
	st := stateOf(t, earlier, turn)
	keep := 1
	provider := &compactCannedProvider{t: t, summary: "summary"}
	ag := inTurnAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)

	if _, err := ag.CompactSession(context.Background(), CompactOptions{}); err != nil {
		t.Fatal(err)
	}
	res, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.InTurn || res.KeptSteps != inTurnKeepRecentSteps {
		t.Fatalf("result = %+v, want an in-turn fold over the six steps since the prompt", res)
	}
	row := windowOf(st)[0]
	if !strings.HasPrefix(row.Content, followUpPrompt+"\n\nThe text above is") {
		t.Fatalf("the row must begin with the opening prompt, got %q", firstChars(row.Content, 200))
	}
}

// --- PreCompact hooks run only for a compaction that has something to fold ---

func hookedCompactAgent(t *testing.T, st *session.State, provider llm.Provider, record string) *Agent {
	t.Helper()
	home := t.TempDir()
	if err := hooktest.Write(filepath.Join(home, "hooks.json"),
		hooktest.Entry{Event: "PreCompact", Handlers: []hooks.Handler{hooktest.Handler("record", record)}}); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Paths:     config.Paths{Home: home, CWD: st.CWD, ConfigPath: filepath.Join(home, "config.yaml")},
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	cfg.Hooks.ApplyDefaults(cfg.Paths)
	cfg.Subagents.ApplyDefaults(cfg.Paths)
	cfg.Prompts.ApplyDefaults()
	ag := NewAgent(cfg, st, resumePermissionSender{}, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
	return ag
}

func TestPreCompactHooksDoNotRunWhenThereIsNothingToFold(t *testing.T) {
	cases := []struct {
		name string
		opts CompactOptions
		st   func(t *testing.T) *session.State
	}{
		{name: "the automatic trigger with one turn in the window", opts: CompactOptions{},
			st: func(t *testing.T) *session.State { return inTurnSession(t, "p", 3) }},
		{name: "the in-turn fold with one step", opts: CompactOptions{InTurn: true},
			st: func(t *testing.T) *session.State { return inTurnSession(t, "p", 1) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			record := filepath.Join(t.TempDir(), "pre-compact.json")
			provider := &compactCannedProvider{t: t, summary: "unused"}
			ag := hookedCompactAgent(t, tc.st(t), provider, record)

			_, err := ag.CompactSession(context.Background(), tc.opts)
			if !errors.Is(err, ErrNothingToCompact) {
				t.Fatalf("err = %v, want ErrNothingToCompact", err)
			}
			if _, statErr := os.Stat(record); statErr == nil {
				t.Fatal("a PreCompact hook ran for a compaction that had nothing to fold")
			}
		})
	}

	t.Run("a compaction that folds runs them once, as an automatic one", func(t *testing.T) {
		record := filepath.Join(t.TempDir(), "pre-compact.json")
		provider := &compactCannedProvider{t: t, summary: "summary"}
		ag := hookedCompactAgent(t, inTurnSession(t, "p", 4), provider, record)

		if _, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true}); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(record)
		if err != nil {
			t.Fatalf("the PreCompact hook did not run: %v", err)
		}
		if !strings.Contains(string(raw), `"trigger":"auto"`) && !strings.Contains(string(raw), `"trigger": "auto"`) {
			t.Fatalf("the in-turn fold must reach hooks as an automatic compaction: %s", raw)
		}
	})
}

// --- the automatic trigger -------------------------------------------------------

func TestMaybeAutoCompactFoldsTheTurnInProgressWhenNoEarlierTurnExists(t *testing.T) {
	const prompt = "Audit every module and report."
	st := inTurnSession(t, prompt, 6)
	provider := &compactCannedProvider{t: t, summary: "the first modules were audited"}
	ag, logs := overThresholdAgent(t, st, config.Compaction{}, provider)

	if !ag.maybeAutoCompact(context.Background()) {
		t.Fatalf("a turn over the threshold with six steps must be folded:\n%s", logs)
	}
	window := windowOf(st)
	if !window[0].CompactionSummary || !strings.HasPrefix(window[0].Content, prompt) {
		t.Fatalf("the window must open with the prompt: %q", window[0].Content)
	}
	// The overhead (7000 of the 10000 tokens) leaves no room under the aim, so
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
	if ag.inTurnFoldSpent {
		t.Fatalf("a fold that brought the request under the threshold spent the turn's folds:\n%s", logs)
	}
}

// Earlier turns are what the regular compaction folds; the in-turn fold is only
// the fallback when there are none.
func TestMaybeAutoCompactStillFoldsEarlierTurnsFirst(t *testing.T) {
	st := stateOf(t, seededCompactState(t, 3).GetMessages(), fixtureTurn("current prompt", 4, 1, 600))
	keep := 1
	provider := &compactCannedProvider{t: t, summary: "the earlier turns"}
	ag, logs := overThresholdAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)

	if !ag.maybeAutoCompact(context.Background()) {
		t.Fatalf("did not compact:\n%s", logs)
	}
	row := windowOf(st)[0]
	if !strings.HasPrefix(row.Content, "The earlier conversation was compacted.") {
		t.Fatalf("an earlier turn existed, so the regular row was due: %q", firstChars(row.Content, 80))
	}
	if strings.Contains(logs.String(), "auto-compacted the turn in progress") {
		t.Fatalf("the turn in progress was folded although earlier turns could be:\n%s", logs)
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

// The trigger measures the context by the provider's own count, and the budget
// of the fold is in Coddy's estimate. A provider that counts denser than the
// estimate must not see a kept tail that lands over the threshold again: the
// budget is divided by the provider's scale, so the next request, as the
// provider counts it, is under the trigger and the next step does not fold.
func TestAutomaticInTurnFoldBudgetIsScaledByTheProvidersCount(t *testing.T) {
	off := false
	comp := config.Compaction{ResultEviction: config.ResultEviction{Enabled: &off}}
	st := stateOf(t, fixtureTurn("Audit every module and report.", 8, 1, 3900))
	provider := &compactCannedProvider{t: t, summary: "the first modules were audited"}
	const window, overhead = 16000, 1500
	conv := conversationTokens(windowOf(st), false)
	ag, logs := windowAgent(t, st, comp, provider, window, overhead, conv)
	// The provider counted the last request at two and a half of its tokens for
	// each one Coddy estimated.
	const scale = 2.5
	b := st.GetLastContextBreakdown()
	b.ProviderInputTokens, b.ProviderEstimateTokens = int(scale*float64(overhead+conv)), overhead+conv
	st.SetLastContextBreakdown(b)
	threshold := window * config.CompactionDefaultThresholdPercent / 100

	if !ag.maybeAutoCompact(context.Background()) {
		t.Fatalf("the turn is over the threshold by the provider's count and must be folded:\n%s", logs)
	}
	// The next request as the provider will count it.
	next := int(scale * float64(ag.inTurnRequestTokens(1)))
	if next >= threshold {
		t.Fatalf("the fold left the next request at %d provider tokens, over the threshold of %d:\n%s", next, threshold, logs)
	}
	if ag.inTurnFoldSpent {
		t.Fatalf("the fold kept the request under the threshold, yet spent the turn's folds:\n%s", logs)
	}

	// The provider's count of that smaller request comes back, and nothing new
	// was added: the trigger has nothing to do.
	after := ag.inTurnRequestTokens(1)
	b = st.GetLastContextBreakdown()
	b.ProviderInputTokens, b.ProviderEstimateTokens = int(scale*float64(after)), after
	st.SetLastContextBreakdown(b)
	if ag.maybeAutoCompact(context.Background()) {
		t.Fatalf("the step after the fold folded again:\n%s", logs)
	}
	if len(provider.requests) != 1 {
		t.Fatalf("the summarizer was asked %d times, want once", len(provider.requests))
	}
}

// A fold that cannot bring the request under the threshold - what every request
// carries besides the conversation is too large for the window - spends the
// automatic folds of the turn: each further one would be a summarizer call per
// step for nothing. The recovery from a request the provider refused is not
// affected by it.
func TestInTurnFoldSpentStopsFurtherAutomaticFoldsButNotTheRecovery(t *testing.T) {
	st := inTurnSession(t, "Audit every module and report.", 6)
	provider := &compactCannedProvider{t: t, summary: "the first modules were audited"}
	// A window of 16000 at 80% triggers at 12800, and 13000 tokens of overhead
	// alone are over it.
	ag, logs := windowAgent(t, st, config.Compaction{}, provider, 16000, 13000, conversationTokens(windowOf(st), false))

	if !ag.maybeAutoCompact(context.Background()) {
		t.Fatalf("the first fold must run:\n%s", logs)
	}
	if !ag.inTurnFoldSpent {
		t.Fatalf("the fold left the request over the threshold and did not spend the turn's folds:\n%s", logs)
	}
	if !strings.Contains(logs.String(), "in-turn compaction left the request over the threshold; no more automatic folds this turn") {
		t.Fatalf("the spent folds are not logged:\n%s", logs)
	}

	// The turn goes on: two more steps, each over the threshold, each with an
	// earlier step that could be folded.
	for i := 0; i < 2; i++ {
		for _, m := range steps(fmt.Sprintf("later%d_", i), 1) {
			st.AddMessage(m)
		}
		if ag.maybeAutoCompact(context.Background()) {
			t.Fatalf("step %d after the spent fold folded again:\n%s", i+1, logs)
		}
	}
	if len(provider.requests) != 1 {
		t.Fatalf("the summarizer was asked %d times, want only the first fold", len(provider.requests))
	}
	if got := strings.Count(logs.String(), "auto-compaction skipped"); got != 1 {
		t.Fatalf("the skip was logged %d times, want once:\n%s", got, logs)
	}

	// A refused request still compacts the turn: the guard is the trigger's.
	if !ag.recoverFromContextOverflow(context.Background(), errRecoveryRefusal, 15000) {
		t.Fatalf("the recovery from a refused request did not run:\n%s", logs)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("the summarizer was asked %d times, want the recovery's call too", len(provider.requests))
	}
	if !strings.HasPrefix(windowOf(st)[0].Content, "Audit every module and report.\n\n") {
		t.Fatalf("the recovery's row must begin with the prompt: %q", firstChars(windowOf(st)[0].Content, 120))
	}
}

// --- the rules a row's prompt carries ------------------------------------------

const ruleBlockPath = "/work/.cursor/rules/go.mdc"

func ruleBlock(path, body string) string {
	return mention.Attachment{Path: path, Kind: mention.KindRule, Body: body}.XML()
}

// A rule a mention attached to the turn's prompt rides in the prompt's content,
// the in-turn row carries the prompt, and the bookkeeping of which rules the
// model can read counts them.
func TestDeliveredRulesCountsTheRulesInFrontOfAnInTurnSummary(t *testing.T) {
	const body = "Use gofmt before every commit."
	prompt := "Fix the build.\n\n" + ruleBlock(ruleBlockPath, body)

	t.Run("the rules of the prompt a row carries are delivered", func(t *testing.T) {
		msgs := []llm.Message{
			session.NewInTurnCompactionSummaryMessage(prompt, "the worker read two files", "m"),
			{Role: llm.RoleAssistant, Content: "ok"},
		}
		if got := deliveredRules(msgs); got[ruleBlockPath] != body {
			t.Fatalf("delivered = %v, want the rule the prompt carried with its text", got)
		}
	})

	t.Run("a rule in the summary part is not the rule", func(t *testing.T) {
		msgs := []llm.Message{session.NewInTurnCompactionSummaryMessage("Fix the build.", "the worker met\n\n"+ruleBlock(ruleBlockPath, body), "m")}
		if got := deliveredRules(msgs); len(got) != 0 {
			t.Fatalf("delivered = %v: whatever a summary retells is not the rule", got)
		}
	})

	t.Run("a plain summary row counts nothing", func(t *testing.T) {
		msgs := []llm.Message{session.NewCompactionSummaryMessage(prompt, "m")}
		if got := deliveredRules(msgs); len(got) != 0 {
			t.Fatalf("delivered = %v from a plain row", got)
		}
	})
}

// The cap on the prefix can cut a block in half. A block cut off at its end is
// not a block at all, and one whose two ends survived around the cut carries the
// marker in its body, so neither is the rule's text and the next match attaches
// the rule whole.
func TestDeliveredRulesDoesNotCountABlockTheCapCutThrough(t *testing.T) {
	body := strings.Repeat("Use gofmt before every commit. ", 3000)
	const limit = 20000

	cases := map[string]string{
		"the end of the block cut off": "Fix the build.\n\n" + ruleBlock(ruleBlockPath, body) + strings.Repeat("x", 60000),
		"the middle of the block cut":  "Fix the build.\n\n" + ruleBlock(ruleBlockPath, body) + "\n\nand then explain why.",
	}
	for name, prompt := range cases {
		t.Run(name, func(t *testing.T) {
			prefix := inTurnPromptPrefix(prompt, limit)
			if !strings.Contains(prefix, "omitted") {
				t.Fatal("fixture: the prefix was not cut")
			}
			msgs := []llm.Message{session.NewInTurnCompactionSummaryMessage(prefix, "s", "m")}
			if got := deliveredRules(msgs); got[ruleBlockPath] == body {
				t.Fatal("a block the cap cut through was counted as the rule")
			}
		})
	}

	t.Run("a block the cap left whole is counted", func(t *testing.T) {
		const small = "Use gofmt."
		prompt := "Fix the build.\n\n" + ruleBlock(ruleBlockPath, small) + "\n\n" + strings.Repeat("pasted log line. ", 5000)
		prefix := inTurnPromptPrefix(prompt, limit)
		msgs := []llm.Message{session.NewInTurnCompactionSummaryMessage(prefix, "s", "m")}
		if got := deliveredRules(msgs); got[ruleBlockPath] != small {
			t.Fatalf("delivered = %v: the block was whole and in the kept head", got)
		}
	})
}
