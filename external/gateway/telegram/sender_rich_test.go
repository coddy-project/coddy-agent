//go:build gateway || gateway.telegram

package telegram

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
)

func TestSender_RichFlow_DraftsThenFinalizesWithTools(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	asked := f.userMessage(5, 5, "say hello")

	s := newSender(f.api, 5, asked.MessageID, slog.Default(), richConfig{
		enabled:    true,
		allowDraft: true,
		draftID:    7,
	})

	// Stream some text, run a tool (with its result), then the final answer.
	_ = s.SendSessionUpdate("sess", acp.MessageChunkUpdate{
		Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: "Hello "},
	})
	_ = s.SendSessionUpdate("sess", acp.ToolCallUpdate{ToolCallID: "t1", Title: "bash"})
	_ = s.SendSessionUpdate("sess", acp.ToolCallStatusUpdate{
		ToolCallID: "t1", Status: "completed",
		Content: []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: "exit 0"}}},
	})
	_ = s.SendSessionUpdate("sess", acp.MessageChunkUpdate{
		Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: "world"},
	})
	s.Flush()

	// At least one ephemeral draft was streamed to the right draft_id.
	drafts := f.fake.Calls("sendRichMessageDraft")
	if len(drafts) == 0 {
		t.Fatalf("expected at least one sendRichMessageDraft call")
	}
	if got := drafts[0].Params["draft_id"]; got != "7" {
		t.Fatalf("draft_id: want 7 got %q", got)
	}

	// Exactly one persistent sendRichMessage finalized the turn.
	finals := f.fake.Calls("sendRichMessage")
	if len(finals) != 1 {
		t.Fatalf("expected exactly one sendRichMessage, got %d", len(finals))
	}
	rm := finals[0].Params["rich_message"]
	if !strings.Contains(rm, "Hello world") {
		t.Fatalf("final message should contain the LLM text, got: %s", rm)
	}
	if !strings.Contains(rm, "details") || !strings.Contains(rm, "bash") {
		t.Fatalf("final message should contain a tools <details> block listing bash, got: %s", rm)
	}
	if !strings.Contains(rm, "exit 0") {
		t.Fatalf("final message should contain the captured tool output, got: %s", rm)
	}
	// The legacy live message path must not be used in rich mode.
	if len(f.fake.Calls("editMessageText")) != 0 || len(f.fake.Calls("sendMessage")) != 0 {
		t.Fatalf("rich mode must not use the legacy send and edit path")
	}
	// The chat holds the question and one rich answer threaded under it.
	msgs := f.fake.Chat(5).Messages
	if len(msgs) != 2 || !msgs[1].Rich || msgs[1].ReplyToMessageID != asked.MessageID {
		t.Fatalf("chat: %+v", msgs)
	}
}

// Regression for "after using tools, the assistant reply does not appear": if the
// combined message (answer + tool blocks) is rejected, the Sender retries with the
// answer alone so the reply is never lost.
func TestSender_RichFlow_AnswerSurvivesToolBlockRejection(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	// Telegram rejects the message while it carries a tool <details> block.
	f.fake.SetFault(tgfake.Fault{Method: "sendRichMessage", Code: http.StatusBadRequest, Description: "bad rich entity", Contains: "details"})

	s := newSender(f.api, 5, 0, slog.Default(), richConfig{enabled: true, allowDraft: false})
	_ = s.SendSessionUpdate("sess", acp.ToolCallUpdate{ToolCallID: "t1", Title: "bash"})
	_ = s.SendSessionUpdate("sess", acp.MessageChunkUpdate{
		Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: "The answer is 42"},
	})
	s.Flush()

	sends := f.fake.Calls("sendRichMessage")
	if len(sends) != 2 {
		t.Fatalf("expected two sendRichMessage attempts (combined, then answer-only), got %d: %+v", len(sends), sends)
	}
	first, retry := sends[0].Params["rich_message"], sends[1].Params["rich_message"]
	if sends[0].Status != http.StatusBadRequest || !strings.Contains(first, "details") {
		t.Fatalf("first attempt should be the combined message, refused: %d %s", sends[0].Status, first)
	}
	if sends[1].Status != http.StatusOK || strings.Contains(retry, "details") || !strings.Contains(retry, "The answer is 42") {
		t.Fatalf("retry should be the answer alone (no tool blocks), accepted: %d %s", sends[1].Status, retry)
	}
	if msgs := f.fake.Chat(5).Messages; len(msgs) != 1 || msgs[0].Text != "The answer is 42" {
		t.Fatalf("the chat should hold the answer once: %+v", msgs)
	}
}

func TestSender_RichGroup_NoDraftButFinalizes(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	asked := f.userMessage(-100, 5, "question")

	// Group chat: allowDraft is false (drafts are private-only).
	s := newSender(f.api, -100, asked.MessageID, slog.Default(), richConfig{
		enabled:    true,
		allowDraft: false,
		draftID:    9,
	})
	_ = s.SendSessionUpdate("sess", acp.MessageChunkUpdate{
		Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: "Answer"},
	})
	s.Flush()

	if got := len(f.fake.Calls("sendRichMessageDraft")); got != 0 {
		t.Fatalf("group chat must not stream drafts, got %d", got)
	}
	if got := len(f.fake.Calls("sendRichMessage")); got != 1 {
		t.Fatalf("expected one sendRichMessage in group, got %d", got)
	}
}
