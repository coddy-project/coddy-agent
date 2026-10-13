package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/hooks"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A step the output limit stopped in the middle of visible text is not the end
// of the turn: the text stays, because the user read it, and the model is asked
// to go on from its last word (outputLimitContinueNudge), inside the bound every
// cut-off step has (output_limit_test.go). The text is stored Truncated and the
// step that goes on from it is a second assistant message, so this file also
// holds what reads the two as the one text they are: the next turn's history
// (foldSplitAnswers), the answer a turn hands over (a subagent's report, the
// last_assistant_message of a Stop hook), and the placement markers of shared
// files, which close the message that completes the answer and never the
// truncated one. The happy path is features/turn_end_reasons.feature; these are
// its edges.

// restOfText finishes what partialStep began.
var restOfText = outputLimitStep{text: " the second half.", stop: "end_turn"}

// TestPromptCacheOutputLimitContinuationIsLocalAndAppendOnly: the message that
// asks the model to go on reaches the model only. The request after the cut-off
// step is the one before it with the step's text and that message added at the
// end, nothing in between rewritten; the transcript keeps the text and the
// continuation as the two assistant messages they are, and never the message.
func TestPromptCacheOutputLimitContinuationIsLocalAndAppendOnly(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{partialStep, restOfText}}
	ag, st := outputLimitAgent(t, p, nil)

	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 2 {
		t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after 2", stop, err, p.calls)
	}
	first, second := requestHistory(p.seen[0]), requestHistory(p.seen[1])
	if len(second) != len(first)+2 {
		t.Fatalf("history went from %d to %d messages, want two more (the text and the message):\n%s", len(first), len(second), strings.Join(second, "\n"))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("message %d of the cached history changed:\nbefore: %s\nafter:  %s", i, first[i], second[i])
		}
	}
	if got, want := second[len(second)-2], "assistant: "+partialText; got != want {
		t.Fatalf("the text the user read is not replayed as it was: %q, want %q", got, want)
	}
	if got, want := second[len(second)-1], "user: "+outputLimitContinueNudge; got != want {
		t.Fatalf("the last message is %q, want the message that asks the model to continue", got)
	}

	stored := st.GetMessages()
	for _, m := range stored {
		if m.Content == outputLimitContinueNudge {
			t.Fatal("the continuation message was written to the transcript")
		}
	}
	// The text and its continuation are stored as they streamed at the join: the
	// continuation keeps the space it starts with.
	if got := assistantTexts(stored); len(got) != 2 || got[0] != partialText || got[1] != restOfText.text {
		t.Fatalf("assistant messages of the transcript = %q, want the text and its continuation", got)
	}
	if notice := st.TakeTurnStopNotice(); notice != "" {
		t.Fatalf("a continued turn left a stop notice: %q", notice)
	}
}

// TestOutputLimitContinuationThenCutOffStepShareTheBound: a text cut off, then
// a step cut off while it only thought, are two recoveries of one chain: the
// request after them carries both messages, in the order they were earned, and
// the third cut-off ends the turn on the output limit.
func TestOutputLimitContinuationThenCutOffStepShareTheBound(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{partialStep, cutOff, cutOff}}
	ag, st := outputLimitAgent(t, p, nil)
	stop, err := runOutputLimitTurn(ag)
	if err != nil || stop != string(acp.StopReasonMaxTokens) || p.calls != 1+maxOutputLimitRecoveries {
		t.Fatalf("stop = %q, err = %v after %d calls; want max_tokens after %d", stop, err, p.calls, 1+maxOutputLimitRecoveries)
	}
	hist := requestHistory(p.seen[2])
	n := len(hist)
	if n < 4 || hist[n-3] != "assistant: "+partialText || hist[n-2] != "user: "+outputLimitContinueNudge || hist[n-1] != "user: "+outputLimitNudge {
		t.Fatalf("the last request does not carry the two messages in order after the text:\n%s", strings.Join(hist, "\n"))
	}
	if hasEmptyAssistant(p.seen[2]) {
		t.Fatal("the step that only thought was replayed")
	}
	if notice := st.TakeTurnStopNotice(); notice != plainOutputLimitNotice {
		t.Fatalf("notice = %q, want %q", notice, plainOutputLimitNotice)
	}
}

// A text the output limit cut off and the step that goes on from it are two
// assistant messages of the transcript, with a local message between them in
// the requests of the turn. A history built from the transcript for a later turn
// has no such message to separate them, and chat templates that insist on
// alternation refuse two assistant messages in a row, every later turn, for the
// rest of the session. So that history joins them: the text with nothing between
// it and its continuation, stored and replayed as streamed; two messages that
// are not one text with a blank line. Signed reasoning keeps its message.

// TestPromptCacheNextTurnAfterAContinuationSendsOneAssistantMessage: the
// continued answer goes into the next turn's request as the one text it is, byte
// for byte as it streamed, whitespace at the join and a code fence included, and
// the requests of the turn that continued it are what they were.
func TestPromptCacheNextTurnAfterAContinuationSendsOneAssistantMessage(t *testing.T) {
	for _, tc := range []struct {
		name, first, rest string
	}{
		{"a space at the end of the text", "Here is the first half of the module and then ", "the second half."},
		{"a space at the start of the continuation", "Here is the first half of the module and then", " the second half."},
		{"a code fence on a line of its own", "Here is the module:\n```go\nfunc Run() {", "\n}\n```\nThat is all."},
		{"trailing blank lines", "First paragraph.\n\n", "Second paragraph."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &outputLimitProvider{steps: []outputLimitStep{
				{text: tc.first, stop: "max_tokens"},
				{text: tc.rest, stop: "end_turn"},
				{text: "Tested.", stop: "end_turn"},
			}}
			ag, st := outputLimitAgent(t, p, nil)
			if stop, err := runOutputLimitTurn(ag); err != nil || stop != string(acp.StopReasonEndTurn) {
				t.Fatalf("first turn: stop = %q, err = %v", stop, err)
			}
			// The turn itself saw the two as two, with its message between.
			inTurn := p.seen[1]
			if got := assistantMessages(inTurn); len(got) != 1 || got[0].Content != tc.first {
				t.Fatalf("the request of the turn replays %+v, want the text as it streamed", got)
			}
			// What the transcript stores is what streamed, at the join.
			stored := assistantMessages(st.GetMessages())
			if len(stored) != 2 || stored[0].Content != tc.first || stored[1].Content != tc.rest || !stored[0].Truncated {
				t.Fatalf("the transcript stores %+v, want the text (truncated) and its continuation exactly as streamed", stored)
			}

			if stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "now test it"}}); err != nil || stop != string(acp.StopReasonEndTurn) {
				t.Fatalf("second turn: stop = %q, err = %v", stop, err)
			}
			next := p.seen[len(p.seen)-1]
			requireNoAssistantInARow(t, next)
			if got := assistantMessages(next); len(got) != 1 || got[0].Content != tc.first+tc.rest {
				t.Fatalf("the next turn replays %+v, want one message holding %q", got, tc.first+tc.rest)
			}
		})
	}
}

// TestPromptCacheNextTurnAfterACutCallWithTextSendsOneAssistantMessage: the
// text before a cut-off call and the step that answered the message about it
// are two assistant messages, and a step with a call joins the text before it.
func TestPromptCacheNextTurnAfterACutCallWithTextSendsOneAssistantMessage(t *testing.T) {
	read := llm.ToolCall{ID: "r1", Name: "coddy_todo_plan_read", InputJSON: "{}"}
	p := &outputLimitProvider{steps: []outputLimitStep{
		cutWriteStep("I will write it.", cutWrite("c1")),
		{calls: []llm.ToolCall{read}, stop: "tool_use"},
		{text: "Done.", stop: "end_turn"},
		{text: "ok", stop: "end_turn"},
	}}
	ag, _, _ := cutCallAgent(t, p, nil)
	if _, err := runOutputLimitTurn(ag); err != nil {
		t.Fatal(err)
	}
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "next"}}); err != nil {
		t.Fatal(err)
	}
	next := p.seen[len(p.seen)-1]
	requireNoAssistantInARow(t, next)
	var withCall *llm.Message
	for i := range next {
		if len(next[i].ToolCalls) == 1 && next[i].ToolCalls[0].ID == "r1" {
			withCall = &next[i]
		}
	}
	if withCall == nil || withCall.Content != "I will write it." {
		t.Fatalf("the step with the call does not carry the text before it: %+v", withCall)
	}
}

// TestFoldSplitAnswers pins what is joined and what is not.
func TestFoldSplitAnswers(t *testing.T) {
	user := llm.Message{Role: llm.RoleUser, Content: "go"}
	text := func(s string) llm.Message { return llm.Message{Role: llm.RoleAssistant, Content: s} }
	cut := func(s string) llm.Message { return llm.Message{Role: llm.RoleAssistant, Content: s, Truncated: true} }
	signed := func(m llm.Message) llm.Message { m.ReasoningSignature = "sig"; return m }
	withCall := func(m llm.Message) llm.Message {
		m.ToolCalls = []llm.ToolCall{{ID: "c1", Name: "read", InputJSON: "{}"}}
		return m
	}
	for _, tc := range []struct {
		name string
		in   []llm.Message
		want []llm.Message
	}{
		{"a text and its continuation", []llm.Message{user, cut("and then "), text("the rest.")}, []llm.Message{user, text("and then the rest.")}},
		{"a chain of continuations", []llm.Message{user, cut("a"), cut("b"), text("c")}, []llm.Message{user, text("abc")}},
		{"the merged message is cut when its continuation is", []llm.Message{user, cut("a"), cut("b")}, []llm.Message{user, cut("ab")}},
		{"two texts that are not one", []llm.Message{user, text("I will write it."), text("Done.")}, []llm.Message{user, text("I will write it.\n\nDone.")}},
		{"a text before a step with a call", []llm.Message{user, text("I will read."), withCall(text(""))}, []llm.Message{user, withCall(text("I will read."))}},
		{"a text before a step with text and a call", []llm.Message{user, text("I will read."), withCall(text("Reading."))}, []llm.Message{user, withCall(text("I will read.\n\nReading."))}},
		{"a step with a call is not joined to what follows", []llm.Message{user, withCall(text("a")), text("b")}, []llm.Message{user, withCall(text("a")), text("b")}},
		{"signed reasoning before", []llm.Message{user, signed(text("a")), text("b")}, []llm.Message{user, signed(text("a")), text("b")}},
		{"signed reasoning after", []llm.Message{user, text("a"), signed(text("b"))}, []llm.Message{user, text("a"), signed(text("b"))}},
		{"a user message between", []llm.Message{text("a"), user, text("b")}, []llm.Message{text("a"), user, text("b")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := foldSplitAnswers(tc.in)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d messages, want %d: %+v", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i].Role != tc.want[i].Role || got[i].Content != tc.want[i].Content || got[i].Truncated != tc.want[i].Truncated ||
					len(got[i].ToolCalls) != len(tc.want[i].ToolCalls) || got[i].ReasoningSignature != tc.want[i].ReasoningSignature {
					t.Fatalf("message %d = %+v, want %+v", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestBuildMessagesLeavesASignedStepItsMessage: a step with signed reasoning,
// which Anthropic, Codex and Devin replay, is not folded into the text next to
// it, whichever side it is on.
func TestBuildMessagesLeavesASignedStepItsMessage(t *testing.T) {
	st := &session.State{ID: "sess_fold", CWD: t.TempDir(), Mode: session.ModeAgent}
	for _, m := range []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleAssistant, Content: "a cut text", Truncated: true},
		{Role: llm.RoleAssistant, Content: "its continuation", Reasoning: "signed", ReasoningSignature: "sig"},
	} {
		st.AddMessage(m)
	}
	ag := NewAgent(&config.Config{Agent: config.Agent{Model: "fake/model"}}, st, resumePermissionSender{}, nil)
	if got := assistantMessages(ag.buildMessages("SYSTEM")); len(got) != 2 {
		t.Fatalf("a signed step was folded: %+v", got)
	}
}

// An answer the output limit cut off and the model went on from is several
// assistant messages of the transcript. What a turn hands over as its answer -
// the report of a subagent, the last_assistant_message of a Stop hook - is all
// of it, not the part after the last cut.

// shareFileCall is a call of share_file on a file of the workspace.
func shareFileCall(id, path string) llm.ToolCall {
	return llm.ToolCall{ID: id, Name: "share_file", InputJSON: `{"path":"` + path + `"}`}
}

// cutTextStep is a scripted step that streams text and stops at the output limit
// in the middle of it.
func cutTextStep(text string) scriptStep {
	return func(_ []llm.Message, _ []llm.ToolDefinition, onChunk func(llm.StreamChunk)) *llm.Response {
		onChunk(llm.StreamChunk{TextDelta: text})
		return &llm.Response{Content: text, StopReason: "max_tokens"}
	}
}

func TestLastAssistantPlainTextIsTheWholeContinuedAnswer(t *testing.T) {
	user := llm.Message{Role: llm.RoleUser, Content: "go"}
	cut := func(s string) llm.Message { return llm.Message{Role: llm.RoleAssistant, Content: s, Truncated: true} }
	text := func(s string) llm.Message { return llm.Message{Role: llm.RoleAssistant, Content: s} }
	empty := llm.Message{Role: llm.RoleAssistant, Reasoning: "Hmm."}
	call := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "read"}}}
	tool := llm.Message{Role: llm.RoleTool, ToolCallID: "c1", Content: "ok"}
	for _, tc := range []struct {
		name string
		msgs []llm.Message
		want string
	}{
		{"no assistant message", []llm.Message{user}, ""},
		{"a plain answer", []llm.Message{user, text("Done.")}, "Done."},
		{"a text and its continuation", []llm.Message{user, cut("REPORT: the cell is a butt"), text("on labelled Save.")}, "REPORT: the cell is a button labelled Save."},
		{"a chain of three", []llm.Message{user, cut("a "), cut("b "), text("c")}, "a b c"},
		{"an empty step in the chain", []llm.Message{user, cut("a "), empty, text("b")}, "a b"},
		{"an empty last step is passed over", []llm.Message{user, cut("a "), text("b"), empty}, "a b"},
		{"an empty last step after a plain answer", []llm.Message{user, text("a"), text("b"), empty}, "b"},
		{"a continuation after a tool step", []llm.Message{user, call, tool, cut("a "), text("b")}, "a b"},
		{"the chain stops at the tool step", []llm.Message{user, cut("a "), call, tool, text("b")}, "b"},
		{"an answer after a plain one is its own", []llm.Message{user, text("first"), text("second")}, "second"},
		{"a cut text that nothing went on from", []llm.Message{user, cut("and then ")}, "and then"},
		// The placement markers of shared files are for the web UI and close the
		// message that completes the answer; a report goes to a model.
		{"markers stay out of the report", []llm.Message{user, cut("REPORT: the cell is a butt"), text("on labelled Save.\n\n<coddy_file id=\"a\"/>\n<coddy_file id=\"b\"/>")}, "REPORT: the cell is a button labelled Save."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := lastAssistantPlainText(tc.msgs); got != tc.want {
				t.Fatalf("lastAssistantPlainText = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSubagentReportIsTheWholeContinuedAnswer: a child whose report was cut off at
// the output limit and went on hands its parent all of it. A report that ended
// "on labelled Save." would leave the parent model without the half of what the
// child found.
func TestSubagentReportIsTheWholeContinuedAnswer(t *testing.T) {
	s := &subagentsFeatureState{}
	if err := s.reset(); err != nil {
		t.Fatal(err)
	}
	defer s.close()
	for _, step := range []func() error{
		func() error { return s.workspaceDefinition("reviewer") },
		s.parentSession,
		func() error { return s.approveDefinition("reviewer") },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	s.mu.Lock()
	s.childSteps = func() []scriptStep {
		return []scriptStep{
			cutTextStep("REPORT: the cell is a butt"),
			answerStep("on labelled Save."),
		}
	}
	s.mu.Unlock()
	if err := s.spawnWith("reviewer", false); err != nil {
		t.Fatal(err)
	}
	res, err := s.lastSpawnResult()
	if err != nil {
		t.Fatal(err)
	}
	if want := "REPORT: the cell is a button labelled Save."; !strings.Contains(res, want) {
		t.Fatalf("the spawn_agent result is %q, want it to hold the whole report %q", res, want)
	}
	// The child was asked to go on once, which is how the answer became two
	// messages of its transcript.
	child := s.childProviderByName("reviewer")
	if child == nil {
		t.Fatal("no child ran as reviewer")
	}
	child.mu.Lock()
	defer child.mu.Unlock()
	if len(child.requests) != 2 || countNudges(child.requests[1], outputLimitContinueNudge) != 1 {
		t.Fatalf("the child made %d requests, want the cut-off step and one that asks it to go on", len(child.requests))
	}
}

// TestStopHookIsHandedTheWholeContinuedAnswer: the last_assistant_message of a Stop
// hook is the answer the turn ends with in full, and without the placement
// markers of shared files, which are for the web UI only.
func TestStopHookIsHandedTheWholeContinuedAnswer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		share bool
		steps []scriptStep
		want  string
	}{
		{
			name:  "a text and its continuation",
			steps: []scriptStep{cutTextStep("The notes hold"), answerStep(" forty numbered lines.")},
			want:  "The notes hold forty numbered lines.",
		},
		{
			name:  "a chain of three",
			steps: []scriptStep{cutTextStep("The notes "), cutTextStep("hold forty "), answerStep("numbered lines.")},
			want:  "The notes hold forty numbered lines.",
		},
		{
			name:  "an answer that was not cut off",
			steps: []scriptStep{answerStep("The notes hold forty numbered lines.")},
			want:  "The notes hold forty numbered lines.",
		},
		{
			// The markers of the file shared before the answer close its last
			// part; the hook is not handed them.
			name:  "a file shared before the cut",
			share: true,
			steps: []scriptStep{
				toolStep(shareFileCall("call-share", "notes.txt")),
				cutTextStep("The file is the butt"),
				answerStep("on labelled notes."),
			},
			want: "The file is the button labelled notes.",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &hooksFeatureState{}
			if err := s.reset(); err != nil {
				t.Fatal(err)
			}
			defer s.close()
			if err := s.hookRecordsAny(hooks.EventStop); err != nil {
				t.Fatal(err)
			}
			if err := s.agentSession(); err != nil {
				t.Fatal(err)
			}
			if tc.share {
				if err := os.WriteFile(filepath.Join(s.cwd, "notes.txt"), []byte("forty numbered lines\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.runPrompt("go", tc.steps...); err != nil {
				t.Fatal(err)
			}
			if s.turnErr != nil || s.stopReason != "end_turn" {
				t.Fatalf("the turn ended with err = %v, stop = %q; want end_turn", s.turnErr, s.stopReason)
			}
			payload, err := s.recordedPayload()
			if err != nil {
				t.Fatal(err)
			}
			if payload["hook_event_name"] != hooks.EventStop {
				t.Fatalf("recorded event = %v, want %s", payload["hook_event_name"], hooks.EventStop)
			}
			if got, _ := payload["last_assistant_message"].(string); got != tc.want {
				t.Fatalf("last_assistant_message = %q, want %q", got, tc.want)
			}
		})
	}
}

// The placement markers of shared files (<coddy_file id="..."/>) are for the web
// UI. A text the output limit cut off and the step that goes on from it are one
// text, joined with nothing between, so a truncated message takes no marker
// (placePendingArtifacts): the files stay pending, and the message that
// completes the answer closes with their markers like any other message, in the
// transcript and as the chunk streamed after it. A marker after the truncated
// part would land in the middle of a word as often as not.

// agentChunks is the text of every message chunk a client was sent, in order.
func agentChunks(s *outputLimitSender) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, u := range s.updates {
		if c, ok := u.(acp.MessageChunkUpdate); ok && c.SessionUpdate == acp.UpdateTypeAgentMessageChunk {
			out = append(out, c.Content.Text)
		}
	}
	return out
}

// shareNotes is a call of share_file on the notes file of cutCallAgent.
var shareNotes = shareFileCall("call_share", "notes.txt")

// TestNoMarkerChunkIsStreamedForATruncatedMessage: a message the output limit cut
// off takes neither the markers of the files shared before it nor the files, and
// no marker chunk is streamed after it - a chunk there would land in the middle
// of the text that goes on. The continuation that completes the answer carries
// the file and closes with its markers, stored and streamed. The next turn sends
// the answer as it was written, whole and without a marker.
func TestNoMarkerChunkIsStreamedForATruncatedMessage(t *testing.T) {
	const (
		head = "The notes file is the butt"
		rest = "on labelled notes."
	)
	p := &outputLimitProvider{steps: []outputLimitStep{
		{calls: []llm.ToolCall{shareNotes}, stop: "tool_use"},
		{text: head, stop: "max_tokens"},
		{text: rest, stop: "end_turn"},
		{text: "ok", stop: "end_turn"},
	}}
	ag, st, sender := cutCallAgent(t, p, nil)
	if stop, err := runOutputLimitTurn(ag); err != nil || stop != string(acp.StopReasonEndTurn) || p.calls != 3 {
		t.Fatalf("stop = %q, err = %v after %d calls; want end_turn after 3", stop, err, p.calls)
	}

	said := textMessages(st.GetMessages())
	if len(said) != 2 || !said[0].Truncated || said[1].Truncated {
		t.Fatalf("the transcript holds %+v, want the cut text and its continuation", said)
	}
	if said[0].Content != head || len(said[0].Artifacts) != 0 {
		t.Fatalf("the cut text is stored as %+v, want its text alone, without a marker or a file", said[0])
	}
	if len(said[1].Artifacts) != 1 {
		t.Fatalf("the continuation carries artifacts %+v, want the shared file", said[1].Artifacts)
	}
	markers := session.ArtifactMarkers(said[1].Artifacts)
	if markers == "" {
		t.Fatal("the shared file has no marker; the test proves nothing")
	}
	if want := rest + "\n\n" + markers; said[1].Content != want {
		t.Fatalf("the continuation is stored as %q, want it closed with the markers: %q", said[1].Content, want)
	}

	// The client was streamed the answer as the model wrote it, then the markers
	// once, after the continuation.
	chunks := agentChunks(sender)
	var markerChunks int
	for _, c := range chunks {
		if strings.Contains(c, "<coddy_file") {
			markerChunks++
		}
	}
	if markerChunks != 1 || chunks[len(chunks)-1] != "\n\n"+markers {
		t.Fatalf("chunks = %q, want one marker chunk, %q, closing the continuation", chunks, "\n\n"+markers)
	}
	if got, want := strings.Join(chunks, ""), head+rest+"\n\n"+markers; got != want {
		t.Fatalf("the client was streamed %q, want %q", got, want)
	}

	// The next turn sends the text as it was written, whole.
	if _, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "next"}}); err != nil {
		t.Fatal(err)
	}
	requireNoAssistantInARow(t, p.seen[len(p.seen)-1])
	if sent := assistantTexts(p.seen[len(p.seen)-1]); len(sent) != 1 || sent[0] != head+rest {
		t.Fatalf("the next turn replays %q, want the one text %q", sent, head+rest)
	}
}

// TestAMarkerChunkClosesAReplyThatWasNotCutOff is the other half of the above:
// the markers of a reply that is not cut off are streamed after it.
func TestAMarkerChunkClosesAReplyThatWasNotCutOff(t *testing.T) {
	p := &outputLimitProvider{steps: []outputLimitStep{
		{calls: []llm.ToolCall{shareNotes}, stop: "tool_use"},
		{text: "Here is the notes file.", stop: "end_turn"},
	}}
	ag, st, sender := cutCallAgent(t, p, nil)
	if stop, err := runOutputLimitTurn(ag); err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop = %q, err = %v", stop, err)
	}
	said := textMessages(st.GetMessages())
	if len(said) != 1 || said[0].Truncated || len(said[0].Artifacts) != 1 {
		t.Fatalf("the transcript holds %+v, want one reply with the shared file", said)
	}
	markers := session.ArtifactMarkers(said[0].Artifacts)
	chunks := agentChunks(sender)
	if len(chunks) == 0 || chunks[len(chunks)-1] != "\n\n"+markers {
		t.Fatalf("chunks = %q, want the reply closed with %q", chunks, "\n\n"+markers)
	}
	if want := "Here is the notes file.\n\n" + markers; said[0].Content != want {
		t.Fatalf("the reply is stored as %q, want %q", said[0].Content, want)
	}
}

// TestBuildMessagesJoinsAContinuedAnswerWithoutItsMarkers: in a transcript in
// which a text the output limit cut off is followed by the continuation that
// completes it, the markers of the two files shared before them close the
// continuation and the truncated text carries none. The history built from that
// transcript strips the markers from every assistant message, keeping the
// whitespace at the join (session.StripAssistantContent), and sends the text and
// its continuation as one message (foldSplitAnswers): "butt" and "on labelled"
// with nothing between them and no marker anywhere in what the model reads.
func TestBuildMessagesJoinsAContinuedAnswerWithoutItsMarkers(t *testing.T) {
	st := &session.State{ID: "sess_markers_join", CWD: t.TempDir(), Mode: session.ModeAgent}
	for _, m := range []llm.Message{
		{Role: llm.RoleUser, Content: "share the notes"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{shareFileCall("one", "one.txt"), shareFileCall("two", "two.txt")}},
		{Role: llm.RoleTool, ToolCallID: "one", Content: "{}", Artifacts: []llm.Artifact{{ID: "artifact-one", Name: "one.txt"}}},
		{Role: llm.RoleTool, ToolCallID: "two", Content: "{}", Artifacts: []llm.Artifact{{ID: "artifact-two", Name: "two.txt"}}},
		{Role: llm.RoleAssistant, Content: "Both files are ready, see the butt", Truncated: true},
		{Role: llm.RoleAssistant, Content: "on labelled Open."},
	} {
		st.AddMessage(m)
	}
	stored := st.GetMessages()
	if head := stored[4]; head.Content != "Both files are ready, see the butt" || len(head.Artifacts) != 0 {
		t.Fatalf("the cut text is stored as %+v, want its text alone", head)
	}
	markers := `<coddy_file id="artifact-one"/>` + "\n" + `<coddy_file id="artifact-two"/>`
	if rest := stored[5]; rest.Content != "on labelled Open.\n\n"+markers || len(rest.Artifacts) != 2 {
		t.Fatalf("the continuation is stored as %+v, want it closed with both markers", rest)
	}

	ag := NewAgent(&config.Config{}, st, resumePermissionSender{}, nil)
	var said []string
	for _, m := range ag.buildMessages("system") {
		if strings.Contains(m.Content, "<coddy_file") {
			t.Fatalf("a marker reaches the model: %+v", m)
		}
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) == 0 {
			said = append(said, m.Content)
		}
	}
	if want := "Both files are ready, see the button labelled Open."; len(said) != 1 || said[0] != want {
		t.Fatalf("the history carries %q, want one message holding %q", said, want)
	}
}
