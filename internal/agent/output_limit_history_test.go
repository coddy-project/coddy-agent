package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A step that came back empty stays in the transcript, where the user watched
// it think. The history a later request replays from that transcript must not
// carry it: an assistant turn with nothing to say is no turn of the
// conversation, and two assistant messages in a row are refused by chat
// templates that insist on alternation.

// TestEmptyAssistantStepsAreNotReplayed pins what buildMessages drops: an
// assistant message with no text, no tool call and no signed reasoning, whatever
// unsigned thinking it holds. Signed reasoning is what Anthropic, Codex and
// Devin replay, so a message that carries it stays.
func TestEmptyAssistantStepsAreNotReplayed(t *testing.T) {
	st := &session.State{ID: "sess_replay", CWD: t.TempDir(), Mode: session.ModeAgent}
	for _, m := range []llm.Message{
		{Role: llm.RoleUser, Content: "write the module"},
		{Role: llm.RoleAssistant, Reasoning: "thinking that was cut off"},
		{Role: llm.RoleAssistant, Content: " \n\t"},
		{Role: llm.RoleAssistant, Content: "On it.", ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "coddy_todo_plan_read", InputJSON: "{}"}}},
		{Role: llm.RoleTool, Content: "no plan", ToolCallID: "call_1"},
		{Role: llm.RoleAssistant, Reasoning: "signed", ReasoningSignature: "sig"},
		{Role: llm.RoleAssistant, Content: "Done."},
	} {
		st.AddMessage(m)
	}
	ag := NewAgent(&config.Config{Agent: config.Agent{Model: "fake/model"}}, st, resumePermissionSender{}, nil)
	got := requestHistory(ag.buildMessages("SYSTEM"))
	want := []string{
		"system: SYSTEM",
		"user: write the module",
		"assistant: On it.",
		"tool: no plan",
		"assistant: ",
		"assistant: Done.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("replayed history:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// TestPromptCacheNextTurnReplaysNoEmptyStep: after a turn that recovered from a
// cut-off step, the next turn's request is the conversation as the model saw it
// in the turn itself - the user's prompt, the tool round, the answer - with no
// empty assistant turn in it and never two assistant messages in a row. Every
// request leaves the step out by the same rule, so the prefix a provider cached
// during the turn is still the prefix on the next one.
func TestPromptCacheNextTurnReplaysNoEmptyStep(t *testing.T) {
	read := llm.ToolCall{ID: "call_read", Name: "coddy_todo_plan_read", InputJSON: "{}"}
	p := &outputLimitProvider{steps: []outputLimitStep{
		cutOff,
		{text: "Reading the plan.", calls: []llm.ToolCall{read}, stop: "tool_use"},
		answered,
		{text: "Tested.", stop: "end_turn"},
	}}
	ag, st := outputLimitAgent(t, p, nil)

	if stop, err := runOutputLimitTurn(ag); err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("first turn: stop = %q, err = %v", stop, err)
	}
	if p.calls != 3 {
		t.Fatalf("first turn: provider called %d times, want 3", p.calls)
	}
	var cut bool
	for _, m := range st.GetMessages() {
		if m.Role == llm.RoleAssistant && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0 && strings.Contains(m.Reasoning, "whole file first") {
			cut = true
		}
	}
	if !cut {
		t.Fatal("the transcript lost the cut-off step")
	}

	if stop, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "now test it"}}); err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("second turn: stop = %q, err = %v", stop, err)
	}
	if p.calls != 4 {
		t.Fatalf("second turn: provider called %d times in all, want 4", p.calls)
	}
	hist := requestHistory(p.seen[3])
	want := []string{
		"user: write the module",
		"assistant: Reading the plan.",
		"tool: ",
		"assistant: Done.",
		"user: now test it",
	}
	// The system prompt first, then the conversation; the tool result's text is
	// whatever the plan tool answered, so only its place is compared.
	if len(hist) != len(want)+1 {
		t.Fatalf("second turn replays %d messages, want %d:\n%s", len(hist), len(want)+1, strings.Join(hist, "\n"))
	}
	for i, w := range want {
		if !strings.HasPrefix(hist[i+1], strings.TrimRight(w, " ")) {
			t.Fatalf("message %d = %q, want it to start with %q", i+1, hist[i+1], w)
		}
	}
	for i := 1; i < len(p.seen[3]); i++ {
		if p.seen[3][i].Role == llm.RoleAssistant && p.seen[3][i-1].Role == llm.RoleAssistant {
			t.Fatalf("two assistant messages in a row at %d: %q", i, hist)
		}
	}
}
