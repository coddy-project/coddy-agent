package agent

// Prompt-cache check of the fold inside one long turn (issue #490): a summary
// written into the middle of the replayed history costs the provider its cached
// copy of everything behind it, so the fold waits for the compaction threshold.
// One prompt, a model that reads files in parallel step after step, and a window
// the turn outgrows several times. Between folds every request has to repeat the
// previous one and add to it; a fold is the only place the history is rewritten,
// and what it leaves is the prompt at the start of the summary row followed by
// the kept steps exactly as they were.

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestPromptCacheInTurnFoldRewritesTheHistoryOnlyAtTheFold(t *testing.T) {
	// The window is sized so that a fold keeps two steps (the aim leaves room for
	// them beside the request's fixed overhead) and the trigger fires every few
	// steps: the run folds several times, which is what makes the check mean
	// something.
	dirs := []string{}
	tempDir := func() (string, error) {
		d, err := os.MkdirTemp("", "coddy-pcache-inturn-*")
		if err == nil {
			dirs = append(dirs, d)
		}
		return d, err
	}
	t.Cleanup(func() {
		for _, d := range dirs {
			_ = os.RemoveAll(d)
		}
	})
	w, err := inTurnLongTurn{window: 60000, steps: 16, fanOut: 4, evictOff: true}.run(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if w.runErr != nil {
		t.Fatalf("the turn failed: %v", w.runErr)
	}
	reqs := w.provider.accepted
	hist := make([][]llm.Message, len(reqs))
	for i, r := range reqs {
		hist[i] = withoutTurnContext(r)
	}
	if got := len(hist); got != 16+1 {
		t.Fatalf("the run made %d requests, want %d: a request per step and the answer", got, 17)
	}

	// The system message is the same bytes in every request, folds included.
	for i, h := range hist {
		if h[0].Role != llm.RoleSystem || h[0].Content != hist[0][0].Content {
			t.Fatalf("request %d does not open with the system message of request 0", i)
		}
	}

	// Transcript content of every message by tool call id, for what the kept
	// steps must still read as.
	stored := map[string]llm.Message{}
	for _, m := range w.st.GetMessages() {
		if m.Role == llm.RoleTool {
			stored[m.ToolCallID] = m
		}
	}

	folds, appends, keptAsSent := 0, 0, 0
	for i := 1; i < len(hist); i++ {
		prev, cur := hist[i-1], hist[i]
		if len(cur) > len(prev) && reflect.DeepEqual(prev, cur[:len(prev)]) {
			// Between folds: the previous request, repeated, with the step that
			// came after it added.
			appends++
			continue
		}
		folds++
		// A fold is the one place the history is rewritten. It is a smaller
		// request that opens, after the system message, with the summary row, and
		// the row begins with the prompt, verbatim.
		if estimateMessagesTokens(cur) >= estimateMessagesTokens(prev) {
			t.Fatalf("request %d rewrote the history without shrinking it: %d tokens after %d", i, estimateMessagesTokens(cur), estimateMessagesTokens(prev))
		}
		row := cur[1]
		if !row.CompactionSummary || !strings.HasPrefix(row.Content, inTurnPrompt+"\n\n") {
			t.Fatalf("request %d: the message after the system message is not the summary row that begins with the prompt: %q", i, firstChars(row.Content, 100))
		}
		if got := strings.Count(transcriptText(cur), inTurnPrompt); got != 1 {
			t.Fatalf("request %d holds the prompt %d times, want once", i, got)
		}
		// What follows the row is the latest steps of the turn, byte for byte what
		// the previous request carried (or, for the step that came after it, what
		// the transcript stored), and whole steps: it opens on an assistant message.
		kept := cur[2:]
		if len(kept) == 0 || kept[0].Role != llm.RoleAssistant {
			t.Fatalf("request %d: the kept steps do not open on an assistant message: %+v", i, kept)
		}
		before := map[string]llm.Message{}
		for _, m := range prev {
			if m.Role == llm.RoleTool {
				before[m.ToolCallID] = m
			}
		}
		for j, m := range kept {
			if m.Role != llm.RoleTool {
				continue
			}
			want, ok := before[m.ToolCallID]
			if ok {
				keptAsSent++
			} else {
				want = stored[m.ToolCallID]
			}
			if !reflect.DeepEqual(m, want) {
				t.Fatalf("request %d: kept message %d (%s) is not the one it was before the fold:\nafter:  %+v\nbefore: %+v", i, j, m.ToolCallID, m, want)
			}
		}
		if issues := session.ValidateToolPairing(cur); len(issues) != 0 {
			t.Fatalf("request %d breaks the tool-call pairing: %v", i, issues)
		}
	}

	// Both phases have to be exercised, or the test passes without testing
	// anything: the run folds more than once and spends most of its requests
	// appending.
	if folds < 2 || appends < 8 {
		t.Fatalf("the run folded %d time(s) and appended %d time(s): too little of either to mean anything", folds, appends)
	}
	// And a fold has to keep a step that an earlier request had already sent, or
	// "kept exactly as it was" compares nothing but the newest step to the
	// transcript.
	if keptAsSent == 0 {
		t.Fatal("no fold kept a step the previous request carried: the window leaves room for the latest step only")
	}
	t.Logf("%d requests: %d folds, %d appends", len(hist), folds, appends)

	// Each fold left a row in the transcript; none was lost.
	rows := 0
	for _, m := range w.st.GetMessages() {
		if m.CompactionSummary {
			rows++
		}
	}
	if rows != folds {
		t.Fatalf("the transcript holds %d summary rows for %d folds", rows, folds)
	}
}
