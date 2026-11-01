package agent

import (
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func historyWithCalls(ids ...string) []llm.Message {
	var msgs []llm.Message
	for _, id := range ids {
		msgs = append(msgs,
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: id, Name: "read", InputJSON: `{}`}}},
			llm.Message{Role: llm.RoleTool, ToolCallID: id, Content: "ok"},
		)
	}
	return msgs
}

func callIDsOf(calls []llm.ToolCall) []string {
	out := make([]string, 0, len(calls))
	for _, tc := range calls {
		out = append(out, tc.ID)
	}
	return out
}

func sameIDs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestToolCallIDsKeepAnIDNoCallCarries(t *testing.T) {
	ids := newToolCallIDs(historyWithCalls("call_1"), t.TempDir())
	got := ids.calls([]llm.ToolCall{{ID: "call_2", Name: "read"}})
	if want := []string{"call_2"}; !sameIDs(callIDsOf(got), want) {
		t.Fatalf("ids = %v, want %v", callIDsOf(got), want)
	}
}

func TestToolCallIDsSuffixAnIDTheHistoryCarries(t *testing.T) {
	ids := newToolCallIDs(historyWithCalls("functions.read:0"), "")
	got := ids.calls([]llm.ToolCall{{ID: "functions.read:0", Name: "read"}})
	if want := []string{"functions.read:0_2"}; !sameIDs(callIDsOf(got), want) {
		t.Fatalf("ids = %v, want %v", callIDsOf(got), want)
	}
}

// A compaction folds old calls out of the history and a restart starts from
// what is on disk; the record under tool_calls/ still holds the id, and the
// row a client drew for it is still in the transcript.
func TestToolCallIDsSuffixAnIDOnlyTheBundleRecords(t *testing.T) {
	dir := t.TempDir()
	if err := session.WriteToolCallMeta(dir, "call_7", session.ToolCallMeta{Name: "read", Status: "completed"}); err != nil {
		t.Fatal(err)
	}
	ids := newToolCallIDs(nil, dir)
	got := ids.calls([]llm.ToolCall{{ID: "call_7", Name: "read"}})
	if want := []string{"call_7_2"}; !sameIDs(callIDsOf(got), want) {
		t.Fatalf("ids = %v, want %v", callIDsOf(got), want)
	}
}

func TestToolCallIDsSkipSuffixesAlreadyTaken(t *testing.T) {
	ids := newToolCallIDs(historyWithCalls("x", "x_2"), "")
	got := ids.calls([]llm.ToolCall{{ID: "x", Name: "read"}})
	if want := []string{"x_3"}; !sameIDs(callIDsOf(got), want) {
		t.Fatalf("ids = %v, want %v", callIDsOf(got), want)
	}
}

func TestToolCallIDsSeparateTwoCallsOfOneResponse(t *testing.T) {
	ids := newToolCallIDs(nil, "")
	got := ids.calls([]llm.ToolCall{
		{ID: "dup", Name: "read", InputJSON: `{"path":"a"}`},
		{ID: "dup", Name: "read", InputJSON: `{"path":"b"}`},
		{ID: "other", Name: "read"},
	})
	if want := []string{"dup", "dup_2", "other"}; !sameIDs(callIDsOf(got), want) {
		t.Fatalf("ids = %v, want %v", callIDsOf(got), want)
	}
	for _, issue := range session.ValidateToolPairing([]llm.Message{{Role: llm.RoleAssistant, ToolCalls: got}}) {
		if issue.Kind == "duplicate_call_id" {
			t.Fatalf("the batch still carries a duplicate id: %+v", issue)
		}
	}
}

// The name, the argument fragments and the complete call of one streamed call
// all carry the id the call ends up with, so a client updates one row.
func TestToolCallIDsGiveEveryChunkOfACallTheSameID(t *testing.T) {
	ids := newToolCallIDs(historyWithCalls("functions.write:0"), "")
	provided := llm.ToolCall{ID: "functions.write:0", Name: "write", InputJSON: `{"path":"b.txt"}`}
	named := ids.chunk(llm.StreamChunk{ToolCallNamed: &llm.ToolCall{ID: provided.ID, Name: "write"}})
	delta := ids.chunk(llm.StreamChunk{ToolCallDelta: &llm.ToolCall{ID: provided.ID, InputJSON: `{"pa`}})
	complete := ids.chunk(llm.StreamChunk{ToolCall: &provided})
	final := ids.calls([]llm.ToolCall{provided})

	want := "functions.write:0_2"
	for name, got := range map[string]string{
		"named":    named.ToolCallNamed.ID,
		"delta":    delta.ToolCallDelta.ID,
		"complete": complete.ToolCall.ID,
		"response": final[0].ID,
	} {
		if got != want {
			t.Errorf("%s chunk id = %q, want %q", name, got, want)
		}
	}
	if provided.ID != "functions.write:0" {
		t.Fatalf("the provider's call was changed in place: %q", provided.ID)
	}
}

func TestToolCallIDsLeaveAnEmptyIDAlone(t *testing.T) {
	ids := newToolCallIDs(historyWithCalls(""), "")
	got := ids.calls([]llm.ToolCall{{ID: "", Name: "read"}, {ID: " ", Name: "read"}})
	if want := []string{"", " "}; !sameIDs(callIDsOf(got), want) {
		t.Fatalf("ids = %q, want %q", callIDsOf(got), want)
	}
	if c := ids.chunk(llm.StreamChunk{TextDelta: "hi"}); c.TextDelta != "hi" || c.ToolCall != nil {
		t.Fatalf("a chunk without a call came back as %+v", c)
	}
}
