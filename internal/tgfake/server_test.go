package tgfake

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

type stand struct {
	t    *testing.T
	fake *Server
	srv  *httptest.Server
}

func newStand(t *testing.T, opts Options) *stand {
	t.Helper()
	if opts.MaxPollWait == 0 {
		opts.MaxPollWait = 200 * time.Millisecond
	}
	fake := New(opts)
	srv := httptest.NewServer(fake.Handler())
	t.Cleanup(func() {
		fake.Close()
		srv.Close()
	})
	return &stand{t: t, fake: fake, srv: srv}
}

// call posts a urlencoded form the way a Bot API library does.
func (s *stand) call(method string, form url.Values) (int, map[string]any) {
	s.t.Helper()
	return s.callToken("123456:TOKEN", method, form)
}

func (s *stand) callToken(token, method string, form url.Values) (int, map[string]any) {
	s.t.Helper()
	resp, err := http.Post(s.srv.URL+"/bot"+token+"/"+method, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
	if err != nil {
		s.t.Fatalf("%s: %v", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		s.t.Fatalf("%s: body %q is not JSON: %v", method, body, err)
	}
	return resp.StatusCode, out
}

func (s *stand) sim(method, path string, body any) (int, map[string]any) {
	s.t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = strings.NewReader(string(raw))
	}
	req, _ := http.NewRequest(method, s.srv.URL+path, reader)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func result(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	if body["ok"] != true {
		t.Fatalf("not ok: %v", body)
	}
	res, _ := body["result"].(map[string]any)
	return res
}

func TestGetMe_GETAndPOST(t *testing.T) {
	s := newStand(t, Options{BotUsername: "unit_bot"})
	resp, err := http.Get(s.srv.URL + "/bot1/getMe")
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	_ = resp.Body.Close()
	if got := result(t, body)["username"]; got != "unit_bot" {
		t.Fatalf("GET getMe username = %v", got)
	}
	_, body = s.call("getMe", nil)
	if got := result(t, body)["username"]; got != "unit_bot" {
		t.Fatalf("POST getMe username = %v", got)
	}
	if got := len(s.fake.Calls("getMe")); got != 2 {
		t.Fatalf("outbox holds %d getMe calls, want 2", got)
	}
}

func TestToken_Mismatch(t *testing.T) {
	s := newStand(t, Options{Token: "right"})
	status, body := s.callToken("wrong", "getMe", nil)
	if status != http.StatusUnauthorized || body["error_code"] != float64(401) {
		t.Fatalf("wrong token: %d %v", status, body)
	}
	status, _ = s.callToken("right", "getMe", nil)
	if status != http.StatusOK {
		t.Fatalf("right token: %d", status)
	}
}

func TestUnknownMethod_404Envelope(t *testing.T) {
	s := newStand(t, Options{})
	status, body := s.call("sendPhoto", url.Values{"chat_id": {"1"}})
	if status != http.StatusNotFound || body["ok"] != false || !strings.Contains(body["description"].(string), "method not found") {
		t.Fatalf("unknown method: %d %v", status, body)
	}
}

func TestKeyboard_CallbackDataLimit(t *testing.T) {
	s := newStand(t, Options{})
	long := strings.Repeat("x", 65)
	status, body := s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"},
		"reply_markup": {`{"inline_keyboard":[[{"text":"A","callback_data":"` + long + `"}]]}`}})
	if status != http.StatusBadRequest || !strings.Contains(body["description"].(string), "BUTTON_DATA_INVALID") {
		t.Fatalf("65-byte callback_data: %d %v", status, body)
	}
	if len(s.fake.Chat(4242).Messages) != 0 {
		t.Fatal("a refused message must not reach the chat")
	}
	status, _ = s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"},
		"reply_markup": {`{"inline_keyboard":[[{"text":"A","callback_data":"` + long[:64] + `"}]]}`}})
	if status != http.StatusOK {
		t.Fatalf("64-byte callback_data: %d", status)
	}
	status, body = s.call("editMessageText", url.Values{"chat_id": {"4242"}, "message_id": {"1"}, "text": {"menu 2"},
		"reply_markup": {`{"inline_keyboard":[[{"text":"no action"}]]}`}})
	if status != http.StatusBadRequest || !strings.Contains(body["description"].(string), "BUTTON") {
		t.Fatalf("a button with nothing behind it: %d %v", status, body)
	}
	status, _ = s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"link"},
		"reply_markup": {`{"inline_keyboard":[[{"text":"Docs","url":"https://coddy.dev"}]]}`}})
	if status != http.StatusOK {
		t.Fatalf("a url button: %d", status)
	}
}

func TestChatView_FindButton(t *testing.T) {
	s := newStand(t, Options{})
	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"old menu"},
		"reply_markup": {`{"inline_keyboard":[[{"text":"Plan","callback_data":"old:plan"}]]}`}})
	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"new menu"},
		"reply_markup": {`{"inline_keyboard":[[{"text":"✓ Agent","callback_data":"mode:agent"}],[{"text":"Plan","callback_data":"mode:plan"}]]}`}})
	view := s.fake.Chat(4242)
	if id, data, ok := view.FindButton("Plan"); !ok || id != 2 || data != "mode:plan" {
		t.Fatalf("newest keyboard wins: %d %q %v", id, data, ok)
	}
	if id, data, ok := view.FindButton("Agent"); !ok || id != 2 || data != "mode:agent" {
		t.Fatalf("check mark ignored: %d %q %v", id, data, ok)
	}
	if _, _, ok := view.FindButton("Ask"); ok {
		t.Fatal("a button nobody offered was found")
	}
	s.call("deleteMessage", url.Values{"chat_id": {"4242"}, "message_id": {"2"}})
	if id, data, ok := s.fake.Chat(4242).FindButton("Plan"); !ok || id != 1 || data != "old:plan" {
		t.Fatalf("a deleted keyboard is skipped: %d %q %v", id, data, ok)
	}
}

func TestSendMessage_IdsAndTranscript(t *testing.T) {
	s := newStand(t, Options{})
	_, first := s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"one"}})
	_, second := s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"two"}, "parse_mode": {"Markdown"},
		"reply_to_message_id": {"1"}, "reply_markup": {`{"inline_keyboard":[[{"text":"✓ Agent","callback_data":"mode:agent"},{"text":"Plan","callback_data":"mode:plan"}]]}`}})
	if result(t, first)["message_id"] != float64(1) || result(t, second)["message_id"] != float64(2) {
		t.Fatalf("ids: %v %v", first, second)
	}
	res := result(t, second)
	if res["reply_to_message"].(map[string]any)["text"] != "one" {
		t.Fatalf("reply_to_message: %v", res)
	}
	_, other := s.call("sendMessage", url.Values{"chat_id": {"5"}, "text": {"elsewhere"}})
	if result(t, other)["message_id"] != float64(1) {
		t.Fatalf("ids are per chat: %v", other)
	}
	view := s.fake.Chat(4242)
	if len(view.Messages) != 2 || view.Messages[1].ParseMode != "Markdown" || view.Messages[1].ReplyToMessageID != 1 {
		t.Fatalf("view: %+v", view.Messages)
	}
	if kb := view.Messages[1].Keyboard; len(kb) != 1 || kb[0][1].CallbackData != "mode:plan" {
		t.Fatalf("keyboard: %+v", kb)
	}
	status, body := s.call("sendMessage", url.Values{"chat_id": {"4242"}})
	if status != http.StatusBadRequest || !strings.Contains(body["description"].(string), "text is empty") {
		t.Fatalf("empty text: %d %v", status, body)
	}
}

func TestEditMessageText(t *testing.T) {
	s := newStand(t, Options{})
	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"draft…"}})
	status, body := s.call("editMessageText", url.Values{"chat_id": {"4242"}, "message_id": {"1"}, "text": {"final"}, "parse_mode": {"Markdown"},
		"reply_markup": {`{"inline_keyboard":[[{"text":"x","callback_data":"y"}]]}`}})
	if status != http.StatusOK || result(t, body)["text"] != "final" {
		t.Fatalf("edit: %d %v", status, body)
	}
	m := s.fake.Chat(4242).Messages[0]
	if !m.Edited || m.Text != "final" || m.ParseMode != "Markdown" || len(m.Keyboard) != 1 {
		t.Fatalf("edited view: %+v", m)
	}
	status, body = s.call("editMessageText", url.Values{"chat_id": {"4242"}, "message_id": {"1"}, "text": {"final"},
		"reply_markup": {`{"inline_keyboard":[[{"text":"x","callback_data":"y"}]]}`}})
	if status != http.StatusBadRequest || !strings.Contains(body["description"].(string), "not modified") {
		t.Fatalf("same edit: %d %v", status, body)
	}
	status, body = s.call("editMessageText", url.Values{"chat_id": {"4242"}, "message_id": {"9"}, "text": {"x"}})
	if status != http.StatusBadRequest || !strings.Contains(body["description"].(string), "not found") {
		t.Fatalf("missing edit: %d %v", status, body)
	}
	status, _ = s.call("editMessageReplyMarkup", url.Values{"chat_id": {"4242"}, "message_id": {"1"}})
	if status != http.StatusOK || s.fake.Chat(4242).Messages[0].Keyboard != nil {
		t.Fatalf("markup removal: %d %+v", status, s.fake.Chat(4242).Messages[0])
	}
}

func TestDeleteMessage(t *testing.T) {
	s := newStand(t, Options{})
	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"gone"}})
	if status, _ := s.call("deleteMessage", url.Values{"chat_id": {"4242"}, "message_id": {"1"}}); status != http.StatusOK {
		t.Fatalf("delete: %d", status)
	}
	if !s.fake.Chat(4242).Messages[0].Deleted || strings.Contains(s.fake.Chat(4242).Text(), "gone") {
		t.Fatalf("deleted message still shown: %s", s.fake.Chat(4242).Text())
	}
	if status, _ := s.call("deleteMessage", url.Values{"chat_id": {"4242"}, "message_id": {"1"}}); status != http.StatusBadRequest {
		t.Fatalf("second delete: %d", status)
	}
}

func TestRichMessageAndDraft(t *testing.T) {
	s := newStand(t, Options{})
	s.fake.InjectMessage(IncomingMessage{Text: "hi"})
	for i := 1; i <= 3; i++ {
		status, body := s.call("sendRichMessageDraft", url.Values{"chat_id": {"4242"}, "draft_id": {"7"}, "rich_message": {`{"markdown":"part ` + itoa(i) + `"}`}})
		if status != http.StatusOK || body["result"] != true {
			t.Fatalf("draft %d: %d %v", i, status, body)
		}
	}
	view := s.fake.Chat(4242)
	if len(view.Drafts) != 1 || view.Drafts[0].Revisions != 3 || view.Drafts[0].Markdown != "part 3" {
		t.Fatalf("drafts: %+v", view.Drafts)
	}
	_, body := s.call("sendRichMessage", url.Values{"chat_id": {"4242"}, "rich_message": {`{"markdown":"# Done"}`}, "reply_parameters": {`{"message_id":1}`}})
	res := result(t, body)
	if res["message_id"] != float64(2) || res["reply_to_message"].(map[string]any)["message_id"] != float64(1) {
		t.Fatalf("rich: %v", res)
	}
	if m := s.fake.Chat(4242).Messages[1]; !m.Rich || m.Text != "# Done" || m.From != "bot" {
		t.Fatalf("rich view: %+v", m)
	}
	status, _ := s.call("sendRichMessage", url.Values{"chat_id": {"4242"}, "rich_message": {`not json`}})
	if status != http.StatusBadRequest {
		t.Fatalf("bad rich_message: %d", status)
	}
}

func TestCommandsRoundTrip(t *testing.T) {
	s := newStand(t, Options{})
	status, _ := s.call("setMyCommands", url.Values{"commands": {`[{"command":"start","description":"Go"},{"command":"help","description":"Help"}]`}})
	if status != http.StatusOK {
		t.Fatalf("set: %d", status)
	}
	_, body := s.call("getMyCommands", nil)
	list, _ := body["result"].([]any)
	if len(list) != 2 || s.fake.Commands()[1].Command != "help" {
		t.Fatalf("commands: %v", body)
	}
	if status, _ := s.call("setMyCommands", url.Values{"commands": {`nope`}}); status != http.StatusBadRequest {
		t.Fatalf("bad commands: %d", status)
	}
}

func TestChatActionAndCallbackAnswer(t *testing.T) {
	s := newStand(t, Options{})
	s.fake.InjectMessage(IncomingMessage{Text: "hi"})
	if status, _ := s.call("sendChatAction", url.Values{"chat_id": {"4242"}, "action": {"typing"}}); status != http.StatusOK {
		t.Fatalf("action: %d", status)
	}
	if !s.fake.Chat(4242).Typing {
		t.Fatal("typing not shown after sendChatAction")
	}
	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"}, "reply_markup": {`{"inline_keyboard":[[{"text":"Plan","callback_data":"mode:plan"}]]}`}})
	_, cbq, err := s.fake.InjectCallback(IncomingCallback{Label: "Plan"})
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := s.call("answerCallbackQuery", url.Values{"callback_query_id": {cbq}, "text": {"done"}, "show_alert": {"true"}}); status != http.StatusOK {
		t.Fatalf("answer: %d", status)
	}
	answers := s.fake.Chat(4242).Callbacks
	if len(answers) != 1 || answers[0].ID != cbq || !answers[0].ShowAlert || answers[0].Text != "done" {
		t.Fatalf("answers: %+v", answers)
	}
	if status, _ := s.call("answerCallbackQuery", nil); status != http.StatusBadRequest {
		t.Fatalf("empty id: %d", status)
	}
}

func TestOutboxRecordsParamsAndResponse(t *testing.T) {
	s := newStand(t, Options{})
	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"hello"}})
	calls := s.fake.Calls("sendmessage")
	if len(calls) != 1 || calls[0].Params["text"] != "hello" || calls[0].Status != 200 || !strings.Contains(string(calls[0].Response), `"message_id":1`) {
		t.Fatalf("outbox: %+v", calls)
	}
	if !s.fake.WaitCall("sendMessage", 1, time.Second) || s.fake.WaitCall("sendMessage", 2, 30*time.Millisecond) {
		t.Fatal("WaitCall")
	}
	s.fake.Reset()
	if len(s.fake.Calls("")) != 0 || len(s.fake.Chat(4242).Messages) != 0 {
		t.Fatal("reset left state behind")
	}
}

func TestJSONBodyIsAccepted(t *testing.T) {
	s := newStand(t, Options{})
	resp, err := http.Post(s.srv.URL+"/bot1/sendMessage", "application/json",
		strings.NewReader(`{"chat_id":4242,"text":"json","reply_markup":{"inline_keyboard":[[{"text":"A","callback_data":"a"}]]}}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("json send: %d", resp.StatusCode)
	}
	m := s.fake.Chat(4242).Messages[0]
	if m.Text != "json" || len(m.Keyboard) != 1 {
		t.Fatalf("json send stored: %+v", m)
	}
}

func TestPage(t *testing.T) {
	s := newStand(t, Options{})
	resp, err := http.Get(s.srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") || !strings.Contains(string(body), "/sim/message") {
		t.Fatalf("page: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	if resp, err := http.Get(s.srv.URL + "/nothing"); err == nil {
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("unknown path: %d", resp.StatusCode)
		}
	}
}

// Telegram counts the 4096-character limit in characters, not bytes: 4096
// Cyrillic letters (8192 bytes) go through, one more is refused - on a send
// and on an edit alike. A fake that took any length would let a streaming path
// that splits by the wrong unit look correct on the stand.
func TestMessageTooLongIsRefused(t *testing.T) {
	s := newStand(t, Options{})
	fits := strings.Repeat("я", 4096)
	status, _ := s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {fits}})
	if status != http.StatusOK {
		t.Fatalf("4096 characters: %d", status)
	}
	status, body := s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {fits + "я"}})
	if status != http.StatusBadRequest || !strings.Contains(body["description"].(string), "message is too long") {
		t.Fatalf("4097 characters: %d %v", status, body)
	}
	status, body = s.call("editMessageText", url.Values{"chat_id": {"4242"}, "message_id": {"1"}, "text": {fits + "я"}})
	if status != http.StatusBadRequest || !strings.Contains(body["description"].(string), "message is too long") {
		t.Fatalf("4097-character edit: %d %v", status, body)
	}
	if msgs := s.fake.Chat(4242).Messages; len(msgs) != 1 || msgs[0].Edited {
		t.Fatalf("a refused send or edit must leave the chat alone: %+v", msgs)
	}
}

// A reply names a message the chat holds; Telegram refuses any other target
// unless the bot said it may send without the reply, in which case the
// message goes out unthreaded. Both spellings of the parameter are read.
func TestReplyToMissingMessageIsRefused(t *testing.T) {
	s := newStand(t, Options{})
	s.fake.InjectMessage(IncomingMessage{Text: "hi"})
	status, body := s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"answer"}, "reply_to_message_id": {"99"}})
	if status != http.StatusBadRequest || !strings.Contains(body["description"].(string), "message to be replied not found") {
		t.Fatalf("reply to a message never sent: %d %v", status, body)
	}
	status, body = s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"answer"}, "reply_to_message_id": {"99"}, "allow_sending_without_reply": {"true"}})
	if status != http.StatusOK || result(t, body)["reply_to_message"] != nil {
		t.Fatalf("allow_sending_without_reply should send unthreaded: %d %v", status, body)
	}
	status, body = s.call("sendRichMessage", url.Values{"chat_id": {"4242"}, "rich_message": {`{"markdown":"answer"}`}, "reply_parameters": {`{"message_id":99}`}})
	if status != http.StatusBadRequest || !strings.Contains(body["description"].(string), "message to be replied not found") {
		t.Fatalf("rich reply to a message never sent: %d %v", status, body)
	}
	status, body = s.call("sendRichMessage", url.Values{"chat_id": {"4242"}, "rich_message": {`{"markdown":"answer"}`}, "reply_parameters": {`{"message_id":99,"allow_sending_without_reply":true}`}})
	if status != http.StatusOK || result(t, body)["reply_to_message"] != nil {
		t.Fatalf("rich allow_sending_without_reply should send unthreaded: %d %v", status, body)
	}
	status, body = s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"threaded"}, "reply_parameters": {`{"message_id":1}`}})
	if status != http.StatusOK || result(t, body)["reply_to_message"].(map[string]any)["text"] != "hi" {
		t.Fatalf("reply_parameters on sendMessage: %d %v", status, body)
	}
	s.call("deleteMessage", url.Values{"chat_id": {"4242"}, "message_id": {"1"}})
	if status, _ = s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"late"}, "reply_to_message_id": {"1"}}); status != http.StatusBadRequest {
		t.Fatalf("a deleted message is not there to reply to: %d", status)
	}
	if status, _ = s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"x"}, "reply_parameters": {`nope`}}); status != http.StatusBadRequest {
		t.Fatalf("broken reply_parameters: %d", status)
	}
}

// answerCallbackQuery names a query the fake handed out; any other id is what
// Telegram calls too old or invalid, and a Reset forgets the ids with the rest.
func TestAnswerCallbackQuery_UnknownIDIsRefused(t *testing.T) {
	s := newStand(t, Options{})
	status, body := s.call("answerCallbackQuery", url.Values{"callback_query_id": {"cbq-99"}})
	if status != http.StatusBadRequest || !strings.Contains(body["description"].(string), "query ID is invalid") {
		t.Fatalf("unknown query: %d %v", status, body)
	}
	s.fake.InjectMessage(IncomingMessage{Text: "/mode"})
	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"}, "reply_markup": {`{"inline_keyboard":[[{"text":"Plan","callback_data":"mode:plan"}]]}`}})
	_, cbq, err := s.fake.InjectCallback(IncomingCallback{Label: "Plan"})
	if err != nil {
		t.Fatal(err)
	}
	if status, _ := s.call("answerCallbackQuery", url.Values{"callback_query_id": {cbq}}); status != http.StatusOK {
		t.Fatalf("a query the fake issued: %d", status)
	}
	s.fake.Reset()
	if status, _ := s.call("answerCallbackQuery", url.Values{"callback_query_id": {cbq}}); status != http.StatusBadRequest {
		t.Fatalf("a query from before Reset: %d", status)
	}
}

// Reset clears what a chat holds and keeps what Telegram keeps with the token:
// the update counter and the allowed_updates subscription.
func TestReset_KeepsSubscriptionAndUpdateIDs(t *testing.T) {
	s := newStand(t, Options{})
	s.fake.SetAllowedUpdates([]string{"message"})
	s.fake.InjectMessage(IncomingMessage{Text: "before"})
	s.fake.Reset()
	if got := s.fake.AllowedUpdates(); len(got) != 1 || got[0] != "message" {
		t.Fatalf("Reset must keep the subscription: %v", got)
	}
	if upd, _ := s.fake.InjectMessage(IncomingMessage{Text: "after"}); upd != 2 {
		t.Fatalf("update ids must keep growing across Reset: %d", upd)
	}
	s.fake.SetAllowedUpdates(nil)
	if got := s.fake.AllowedUpdates(); got != nil {
		t.Fatalf("SetAllowedUpdates(nil) should mean every kind: %v", got)
	}
}

// An edit or a delete without a chat is refused as such, not reported as a
// message that was not found in chat 0.
func TestEditAndDeleteNeedAChatID(t *testing.T) {
	s := newStand(t, Options{})
	for _, method := range []string{"editMessageText", "editMessageReplyMarkup", "deleteMessage"} {
		status, body := s.call(method, url.Values{"message_id": {"1"}, "text": {"x"}})
		if status != http.StatusBadRequest || !strings.Contains(body["description"].(string), "chat_id is empty") {
			t.Fatalf("%s without chat_id: %d %v", method, status, body)
		}
	}
}
