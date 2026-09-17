package tgfake

import (
	"net/http"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func TestRichDraftLifetime(t *testing.T) {
	s := newStand(t, Options{})
	start := time.Now()
	var elapsed atomic.Int64
	s.fake.now = func() time.Time { return start.Add(time.Duration(elapsed.Load())) }
	send := func(id, text string) {
		t.Helper()
		status, body := s.call("sendRichMessageDraft", url.Values{
			"chat_id": {"4242"}, "draft_id": {id}, "rich_message": {`{"markdown":"` + text + `"}`},
		})
		if status != http.StatusOK || body["ok"] != true {
			t.Fatalf("draft rejected: %d %v", status, body)
		}
	}

	send("1", "first")
	elapsed.Store(int64(20 * time.Second))
	send("1", "revised")
	send("2", "second")
	s.call("sendRichMessage", url.Values{"chat_id": {"4242"}, "rich_message": {`{"markdown":"final"}`}})
	elapsed.Store(int64(50*time.Second - time.Nanosecond))
	view := s.fake.Chat(4242)
	if len(view.Drafts) != 2 || view.Drafts[0].Revisions != 2 || view.Drafts[0].Markdown != "revised" {
		t.Fatalf("revision did not renew the lifetime: %+v", view.Drafts)
	}
	elapsed.Add(1)
	view = s.fake.Chat(4242)
	if len(view.Drafts) != 0 || len(view.Messages) != 1 || view.Messages[0].Text != "final" {
		t.Fatalf("expiry must remove only drafts: %+v", view)
	}
	s.fake.mu.Lock()
	remaining := len(s.fake.chats[4242].drafts)
	s.fake.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("read retained %d expired drafts in storage", remaining)
	}
	if len(s.fake.Calls("sendRichMessageDraft")) != 3 {
		t.Fatal("expiry removed the debugging history")
	}
}

func TestRichDraftWritePrunesWithoutChatReads(t *testing.T) {
	s := newStand(t, Options{})
	start := time.Now()
	var elapsed atomic.Int64
	s.fake.now = func() time.Time { return start.Add(time.Duration(elapsed.Load())) }
	for _, id := range []string{"1", "2"} {
		s.call("sendRichMessageDraft", url.Values{
			"chat_id": {"4242"}, "draft_id": {id}, "rich_message": {`{"markdown":"partial"}`},
		})
	}
	elapsed.Store(int64(30 * time.Second))
	s.call("sendRichMessageDraft", url.Values{
		"chat_id": {"4242"}, "draft_id": {"1"}, "rich_message": {`{"markdown":"new preview"}`},
	})
	s.fake.mu.Lock()
	defer s.fake.mu.Unlock()
	drafts := s.fake.chats[4242].drafts
	if len(drafts) != 1 || drafts[1] == nil || drafts[1].revisions != 1 {
		t.Fatalf("write must prune expired previews before reusing an id: %+v", drafts)
	}
}
