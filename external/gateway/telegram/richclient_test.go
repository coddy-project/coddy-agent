//go:build gateway || gateway.telegram

package telegram

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
)

func TestRichParams_WireFormat(t *testing.T) {
	p := richParams(12345, "**hi**", 678)
	if p["chat_id"] != "12345" {
		t.Fatalf("chat_id: want 12345 got %q", p["chat_id"])
	}
	var rm map[string]any
	if err := json.Unmarshal([]byte(p["rich_message"]), &rm); err != nil {
		t.Fatalf("rich_message is not valid JSON: %v (%q)", err, p["rich_message"])
	}
	if rm["markdown"] != "**hi**" {
		t.Fatalf("rich_message.markdown: want **hi** got %v", rm["markdown"])
	}
	if _, ok := rm["html"]; ok {
		t.Fatalf("rich_message must not carry html when markdown is used: %q", p["rich_message"])
	}
	var rp map[string]any
	if err := json.Unmarshal([]byte(p["reply_parameters"]), &rp); err != nil {
		t.Fatalf("reply_parameters is not valid JSON: %v (%q)", err, p["reply_parameters"])
	}
	if rp["message_id"] != float64(678) {
		t.Fatalf("reply_parameters.message_id: want 678 got %v", rp["message_id"])
	}
}

func TestRichParams_NoReplyWhenZero(t *testing.T) {
	p := richParams(1, "x", 0)
	if _, ok := p["reply_parameters"]; ok {
		t.Fatalf("reply_parameters must be omitted when replyTo == 0")
	}
}

func TestRichDraftParams_WireFormat(t *testing.T) {
	p := richDraftParams(12345, 99, "wip")
	if p["chat_id"] != "12345" {
		t.Fatalf("chat_id: want 12345 got %q", p["chat_id"])
	}
	if p["draft_id"] != "99" {
		t.Fatalf("draft_id: want 99 got %q", p["draft_id"])
	}
	var rm map[string]any
	if err := json.Unmarshal([]byte(p["rich_message"]), &rm); err != nil {
		t.Fatalf("rich_message is not valid JSON: %v", err)
	}
	if rm["markdown"] != "wip" {
		t.Fatalf("rich_message.markdown: want wip got %v", rm["markdown"])
	}
}

// The fake accepts the bot's token and no other, so an ok answer is the
// /bot<token>/<method> path arriving as the Bot API expects it.
func TestSendRichMessage_PostsExpectedRequest(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{Token: fakeToken})

	resp, err := sendRichMessage(f.api, 5, "# Title", 0)
	if err != nil {
		t.Fatalf("sendRichMessage: %v", err)
	}
	if !resp.Ok {
		t.Fatalf("expected ok response")
	}
	calls := f.fake.Calls("sendRichMessage")
	if len(calls) != 1 || calls[0].Status != http.StatusOK {
		t.Fatalf("sendRichMessage calls: %+v", f.fake.Calls(""))
	}
	if got := calls[0].Params["chat_id"]; got != "5" {
		t.Fatalf("chat_id: want 5 got %q", got)
	}
	var rm map[string]any
	if err := json.Unmarshal([]byte(calls[0].Params["rich_message"]), &rm); err != nil {
		t.Fatalf("rich_message not JSON: %v", err)
	}
	if rm["markdown"] != "# Title" {
		t.Fatalf("markdown: want '# Title' got %v", rm["markdown"])
	}
	// What the chat ends up holding is the message as the agent wrote it.
	if msgs := f.fake.Chat(5).Messages; len(msgs) != 1 || !msgs[0].Rich || msgs[0].Text != "# Title" {
		t.Fatalf("chat: %+v", msgs)
	}
}

func TestSendRichMessageDraft_PostsExpectedRequest(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{Token: fakeToken})

	if err := sendRichMessageDraft(f.api, 5, 42, "partial"); err != nil {
		t.Fatalf("sendRichMessageDraft: %v", err)
	}
	calls := f.fake.Calls("sendRichMessageDraft")
	if len(calls) != 1 || calls[0].Status != http.StatusOK {
		t.Fatalf("sendRichMessageDraft calls: %+v", f.fake.Calls(""))
	}
	if got := calls[0].Params["draft_id"]; got != "42" {
		t.Fatalf("draft_id: want 42 got %q", got)
	}
	if drafts := f.fake.Chat(5).Drafts; len(drafts) != 1 || drafts[0].DraftID != 42 || drafts[0].Markdown != "partial" {
		t.Fatalf("drafts: %+v", drafts)
	}
}

// A token the server does not know is refused, which is what keeps the two
// tests above honest about the path.
func TestSendRichMessage_WrongTokenIsRefused(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{Token: "another-token"})
	if _, err := sendRichMessage(f.api, 5, "x", 0); err == nil {
		t.Fatal("a request under the wrong token was accepted")
	}
}
