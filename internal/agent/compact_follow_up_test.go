package agent

// The summary row of a compaction made while a turn is in progress: what a
// compact_context call writes, and how the follow-ups the user sent during the
// turn are carried and never mistaken for its prompt (issue #490). The happy path
// is in features/context_in_turn_compaction.feature and
// features/context_compaction.feature.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

const (
	followUpPrompt = "Audit every module.\n\nReport each finding with its file."
	openedAt       = "2026-10-11T10:00:00Z"
	queuedAt       = "2026-10-11T10:03:00Z"
	hookedAt       = "2026-10-11T10:05:00Z"
	laterQueuedAt  = "2026-10-11T10:08:00Z"
)

// stamped is a user message written at a given time, the way the agent stamps one.
func stamped(text, createdAt string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Content: text, CreatedAt: createdAt}
}

// steps is n steps of one read each, ids made of prefix.
func steps(prefix string, n int) []llm.Message {
	var out []llm.Message
	for i := 0; i < n; i++ {
		out = append(out, fixtureStep(fmt.Sprintf("%s%d", prefix, i), 1, 600)...)
	}
	return out
}

// compactContextCall is the assistant message of a compact_context call that is
// being executed: its result is appended after the compaction returns.
func compactContextCall() llm.Message {
	return llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_compact", Name: "compact_context", InputJSON: "{}"}}}
}

func stateOf(t *testing.T, msgs ...[]llm.Message) *session.State {
	t.Helper()
	st := &session.State{ID: "sess_follow_up_unit", CWD: t.TempDir(), Mode: session.ModeAgent}
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

// --- compact_context while a turn is in progress -----------------------------

// A compact_context call after an in-turn fold folds the row the fold wrote:
// the row it writes begins with the prompt again, once.
func TestCompactContextAfterAnInTurnFoldKeepsOneCopyOfThePrompt(t *testing.T) {
	st := inTurnSession(t, followUpPrompt, 10)
	two := 2
	provider := &compactCannedProvider{t: t, summary: "SUMMARY-OF-EARLIER-WORK"}
	ag := inTurnAgent(t, st, config.Compaction{InTurn: config.InTurn{KeepRecentSteps: &two}}, provider)
	if _, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true}); err != nil {
		t.Fatal(err)
	}
	for _, m := range append(steps("later", 3), compactContextCall()) {
		st.AddMessage(m)
	}

	res, err := ag.CompactSession(context.Background(), CompactOptions{Force: true, FromTool: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.InTurn {
		t.Fatalf("result = %+v, want a fold that kept the prompt in front", res)
	}
	window := windowOf(st)
	if len(window) != 2 || !window[0].CompactionSummary || window[1].Role != llm.RoleAssistant || len(window[1].ToolCalls) != 1 {
		t.Fatalf("the window must be the new row and the compact_context call, got %d messages: %s", len(window), transcriptText(window))
	}
	if !strings.HasPrefix(window[0].Content, followUpPrompt+"\n\n") {
		t.Fatalf("the new row must begin with the prompt verbatim, got %q", firstChars(window[0].Content, 120))
	}
	if got := strings.Count(window[0].Content, followUpPrompt); got != 1 {
		t.Fatalf("the new row holds the prompt %d times, want once", got)
	}
	if got := summaryRowsOf(st); got != 2 {
		t.Fatalf("the transcript holds %d summary rows, want 2 (the fold's stays for the UI)", got)
	}
	// The summarizer was told the request stays in front of its summary, and it
	// read the earlier row: that is where the earlier work is.
	req := transcriptText(provider.requests[1])
	if !strings.Contains(req, "stays verbatim") || !strings.Contains(req, "SUMMARY-OF-EARLIER-WORK") {
		t.Fatalf("the second summarization request is wrong:\n%s", req)
	}
	if issues := session.ValidateToolPairing(append(window, llm.Message{Role: llm.RoleTool, ToolCallID: "call_compact", Content: "ok"})); len(issues) > 0 {
		t.Fatalf("malformed tool-call history: %v", issues)
	}
}

// With no earlier fold the head holds the prompt itself.
func TestCompactContextInTheFirstTurnWritesTheInTurnRow(t *testing.T) {
	st := stateOf(t, []llm.Message{stamped(followUpPrompt, openedAt)}, steps("s", 3), []llm.Message{compactContextCall()})
	provider := &compactCannedProvider{t: t, summary: "the first three reads"}
	ag := inTurnAgent(t, st, config.Compaction{}, provider)
	ag.turnOpening = session.AnchorOf(st.GetMessages()[0])

	res, err := ag.CompactSession(context.Background(), CompactOptions{Force: true, FromTool: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.InTurn {
		t.Fatalf("result = %+v, want a fold that kept the prompt in front", res)
	}
	window := windowOf(st)
	if !window[0].CompactionSummary || !strings.HasPrefix(window[0].Content, followUpPrompt+"\n\n") {
		t.Fatalf("the window must open with the prompt: %q", firstChars(window[0].Content, 120))
	}
	if !strings.Contains(window[0].Content, "the first three reads") {
		t.Fatalf("the summary is missing from the row: %q", window[0].Content)
	}
	if !strings.Contains(transcriptText(provider.requests[0]), "stays verbatim") {
		t.Fatalf("the summarizer was not told the work is still going on:\n%s", transcriptText(provider.requests[0]))
	}
	if !strings.Contains(compactionOutcomeText(res), "The request being answered starts the summary.") {
		t.Fatalf("the tool result does not say where the request went: %q", compactionOutcomeText(res))
	}
}

// A /compact between turns is no turn being answered: the last prompt folded with
// the rest, in front of a plain row, would ask the model to answer it again.
func TestManualCompactBetweenTurnsStillWritesAPlainRow(t *testing.T) {
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

// When the opening prompt is in the kept tail it stays an ordinary message and
// the row has nothing in front of it.
func TestCompactContextKeepingThePromptWritesAPlainRow(t *testing.T) {
	st := seededCompactState(t, 3)
	for _, m := range concatMsgs([]llm.Message{stamped(followUpPrompt, openedAt)}, steps("s", 2), []llm.Message{compactContextCall()}) {
		st.AddMessage(m)
	}
	provider := &compactCannedProvider{t: t, summary: "the earlier exchanges"}
	ag := inTurnAgent(t, st, config.Compaction{}, provider)
	ag.turnOpening = session.AnchorOf(stamped(followUpPrompt, openedAt))

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
	if got := strings.Count(transcriptText(window), followUpPrompt); got != 1 {
		t.Fatalf("the window holds the prompt %d times, want once, as an ordinary message", got)
	}
}

func concatMsgs(parts ...[]llm.Message) []llm.Message {
	var out []llm.Message
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// --- follow-ups in the prefix of the row ------------------------------------

// followUpTurn is a turn that went on through a queued follow-up, a Stop-hook
// follow-up and a second queued follow-up that falls into the kept tail:
//
//	0 opening, 1-4 two steps, 5 queued, 6-9 two steps, 10 Stop hook, 11-14 two steps,
//	15-16 a step, 17 queued (tail), 18-19 a step
func followUpTurn() []llm.Message {
	return concatMsgs(
		[]llm.Message{stamped(followUpPrompt, openedAt)}, steps("a", 2),
		[]llm.Message{stamped("Also keep the tests green.", queuedAt)}, steps("b", 2),
		[]llm.Message{stamped(stopHookPrefix+"run the linter before you finish", hookedAt)}, steps("c", 2),
		steps("d", 1),
		[]llm.Message{stamped("And leave the docs alone.", laterQueuedAt)}, steps("e", 1),
	)
}

func TestInTurnFoldCarriesTheQueuedFollowUpsVerbatim(t *testing.T) {
	msgs := followUpTurn()
	st := stateOf(t, msgs)
	two := 2
	provider := &compactCannedProvider{t: t, summary: "audited the first modules"}
	ag := inTurnAgent(t, st, config.Compaction{InTurn: config.InTurn{KeepRecentSteps: &two}}, provider)
	ag.turnOpening = session.AnchorOf(msgs[0])

	res, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.InTurn || res.KeptSteps != 2 {
		t.Fatalf("result = %+v, want an in-turn fold keeping two steps", res)
	}
	window := windowOf(st)
	row := window[0]
	wantPrefix := followUpPrompt + "\n\n" + inTurnFollowUpIntro + "\nAlso keep the tests green.\n\nThe text above is"
	if !row.CompactionSummary || !strings.HasPrefix(row.Content, wantPrefix) {
		t.Fatalf("the row must carry the prompt and the queued follow-up verbatim, got %q", firstChars(row.Content, 220))
	}
	if strings.Contains(row.Content, "run the linter") {
		t.Fatalf("a Stop-hook follow-up is hook output and is summarized, not carried: %q", row.Content)
	}
	if strings.Contains(row.Content, "leave the docs alone") {
		t.Fatalf("a follow-up in the kept tail was repeated in the row: %q", row.Content)
	}
	// The one in the tail stays where it was, as an ordinary message.
	if got := strings.Count(transcriptText(window), "And leave the docs alone."); got != 1 {
		t.Fatalf("the window holds the tail's follow-up %d times, want once", got)
	}
	// The summarizer read the Stop-hook follow-up with the rest of the head.
	if !strings.Contains(transcriptText(provider.requests[0]), "run the linter before you finish") {
		t.Fatal("the Stop-hook follow-up was kept out of the summarization request")
	}
}

// The cap on the prefix is shared by the prompt and the follow-ups.
func TestInTurnPromptPrefixCapsThePromptAndItsFollowUpsTogether(t *testing.T) {
	const limit = 20000
	prompt := "START-OF-REQUEST " + strings.Repeat("a long pasted log line. ", 1500)
	followUps := []string{"FIRST-FOLLOW-UP " + strings.Repeat("more pasted text. ", 800), "LAST-FOLLOW-UP-END"}

	got := inTurnPromptPrefix(prompt, followUps, limit)
	if !strings.HasPrefix(got, "START-OF-REQUEST") || !strings.HasSuffix(got, "LAST-FOLLOW-UP-END") {
		t.Fatalf("an end was lost: %q ... %q", firstChars(got, 40), got[len(got)-40:])
	}
	if tokens, share := session.EstimateContextTokens(got), limit*inTurnPromptSharePercent/100; tokens > share {
		t.Fatalf("the prefix is %d tokens, over the share of %d", tokens, share)
	}
	if !strings.Contains(got, "omitted") {
		t.Fatalf("the cut says nothing: %q", firstChars(got, 200))
	}

	short := inTurnPromptPrefix("do it", []string{"one", "two"}, limit)
	want := "do it\n\n" + inTurnFollowUpIntro + "\none\n\n" + inTurnFollowUpIntro + "\ntwo"
	if short != want {
		t.Fatalf("prefix = %q, want %q", short, want)
	}
}

// --- the opening prompt is not a follow-up ---------------------------------

// Only the user messages up to the opening prompt are turn boundaries for the
// automatic split: folding the turns before the prompt leaves it, and the
// follow-ups, as they were.
func TestAutomaticSplitDoesNotCutAtAFollowUpOfTheTurnInProgress(t *testing.T) {
	earlier := seededCompactState(t, 2).GetMessages()
	turn := concatMsgs([]llm.Message{stamped(followUpPrompt, openedAt)}, steps("a", 2), []llm.Message{stamped("Also keep the tests green.", queuedAt)}, steps("b", 2))
	st := stateOf(t, earlier, turn)
	keep := 1
	provider := &compactCannedProvider{t: t, summary: "the two earlier exchanges"}
	ag := inTurnAgent(t, st, config.Compaction{KeepRecentTurns: &keep}, provider)
	ag.turnOpening = session.AnchorOf(turn[0])

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
	turn := concatMsgs([]llm.Message{stamped(followUpPrompt, openedAt)}, steps("a", 2), []llm.Message{stamped("Also keep the tests green.", queuedAt)}, steps("b", 2))
	st := stateOf(t, turn)
	provider := &compactCannedProvider{t: t, summary: "must not be asked"}
	ag := inTurnAgent(t, st, config.Compaction{}, provider)
	ag.turnOpening = session.AnchorOf(turn[0])

	_, err := ag.CompactSession(context.Background(), CompactOptions{})
	if !errors.Is(err, ErrNothingToCompact) {
		t.Fatalf("err = %v, want ErrNothingToCompact: the prompt would have been folded into a plain summary", err)
	}
	if len(provider.requests) != 0 {
		t.Fatal("the summarizer was called")
	}
}

// The record is a message identity, not a position: a regular compaction of the
// turns before the prompt inserts a row in front of it, and the next fold of the
// turn still finds the prompt, not the follow-up.
func TestRecordedPromptSurvivesARegularCompactionBeforeIt(t *testing.T) {
	earlier := seededCompactState(t, 2).GetMessages()
	turn := concatMsgs([]llm.Message{stamped(followUpPrompt, openedAt)}, steps("a", 3), []llm.Message{stamped("Also keep the tests green.", queuedAt)}, steps("b", 3))
	st := stateOf(t, earlier, turn)
	keep := 1
	two := 2
	provider := &compactCannedProvider{t: t, summary: "summary"}
	ag := inTurnAgent(t, st, config.Compaction{KeepRecentTurns: &keep, InTurn: config.InTurn{KeepRecentSteps: &two}}, provider)
	ag.turnOpening = session.AnchorOf(turn[0])

	if _, err := ag.CompactSession(context.Background(), CompactOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true}); err != nil {
		t.Fatal(err)
	}
	row := windowOf(st)[0]
	want := followUpPrompt + "\n\n" + inTurnFollowUpIntro + "\nAlso keep the tests green.\n\nThe text above is"
	if !strings.HasPrefix(row.Content, want) {
		t.Fatalf("the row must begin with the opening prompt and its follow-up, got %q", firstChars(row.Content, 200))
	}
}

// Without a record (a turn resumed after a permission answer) the opening
// prompt is the last user message that is not a Stop-hook follow-up.
func TestInTurnFoldWithoutARecordSkipsStopHookFollowUps(t *testing.T) {
	turn := concatMsgs(
		[]llm.Message{stamped(followUpPrompt, openedAt)}, steps("a", 3),
		[]llm.Message{stamped(stopHookPrefix+"run the linter", hookedAt)}, steps("b", 3),
	)
	st := stateOf(t, turn)
	one := 1
	provider := &compactCannedProvider{t: t, summary: "summary"}
	ag := inTurnAgent(t, st, config.Compaction{InTurn: config.InTurn{KeepRecentSteps: &one}}, provider)

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

// A compact_context call carries the queued follow-ups its head holds, like an
// in-turn fold does; the one its kept tail starts with stays where it is.
func TestCompactContextCarriesTheQueuedFollowUpsOfItsHead(t *testing.T) {
	msgs := concatMsgs(
		[]llm.Message{stamped(followUpPrompt, openedAt)}, steps("a", 2),
		[]llm.Message{stamped("Also keep the tests green.", queuedAt)}, steps("b", 2),
		[]llm.Message{stamped("And leave the docs alone.", laterQueuedAt)}, steps("c", 1),
		[]llm.Message{compactContextCall()},
	)
	st := stateOf(t, msgs)
	one := 1
	provider := &compactCannedProvider{t: t, summary: "summary"}
	ag := inTurnAgent(t, st, config.Compaction{KeepRecentTurns: &one}, provider)
	ag.turnOpening = session.AnchorOf(msgs[0])

	if _, err := ag.CompactSession(context.Background(), CompactOptions{Force: true, FromTool: true}); err != nil {
		t.Fatal(err)
	}
	window := windowOf(st)
	want := followUpPrompt + "\n\n" + inTurnFollowUpIntro + "\nAlso keep the tests green.\n\nThe text above is"
	if !strings.HasPrefix(window[0].Content, want) {
		t.Fatalf("the row must carry the follow-up of its head verbatim, got %q", firstChars(window[0].Content, 200))
	}
	if strings.Contains(window[0].Content, "leave the docs alone") || window[1].Content != "And leave the docs alone." {
		t.Fatalf("the follow-up the kept tail starts with must stay an ordinary message, once: %s", transcriptText(window[:2]))
	}
}
