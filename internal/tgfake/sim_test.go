package tgfake

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func newHTTPTest(fake *Server) *httptest.Server {
	return httptest.NewServer(fake.Handler())
}

func TestSim_MessageAndCallbackRoutes(t *testing.T) {
	s := newStand(t, Options{})
	status, body := s.sim("POST", "/sim/message", map[string]any{"text": "/model"})
	if status != http.StatusOK || body["update_id"] != float64(1) || body["message_id"] != float64(1) {
		t.Fatalf("message: %d %v", status, body)
	}
	if status, _ := s.sim("POST", "/sim/message", map[string]any{"text": "  "}); status != http.StatusBadRequest {
		t.Fatalf("empty text: %d", status)
	}
	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"},
		"reply_markup": {`{"inline_keyboard":[[{"text":"rpa/x","callback_data":"model:rpa/x"}]]}`}})
	status, body = s.sim("POST", "/sim/callback", map[string]any{"label": "rpa/x"})
	if status != http.StatusOK || body["callback_query_id"] != "cbq-1" {
		t.Fatalf("callback: %d %v", status, body)
	}
	if status, _ := s.sim("POST", "/sim/callback", map[string]any{"label": "nope"}); status != http.StatusNotFound {
		t.Fatalf("missing button: %d", status)
	}
	if status, _ := s.sim("POST", "/sim/callback", map[string]any{"chat_id": 4242}); status != http.StatusBadRequest {
		t.Fatalf("no data or label: %d", status)
	}
}

func TestSim_OutboxChatAndState(t *testing.T) {
	s := newStand(t, Options{BotUsername: "unit_bot"})
	s.sim("POST", "/sim/message", map[string]any{"text": "hello"})
	s.call("sendChatAction", url.Values{"chat_id": {"4242"}, "action": {"typing"}})
	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"You said: hello"}, "reply_markup": {`{"inline_keyboard":[[{"text":"Again","callback_data":"x"}]]}`}})
	s.call("sendRichMessageDraft", url.Values{"chat_id": {"4242"}, "draft_id": {"3"}, "rich_message": {`{"markdown":"half"}`}})

	_, body := s.sim("GET", "/sim/outbox?method=sendMessage", nil)
	calls := body["calls"].([]any)
	if len(calls) != 1 || calls[0].(map[string]any)["params"].(map[string]any)["text"] != "You said: hello" {
		t.Fatalf("outbox filter: %v", calls)
	}
	_, body = s.sim("GET", "/sim/outbox?since=2", nil)
	if got := len(body["calls"].([]any)); got != 1 {
		t.Fatalf("outbox since: %d", got)
	}
	_, body = s.sim("GET", "/sim/outbox/count?method=sendchataction", nil)
	if body["count"] != float64(1) {
		t.Fatalf("count: %v", body)
	}

	resp, err := http.Get(s.srv.URL + "/sim/chat/4242?format=text")
	if err != nil {
		t.Fatal(err)
	}
	text, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	for _, want := range []string{"[1] alice: hello", "[2] bot: You said: hello", "[Again]", "draft 3 (rev 1): half", "typing…"} {
		if !strings.Contains(string(text), want) {
			t.Fatalf("text transcript lacks %q:\n%s", want, text)
		}
	}
	_, body = s.sim("GET", "/sim/chat/4242", nil)
	if body["typing"] != true || len(body["messages"].([]any)) != 2 {
		t.Fatalf("chat json: %v", body)
	}
	if status, _ := s.sim("GET", "/sim/chat/x", nil); status != http.StatusBadRequest {
		t.Fatalf("bad chat id: %d", status)
	}
	_, body = s.sim("GET", "/sim/chats", nil)
	if chats := body["chats"].([]any); len(chats) != 1 || chats[0].(map[string]any)["title"] != "@alice" {
		t.Fatalf("chats: %v", body)
	}
	_, body = s.sim("GET", "/sim/state", nil)
	if body["bot"].(map[string]any)["username"] != "unit_bot" || body["pending_updates"] != float64(1) {
		t.Fatalf("state: %v", body)
	}
	if status, _ := s.sim("POST", "/sim/reset", nil); status != http.StatusOK || len(s.fake.Calls("")) != 0 {
		t.Fatal("reset")
	}
}

func TestSim_FaultRoutes(t *testing.T) {
	s := newStand(t, Options{})
	status, _ := s.sim("POST", "/sim/fault", map[string]any{"method": "sendMessage", "code": 429, "retry_after": 2, "times": 1})
	if status != http.StatusOK {
		t.Fatalf("set fault: %d", status)
	}
	status, body := s.call("sendMessage", url.Values{"chat_id": {"1"}, "text": {"x"}})
	if status != 429 || body["parameters"].(map[string]any)["retry_after"] != float64(2) || !strings.Contains(body["description"].(string), "retry after 2") {
		t.Fatalf("429: %d %v", status, body)
	}
	if status, _ := s.call("sendMessage", url.Values{"chat_id": {"1"}, "text": {"x"}}); status != http.StatusOK {
		t.Fatalf("fault should have cleared after one hit: %d", status)
	}

	s.sim("POST", "/sim/fault", map[string]any{"method": "*", "code": 502})
	if status, _ := s.call("getMe", nil); status != 502 {
		t.Fatalf("catch-all: %d", status)
	}
	if status, _ := s.call("getMe", nil); status != 502 {
		t.Fatal("times 0 should persist")
	}
	s.sim("POST", "/sim/fault", map[string]any{"method": "*", "clear": true})
	if status, _ := s.call("getMe", nil); status != http.StatusOK {
		t.Fatalf("cleared: %d", status)
	}
	// A fault tied to the payload: only a call carrying the text is refused,
	// and a call that does not match leaves the count alone.
	s.sim("POST", "/sim/fault", map[string]any{"method": "sendMessage", "code": 400, "description": "Bad Request: can't parse entities", "contains": "<details>", "times": 1})
	if status, _ := s.call("sendMessage", url.Values{"chat_id": {"1"}, "text": {"plain"}}); status != http.StatusOK {
		t.Fatalf("a call without the text should pass: %d", status)
	}
	if got := s.fake.Faults(); len(got) != 1 || got[0].Times != 1 {
		t.Fatalf("a non-matching call must not count the fault down: %+v", got)
	}
	status, body = s.call("sendMessage", url.Values{"chat_id": {"1"}, "text": {"answer <details>tool</details>"}})
	if status != http.StatusBadRequest || !strings.Contains(body["description"].(string), "parse entities") {
		t.Fatalf("matching call: %d %v", status, body)
	}
	if status, _ := s.call("sendMessage", url.Values{"chat_id": {"1"}, "text": {"<details> again"}}); status != http.StatusOK {
		t.Fatalf("the fault should have cleared after its one hit: %d", status)
	}

	s.fake.SetFault(Fault{Method: "getMe", Code: 500})
	if status, _ := s.sim("DELETE", "/sim/fault", nil); status != http.StatusOK || len(s.fake.Faults()) != 0 {
		t.Fatal("delete all")
	}
	if status, _ := s.sim("POST", "/sim/fault", nil); status != http.StatusBadRequest {
		t.Fatalf("empty body: %d", status)
	}
}
