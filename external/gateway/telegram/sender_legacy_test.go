//go:build gateway || gateway.telegram

package telegram

// The legacy path of the Sender (rich_messages off): a live message that is
// sent once, edited as the answer grows and replaced by the formatted text at
// the end. Run against the fake Bot API, which refuses what Telegram refuses -
// an edit of a message that was never sent, an edit that changes nothing.

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
)

func chunk(text string) acp.MessageChunkUpdate {
	return acp.MessageChunkUpdate{Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: text}}
}

func TestSender_Legacy_PreviewThenFinalEditInPlace(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	asked := f.userMessage(5, 5, "hi")
	s := newSender(f.api, 5, asked.MessageID, slog.Default(), richConfig{})

	_ = s.SendSessionUpdate("sess", chunk("**Hello** world"))
	s.Flush()

	sent := f.fake.Calls("sendMessage")
	if len(sent) != 1 {
		t.Fatalf("expected one sendMessage for the live preview, got %d", len(sent))
	}
	preview := sent[0].Params
	if preview["parse_mode"] != "" || !strings.HasSuffix(preview["text"], "…") || strings.Contains(preview["text"], "*") {
		t.Fatalf("the preview goes out plain, unfinished and without markers: %v", preview)
	}
	if preview["reply_to_message_id"] != strconv.Itoa(asked.MessageID) {
		t.Fatalf("the preview should answer the user's message: %v", preview)
	}

	edits := f.fake.Calls("editMessageText")
	if len(edits) != 1 || edits[0].Status != http.StatusOK {
		t.Fatalf("expected one accepted final edit, got %+v", edits)
	}
	if edits[0].Params["parse_mode"] != "Markdown" || edits[0].Params["text"] != "*Hello* world" {
		t.Fatalf("the final edit carries the converted Markdown: %v", edits[0].Params)
	}

	// One bot message in the chat: the preview, edited into the answer.
	msgs := f.fake.Chat(5).Messages
	if len(msgs) != 2 || msgs[1].From != "bot" || !msgs[1].Edited || msgs[1].Text != "*Hello* world" || msgs[1].ParseMode != "Markdown" {
		t.Fatalf("chat: %+v", msgs)
	}
	if edits[0].Params["message_id"] != strconv.Itoa(msgs[1].MessageID) {
		t.Fatalf("the edit should target the live message %d: %v", msgs[1].MessageID, edits[0].Params)
	}
}

func TestSender_Legacy_NoAnswerRemovesThePlaceholder(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	s := newSender(f.api, 5, 0, slog.Default(), richConfig{})

	// A tool runs, the model never writes a word.
	_ = s.SendSessionUpdate("sess", acp.ToolCallUpdate{ToolCallID: "t1", Title: "bash"})
	if msgs := f.fake.Chat(5).Messages; len(msgs) != 1 || !strings.Contains(msgs[0].Text, "bash") {
		t.Fatalf("the tool indicator should be the live message: %+v", msgs)
	}
	if !f.fake.Chat(5).Typing {
		t.Fatal("a running tool should refresh the typing action")
	}
	s.Flush()

	if got := f.fake.Calls("deleteMessage"); len(got) != 1 || got[0].Status != http.StatusOK {
		t.Fatalf("expected the placeholder to be deleted, got %+v", got)
	}
	if msgs := f.fake.Chat(5).Messages; len(msgs) != 1 || !msgs[0].Deleted {
		t.Fatalf("the chat should be left empty: %+v", msgs)
	}
	if len(f.fake.Calls("editMessageText")) != 0 {
		t.Fatal("nothing to edit when the model said nothing")
	}
}

func TestSender_Legacy_RejectedMarkdownIsRetriedPlain(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	f.fake.SetFault(tgfake.Fault{Method: "editMessageText", Code: http.StatusBadRequest,
		Description: "Bad Request: can't parse entities", Times: 1})
	s := newSender(f.api, 5, 0, slog.Default(), richConfig{})

	_ = s.SendSessionUpdate("sess", chunk("**bold** move"))
	s.Flush()

	edits := f.fake.Calls("editMessageText")
	if len(edits) != 2 {
		t.Fatalf("expected the Markdown edit and a plain retry, got %d: %+v", len(edits), edits)
	}
	if edits[0].Status != http.StatusBadRequest || edits[0].Params["parse_mode"] != "Markdown" {
		t.Fatalf("first edit: %d %v", edits[0].Status, edits[0].Params)
	}
	if edits[1].Status != http.StatusOK || edits[1].Params["parse_mode"] != "" || edits[1].Params["text"] != "bold move" {
		t.Fatalf("the retry goes out plain with the markers stripped: %d %v", edits[1].Status, edits[1].Params)
	}
	if msgs := f.fake.Chat(5).Messages; len(msgs) != 1 || msgs[0].Text != "bold move" || msgs[0].ParseMode != "" {
		t.Fatalf("the answer must survive the rejection: %+v", msgs)
	}
}

// A message another client queued is the operator's, not the bot's: the frame
// that announces it where the turn read it - or where its own prompt starts -
// never becomes part of the answer the chat is sent.
func TestSender_OperatorMessagesStayOutOfTheAnswer(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	s := newSender(f.api, 5, 0, slog.Default(), richConfig{})

	_ = s.SendSessionUpdate("sess", chunk("First answer."))
	_ = s.SendSessionUpdate("sess", acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeUserMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: "then update the changelog"},
	})
	_ = s.SendSessionUpdate("sess", chunk(" Done."))
	s.Flush()

	msgs := f.fake.Chat(5).Messages
	var bot []string
	for _, m := range msgs {
		if strings.Contains(m.Text, "changelog") {
			t.Fatalf("the operator's message reached the bot's answer: %+v", msgs)
		}
		if m.From == "bot" && !m.Deleted {
			bot = append(bot, m.Text)
		}
	}
	if len(bot) != 1 || bot[0] != "First answer. Done." {
		t.Fatalf("the answer should be the agent's words only: %q", bot)
	}
}
