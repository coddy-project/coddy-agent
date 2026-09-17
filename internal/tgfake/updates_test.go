package tgfake

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

func updates(t *testing.T, body map[string]any) []map[string]any {
	t.Helper()
	if body["ok"] != true {
		t.Fatalf("not ok: %v", body)
	}
	raw, _ := body["result"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, u := range raw {
		out = append(out, u.(map[string]any))
	}
	return out
}

func TestGetUpdates_OffsetSemantics(t *testing.T) {
	s := newStand(t, Options{})
	first, _ := s.fake.InjectMessage(IncomingMessage{Text: "one"})
	second, _ := s.fake.InjectMessage(IncomingMessage{Text: "two"})
	if first != 1 || second != 2 {
		t.Fatalf("update ids %d %d", first, second)
	}

	_, body := s.call("getUpdates", url.Values{"timeout": {"0"}})
	if got := updates(t, body); len(got) != 2 || got[0]["update_id"] != float64(1) {
		t.Fatalf("offset 0: %v", got)
	}
	_, body = s.call("getUpdates", url.Values{"offset": {"2"}, "timeout": {"0"}, "allowed_updates": {"null"}})
	if got := updates(t, body); len(got) != 1 || got[0]["update_id"] != float64(2) {
		t.Fatalf("offset 2: %v", got)
	}
	if s.fake.PendingUpdates() != 1 {
		t.Fatalf("pending after offset 2: %d", s.fake.PendingUpdates())
	}
	_, body = s.call("getUpdates", url.Values{"offset": {"3"}, "timeout": {"0"}})
	if got := updates(t, body); len(got) != 0 {
		t.Fatalf("offset 3: %v", got)
	}
	s.fake.Reset()
	third, _ := s.fake.InjectMessage(IncomingMessage{Text: "after reset"})
	if third != 3 {
		t.Fatalf("update ids restart after Reset: %d", third)
	}
}

func TestGetUpdates_AllowedUpdatesIsRemembered(t *testing.T) {
	// The subscription a previous bot process left behind: messages only.
	s := newStand(t, Options{AllowedUpdates: []string{"message", "edited_message"}})
	s.fake.InjectMessage(IncomingMessage{Text: "menu"})
	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"}, "reply_markup": {`{"inline_keyboard":[[{"text":"Plan","callback_data":"mode:plan"}]]}`}})
	if _, _, err := s.fake.InjectCallback(IncomingCallback{Label: "Plan"}); err != nil {
		t.Fatal(err)
	}
	if got := s.fake.PendingUpdates(); got != 1 {
		t.Fatalf("excluded tap was queued: %d pending updates", got)
	}

	// A poll that names no allowed_updates (the literal null) inherits it:
	// the message arrives, the tap is dropped for good.
	_, body := s.call("getUpdates", url.Values{"timeout": {"0"}, "allowed_updates": {"null"}})
	if got := updates(t, body); len(got) != 1 || got[0]["message"] == nil {
		t.Fatalf("inherited subscription: %v", got)
	}
	if s.fake.PendingUpdates() != 1 {
		t.Fatalf("pending after the drop: %d", s.fake.PendingUpdates())
	}
	if _, _, err := s.fake.InjectCallback(IncomingCallback{Label: "Plan"}); err != nil {
		t.Fatal(err)
	}
	_, body = s.call("getUpdates", url.Values{"offset": {"2"}, "timeout": {"0"}})
	if got := updates(t, body); len(got) != 0 {
		t.Fatalf("tap still filtered without a new subscription: %v", got)
	}

	// Naming the kinds replaces the subscription for future updates only.
	if _, _, err := s.fake.InjectCallback(IncomingCallback{Label: "Plan"}); err != nil {
		t.Fatal(err)
	}
	_, body = s.call("getUpdates", url.Values{"offset": {"2"}, "timeout": {"0"}, "allowed_updates": {`["message","callback_query"]`}})
	if got := updates(t, body); len(got) != 0 {
		t.Fatalf("subscription change recovered an excluded tap: %v", got)
	}
	if _, _, err := s.fake.InjectCallback(IncomingCallback{Label: "Plan"}); err != nil {
		t.Fatal(err)
	}
	_, body = s.call("getUpdates", url.Values{"offset": {"2"}, "timeout": {"0"}})
	if got := updates(t, body); len(got) != 1 || got[0]["callback_query"] == nil {
		t.Fatalf("subscribed tap: %v", got)
	}
	if got := s.fake.AllowedUpdates(); len(got) != 2 {
		t.Fatalf("subscription: %v", got)
	}

	// An empty list is every kind again; a broken one is refused.
	s.call("getUpdates", url.Values{"offset": {"6"}, "timeout": {"0"}, "allowed_updates": {`[]`}})
	if got := s.fake.AllowedUpdates(); got != nil {
		t.Fatalf("empty list should mean everything: %v", got)
	}
	if status, _ := s.call("getUpdates", url.Values{"timeout": {"0"}, "allowed_updates": {`nope`}}); status != http.StatusBadRequest {
		t.Fatalf("bad allowed_updates: %d", status)
	}
}

func TestGetUpdates_LimitAndBatch(t *testing.T) {
	s := newStand(t, Options{})
	for i := 0; i < 5; i++ {
		s.fake.InjectMessage(IncomingMessage{Text: "n"})
	}
	_, body := s.call("getUpdates", url.Values{"limit": {"2"}, "timeout": {"0"}})
	if got := updates(t, body); len(got) != 2 {
		t.Fatalf("limit 2: %d", len(got))
	}
}

func TestGetUpdates_LongPollWakesOnInjection(t *testing.T) {
	s := newStand(t, Options{MaxPollWait: 5 * time.Second})
	done := make(chan []map[string]any, 1)
	go func() {
		_, body := s.call("getUpdates", url.Values{"timeout": {"30"}})
		done <- updates(t, body)
	}()
	time.Sleep(50 * time.Millisecond)
	s.fake.InjectMessage(IncomingMessage{Text: "wake"})
	select {
	case got := <-done:
		if len(got) != 1 || got[0]["message"].(map[string]any)["text"] != "wake" {
			t.Fatalf("woken poll: %v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("long poll did not wake on injection")
	}
}

func TestGetUpdates_MaxPollWaitCapsTimeout(t *testing.T) {
	s := newStand(t, Options{MaxPollWait: 100 * time.Millisecond})
	start := time.Now()
	_, body := s.call("getUpdates", url.Values{"timeout": {"30"}})
	if got := updates(t, body); len(got) != 0 {
		t.Fatalf("empty poll: %v", got)
	}
	if elapsed := time.Since(start); elapsed < 80*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("poll took %v, want about 100ms", elapsed)
	}
}

func TestGetUpdates_CloseReleasesWaiter(t *testing.T) {
	fake := New(Options{MaxPollWait: 10 * time.Second})
	srv := newStandFrom(t, fake)
	done := make(chan int, 1)
	go func() {
		resp, err := http.PostForm(srv+"/bot1/getUpdates", url.Values{"timeout": {"30"}})
		if err != nil {
			done <- -1
			return
		}
		_ = resp.Body.Close()
		done <- resp.StatusCode
	}()
	time.Sleep(50 * time.Millisecond)
	fake.Close()
	select {
	case status := <-done:
		if status != http.StatusOK {
			t.Fatalf("released poll answered %d", status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not release the poll")
	}
}

func TestInjectMessage_Shapes(t *testing.T) {
	s := newStand(t, Options{BotUsername: "unit_bot"})
	s.fake.InjectMessage(IncomingMessage{Text: "/model@unit_bot extra"})
	s.fake.InjectMessage(IncomingMessage{Text: "hello /x"})
	s.fake.InjectMessage(IncomingMessage{Text: "ping", Mention: true, ChatType: "supergroup", UserID: 77, Username: "bob"})
	_, body := s.call("getUpdates", url.Values{"timeout": {"0"}})
	got := updates(t, body)
	if len(got) != 3 {
		t.Fatalf("updates: %d", len(got))
	}

	cmd := got[0]["message"].(map[string]any)
	ents := cmd["entities"].([]any)
	if len(ents) != 1 || ents[0].(map[string]any)["type"] != "bot_command" || ents[0].(map[string]any)["length"] != float64(len("/model@unit_bot")) {
		t.Fatalf("command entity: %v", ents)
	}
	if cmd["chat"].(map[string]any)["id"] != float64(4242) || cmd["chat"].(map[string]any)["type"] != "private" || cmd["from"].(map[string]any)["username"] != "alice" {
		t.Fatalf("private defaults: %v", cmd)
	}

	if _, has := got[1]["message"].(map[string]any)["entities"]; has {
		t.Fatalf("a slash mid-text is no command: %v", got[1])
	}

	grp := got[2]["message"].(map[string]any)
	if grp["text"] != "@unit_bot ping" || grp["chat"].(map[string]any)["type"] != "supergroup" || grp["chat"].(map[string]any)["title"] != defaultGroupName {
		t.Fatalf("group message: %v", grp)
	}
	if grp["chat"].(map[string]any)["id"].(float64) >= 0 {
		t.Fatalf("group chat ids are negative: %v", grp["chat"])
	}
}

func TestInjectMessage_ReplyToBotMessage(t *testing.T) {
	s := newStand(t, Options{BotUsername: "unit_bot"})
	s.fake.InjectMessage(IncomingMessage{Text: "hi"})
	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"answer"}})
	_, id := s.fake.InjectMessage(IncomingMessage{Text: "and?", ReplyToMessageID: 2})
	if id != 3 {
		t.Fatalf("message id %d", id)
	}
	_, body := s.call("getUpdates", url.Values{"offset": {"2"}, "timeout": {"0"}})
	got := updates(t, body)
	reply := got[0]["message"].(map[string]any)["reply_to_message"].(map[string]any)
	if reply["message_id"] != float64(2) || reply["from"].(map[string]any)["username"] != "unit_bot" {
		t.Fatalf("reply_to_message: %v", reply)
	}
}

func TestInjectCallback(t *testing.T) {
	s := newStand(t, Options{})
	if _, _, err := s.fake.InjectCallback(IncomingCallback{Label: "Plan"}); err == nil {
		t.Fatal("callback into an empty chat should fail")
	}
	s.fake.InjectMessage(IncomingMessage{Text: "/mode"})
	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"},
		"reply_markup": {`{"inline_keyboard":[[{"text":"✓ Agent","callback_data":"mode:agent"},{"text":"Plan","callback_data":"mode:plan"}]]}`}})
	if _, _, err := s.fake.InjectCallback(IncomingCallback{Label: "Ask"}); err == nil {
		t.Fatal("missing label should fail")
	}
	upd, cbq, err := s.fake.InjectCallback(IncomingCallback{Label: "✓ Agent"})
	if err != nil || cbq != "cbq-1" {
		t.Fatalf("tap: %v %q", err, cbq)
	}
	_, body := s.call("getUpdates", url.Values{"offset": {itoa(upd)}, "timeout": {"0"}})
	got := updates(t, body)
	q := got[0]["callback_query"].(map[string]any)
	if q["data"] != "mode:agent" || q["id"] != "cbq-1" || q["message"].(map[string]any)["message_id"] != float64(2) || q["from"].(map[string]any)["id"] != float64(4242) {
		t.Fatalf("callback query: %v", q)
	}
	if _, _, err := s.fake.InjectCallback(IncomingCallback{MessageID: 2, Data: "mode:plan"}); err != nil {
		t.Fatalf("explicit data: %v", err)
	}
}

// newStandFrom serves an existing fake and returns its URL; the caller
// closes the fake.
func newStandFrom(t *testing.T, fake *Server) string {
	t.Helper()
	srv := newHTTPTest(fake)
	t.Cleanup(func() {
		fake.Close()
		srv.Close()
	})
	return srv.URL
}
