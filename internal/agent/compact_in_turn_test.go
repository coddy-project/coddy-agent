package agent

// Unit tests of the fold that stays inside the turn being answered
// (compact_in_turn.go and the in-turn branch of CompactSession). The happy path
// is in features/context_in_turn_compaction.feature.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks/hooktest"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

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

func tailTokens(msgs []llm.Message, idx int) int {
	return conversationTokens(msgs[idx:], false)
}

func splitRequest(msgs []llm.Message, mod func(*inTurnSplitRequest)) inTurnSplitRequest {
	r := inTurnSplitRequest{
		msgs:         msgs,
		visibleStart: 0,
		projected:    msgs,
		keepSteps:    4,
		budget:       1 << 30,
	}
	if mod != nil {
		mod(&r)
	}
	return r
}

func TestChooseInTurnSplit(t *testing.T) {
	// A prompt and eight steps of two parallel results of 3000 characters.
	turn := fixtureTurn("do the audit", 8, 2, 3000)
	at := func(keep int) int {
		idx, ok := session.TurnStepSplitIndex(turn, keep, session.TurnAnchor{})
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

	t.Run("an unlimited budget is capped at keep_recent_steps", func(t *testing.T) {
		got, err := chooseInTurnSplit(splitRequest(turn, func(r *inTurnSplitRequest) { r.keepSteps = 3 }))
		if err != nil {
			t.Fatal(err)
		}
		if got.keptSteps != 3 || got.idx != at(3) {
			t.Fatalf("got %+v, want the cap of three steps at %d", got, at(3))
		}
	})

	t.Run("a cap above the steps keeps all but the first", func(t *testing.T) {
		small := fixtureTurn("p", 3, 1, 100)
		got, err := chooseInTurnSplit(splitRequest(small, func(r *inTurnSplitRequest) { r.keepSteps = 10 }))
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
		msgs := append(append(append([]llm.Message{}, old...), row), rest...)
		visibleStart := len(old)
		got, err := chooseInTurnSplit(splitRequest(msgs, func(r *inTurnSplitRequest) {
			r.visibleStart = visibleStart
			r.projected = msgs[visibleStart:]
			r.keepSteps = 2
		}))
		if err != nil {
			t.Fatal(err)
		}
		want, _ := session.TurnStepSplitIndex(msgs, 2, session.TurnAnchor{})
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
	msgs := append(append([]llm.Message{}, earlier...), current...)
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

	t.Run("a request refused before the first step folds what came before it", func(t *testing.T) {
		noSteps := append(append([]llm.Message{}, earlier...), llm.Message{Role: llm.RoleUser, Content: "the current prompt"})
		got, err := chooseInTurnSplit(splitRequest(noSteps, func(r *inTurnSplitRequest) { r.recovery = true; r.budget = 0; r.regularBudget = 0 }))
		if err != nil {
			t.Fatal(err)
		}
		if !got.regular || got.idx != len(earlier) {
			t.Fatalf("got %+v, want the regular split before the prompt", got)
		}
	})

	t.Run("no steps and no earlier turn is nothing to fold", func(t *testing.T) {
		_, err := chooseInTurnSplit(splitRequest([]llm.Message{{Role: llm.RoleUser, Content: "only"}}, func(r *inTurnSplitRequest) { r.recovery = true }))
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

func TestInTurnPromptPrefix(t *testing.T) {
	t.Run("a prompt within its share is kept verbatim", func(t *testing.T) {
		prompt := "Fix the failing build.\n\nThen explain why it broke."
		if got := inTurnPromptPrefix(prompt, nil, 49152); got != prompt {
			t.Fatalf("prefix = %q, want the prompt verbatim", got)
		}
	})

	t.Run("a huge prompt is cut in the middle with its own marker", func(t *testing.T) {
		prompt := "START OF THE REQUEST " + strings.Repeat("a long pasted log line. ", 4000) + " END OF THE REQUEST"
		const limit = 20000
		got := inTurnPromptPrefix(prompt, nil, limit)
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
		got := inTurnPromptPrefix(prompt, nil, 1000)
		if tokens := session.EstimateContextTokens(got); tokens < inTurnPromptMinTokens-50 || tokens > inTurnPromptMinTokens+20 {
			t.Fatalf("prefix = %d tokens, want about the floor of %d", tokens, inTurnPromptMinTokens)
		}
	})
}

// --- CompactSession with InTurn ----------------------------------------------

// inTurnSession is one turn in progress: a prompt and n steps of one read each.
func inTurnSession(t *testing.T, prompt string, n int) *session.State {
	t.Helper()
	st := &session.State{ID: "sess_in_turn_unit", CWD: t.TempDir(), Mode: session.ModeAgent}
	for _, m := range fixtureTurn(prompt, n, 1, 600) {
		st.AddMessage(m)
	}
	return st
}

func inTurnAgent(t *testing.T, st *session.State, comp config.Compaction, provider llm.Provider) *Agent {
	t.Helper()
	// The window is left to the default (128000), so the target is large and
	// keep_recent_steps decides how much stays.
	return compactTestAgent(t, st, comp, provider)
}

func TestCompactSessionInTurnFoldsEarlierStepsBehindThePrompt(t *testing.T) {
	const prompt = "Audit every module.\n\nReport each finding with its file."
	st := inTurnSession(t, prompt, 6)
	before := st.GetMessages()
	two := 2
	provider := &compactCannedProvider{t: t, summary: "steps one to four read the first modules"}
	ag := inTurnAgent(t, st, config.Compaction{InTurn: config.InTurn{KeepRecentSteps: &two}}, provider)

	res, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.InTurn || res.KeptSteps != 2 {
		t.Fatalf("result = %+v, want an in-turn fold keeping two steps", res)
	}
	// Prompt + four folded steps (assistant + result each) are the head.
	if res.CompactedMessages != 1+4*2 || res.KeptMessages != 2*2 {
		t.Fatalf("counts = %d folded / %d kept, want 9 / 4", res.CompactedMessages, res.KeptMessages)
	}

	after := st.GetMessages()
	if len(after) != len(before)+1 {
		t.Fatalf("a fold adds one row and removes none: %d messages, was %d", len(after), len(before))
	}
	window := session.MessagesForLLM(after)
	if !window[0].CompactionSummary || !strings.HasPrefix(window[0].Content, prompt+"\n\n") {
		t.Fatalf("the window must open with the prompt verbatim, got %q", window[0].Content)
	}
	if !strings.Contains(window[0].Content, "steps one to four read the first modules") {
		t.Fatalf("the summary is missing from the row: %q", window[0].Content)
	}
	if got := strings.Count(transcriptText(window), prompt); got != 1 {
		t.Fatalf("the window holds the prompt %d times, want once", got)
	}
	// The kept steps are the latest two, byte for byte.
	for i, m := range window[1:] {
		want := before[len(before)-4+i]
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

func TestCompactSessionInTurnNeedsAtLeastTwoSteps(t *testing.T) {
	for _, steps := range []int{0, 1} {
		st := inTurnSession(t, "p", steps)
		provider := &compactCannedProvider{t: t, summary: "unused"}
		ag := inTurnAgent(t, st, config.Compaction{}, provider)
		_, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true})
		if !errors.Is(err, ErrNothingToCompact) {
			t.Fatalf("%d step(s): err = %v, want ErrNothingToCompact", steps, err)
		}
		if len(provider.requests) != 0 {
			t.Fatalf("%d step(s): the summarizer was called", steps)
		}
	}
}

// Folding twice keeps one copy of the prompt: the second row's head holds the
// first row, whose prompt is not written again beside the new one.
func TestChainedInTurnFoldsKeepOnePromptCopy(t *testing.T) {
	const prompt = "UNIQUE-REQUEST-TEXT: audit every module and report."
	st := inTurnSession(t, prompt, 10)
	two := 2
	provider := &compactCannedProvider{t: t, summary: "SUMMARY-OF-EARLIER-WORK"}
	ag := inTurnAgent(t, st, config.Compaction{InTurn: config.InTurn{KeepRecentSteps: &two}}, provider)

	if _, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true}); err != nil {
		t.Fatal(err)
	}
	// The turn goes on: three more steps, then a second fold.
	for _, m := range fixtureTurn("ignored", 3, 1, 600)[1:] {
		m := m
		if m.Role == llm.RoleAssistant {
			for i := range m.ToolCalls {
				m.ToolCalls[i].ID = "later_" + m.ToolCalls[i].ID
			}
		} else {
			m.ToolCallID = "later_" + m.ToolCallID
		}
		st.AddMessage(m)
	}
	if _, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true}); err != nil {
		t.Fatal(err)
	}

	window := session.MessagesForLLM(st.GetMessages())
	if !window[0].CompactionSummary || !strings.HasPrefix(window[0].Content, prompt) {
		t.Fatalf("the window must open with the prompt: %q", window[0].Content)
	}
	if got := strings.Count(transcriptText(window), prompt); got != 1 {
		t.Fatalf("the window holds the prompt %d times after two folds, want once", got)
	}
	rows := 0
	for _, m := range st.GetMessages() {
		if m.CompactionSummary {
			rows++
		}
	}
	if rows != 2 {
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
	one := 1
	provider := &compactCannedProvider{t: t, summary: "summary"}
	ag := inTurnAgent(t, st, config.Compaction{InTurn: config.InTurn{KeepRecentSteps: &one}}, provider)

	if _, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true}); err != nil {
		t.Fatal(err)
	}
	row := session.MessagesForLLM(st.GetMessages())[0]
	if !strings.HasPrefix(row.Content, "HEAD-OF-REQUEST") || !strings.Contains(row.Content, "TAIL-OF-REQUEST") {
		t.Fatalf("the row lost an end of the request: %q ... %q", firstChars(row.Content, 60), row.Content[len(row.Content)-120:])
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

// A model without a prompt on record (a history that starts mid-turn) gets a
// plain summary row.
func TestCompactSessionInTurnWithoutAPromptWritesAPlainRow(t *testing.T) {
	st := &session.State{ID: "sess_in_turn_noprompt", CWD: t.TempDir(), Mode: session.ModeAgent}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "   "})
	for _, m := range fixtureTurn("x", 4, 1, 600)[1:] {
		st.AddMessage(m)
	}
	provider := &compactCannedProvider{t: t, summary: "summary"}
	ag := inTurnAgent(t, st, config.Compaction{}, provider)

	if _, err := ag.CompactSession(context.Background(), CompactOptions{InTurn: true}); err != nil {
		t.Fatal(err)
	}
	row := session.MessagesForLLM(st.GetMessages())[0]
	if !strings.HasPrefix(row.Content, "The earlier conversation was compacted.") {
		t.Fatalf("a history with no prompt must get the regular row, got %q", row.Content)
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
