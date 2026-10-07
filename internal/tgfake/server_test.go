package tgfake

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
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
	status, body := s.call("sendVoice", url.Values{"chat_id": {"1"}})
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
	if status != http.StatusBadRequest || body["description"] != "Bad Request: Text buttons are not allowed in the inline keyboard" {
		t.Fatalf("a button with nothing behind it: %d %v", status, body)
	}
	status, _ = s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"link"},
		"reply_markup": {`{"inline_keyboard":[[{"text":"Docs","url":"https://coddy.dev"}]]}`}})
	if status != http.StatusOK {
		t.Fatalf("a url button: %d", status)
	}
}

// Telegram reads inline_keyboard as an array and refuses a value of any other
// type, null included. A Bot API library marshals a markup built from no rows
// as {"inline_keyboard":null}, so a bot that attaches an empty keyboard sends
// nothing at all to a real chat. A markup without the key (another kind of
// keyboard) and an array, an empty one too, still go through.
func TestKeyboard_InlineKeyboardMustBeAnArray(t *testing.T) {
	const refusal = `Bad Request: Field "inline_keyboard" must be of type Array`
	s := newStand(t, Options{})
	for _, markup := range []string{`{"inline_keyboard":null}`, `{"inline_keyboard":{}}`, `{"inline_keyboard":"[]"}`, `{"inline_keyboard":0}`} {
		status, body := s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"}, "reply_markup": {markup}})
		if status != http.StatusBadRequest || body["description"] != refusal {
			t.Fatalf("sendMessage with %s: %d %v", markup, status, body)
		}
	}
	// A JSON body carries the markup as a nested object rather than a string.
	resp, err := http.Post(s.srv.URL+"/bot1/sendMessage", "application/json",
		strings.NewReader(`{"chat_id":4242,"text":"menu","reply_markup":{"inline_keyboard":null}}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("JSON body with a null keyboard: %d", resp.StatusCode)
	}
	if n := len(s.fake.Chat(4242).Messages); n != 0 {
		t.Fatalf("a refused keyboard delivered %d messages", n)
	}

	for _, markup := range []string{`{"inline_keyboard":[]}`, `{"remove_keyboard":true}`} {
		if status, body := s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"}, "reply_markup": {markup}}); status != http.StatusOK {
			t.Fatalf("sendMessage with %s: %d %v", markup, status, body)
		}
	}
	if msgs := s.fake.Chat(4242).Messages; len(msgs) != 2 || msgs[0].Keyboard != nil || msgs[1].Keyboard != nil {
		t.Fatalf("an empty array and another kind of markup should arrive without a keyboard: %+v", msgs)
	}

	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"},
		"reply_markup": {`{"inline_keyboard":[[{"text":"A","callback_data":"a"}]]}`}})
	for _, method := range []string{"editMessageText", "editMessageReplyMarkup"} {
		status, body := s.call(method, url.Values{"chat_id": {"4242"}, "message_id": {"3"}, "text": {"menu 2"},
			"reply_markup": {`{"inline_keyboard":null}`}})
		if status != http.StatusBadRequest || body["description"] != refusal {
			t.Fatalf("%s with a null keyboard: %d %v", method, status, body)
		}
	}
	if m := s.fake.Chat(4242).Messages[2]; m.Edited || m.Text != "menu" || len(m.Keyboard) != 1 {
		t.Fatalf("a refused edit must leave the message as it was: %+v", m)
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
	s.fake.InjectMessage(IncomingMessage{Text: "/model"})
	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"}, "reply_markup": {`{"inline_keyboard":[[{"text":"coddy-mini","callback_data":"model:stub/coddy-mini"}]]}`}})
	_, cbq, err := s.fake.InjectCallback(IncomingCallback{Label: "coddy-mini"})
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

// A callback query takes one answer. A bot that answers every tap at once to
// stop the spinner and then sends an alert about a failure found later has the
// alert refused, and the person never sees it; the refused answer is not
// recorded. Another tap is another query and is answered on its own.
func TestAnswerCallbackQuery_SecondAnswerIsRefused(t *testing.T) {
	s := newStand(t, Options{})
	s.fake.InjectMessage(IncomingMessage{Text: "/mcp"})
	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"menu"},
		"reply_markup": {`{"inline_keyboard":[[{"text":"Enable demo","callback_data":"mcp:1:demo"}]]}`}})
	_, first, err := s.fake.InjectCallback(IncomingCallback{Label: "Enable demo"})
	if err != nil {
		t.Fatal(err)
	}
	if status, body := s.call("answerCallbackQuery", url.Values{"callback_query_id": {first}}); status != http.StatusOK {
		t.Fatalf("first answer: %d %v", status, body)
	}
	status, body := s.call("answerCallbackQuery", url.Values{"callback_query_id": {first}, "text": {"❌ failed"}, "show_alert": {"true"}})
	if status != http.StatusBadRequest || body["description"] != "Bad Request: query is too old and response timeout expired or query ID is invalid" {
		t.Fatalf("second answer to the same query: %d %v", status, body)
	}
	if answers := s.fake.Chat(4242).Callbacks; len(answers) != 1 || answers[0].ID != first || answers[0].Text != "" || answers[0].ShowAlert {
		t.Fatalf("only the first answer should be recorded: %+v", answers)
	}
	_, second, err := s.fake.InjectCallback(IncomingCallback{Label: "Enable demo"})
	if err != nil {
		t.Fatal(err)
	}
	if status, body := s.call("answerCallbackQuery", url.Values{"callback_query_id": {second}, "text": {"done"}}); status != http.StatusOK {
		t.Fatalf("answer to another tap: %d %v", status, body)
	}
	if answers := s.fake.Chat(4242).Callbacks; len(answers) != 2 || answers[1].ID != second {
		t.Fatalf("a new tap should be answered on its own: %+v", answers)
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

// --- Mini Apps -------------------------------------------------------------

// checkInitData verifies launch data the way a Mini App's server does
// (core.telegram.org/bots/webapps, "Validating data received via the Mini
// App"): the data-check-string is every field but hash, sorted by key, as
// key=value lines; the secret is HMAC-SHA256 of the token under the key
// "WebAppData"; hash is the hex HMAC-SHA256 of the data-check-string under
// that secret. It is written here from the documentation, not taken from the
// fake, so the fake's signing is checked against something independent.
func checkInitData(t *testing.T, token, initData string) url.Values {
	t.Helper()
	vals, err := url.ParseQuery(initData)
	if err != nil {
		t.Fatalf("init data %q does not parse: %v", initData, err)
	}
	hash := vals.Get("hash")
	if hash == "" {
		t.Fatalf("init data has no hash: %q", initData)
	}
	keys := make([]string, 0, len(vals))
	for k := range vals {
		if k != "hash" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+vals.Get(k))
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(token))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	if want := hex.EncodeToString(mac.Sum(nil)); hash != want {
		t.Fatalf("init data hash %s does not check out against token %q (want %s)", hash, token, want)
	}
	return vals
}

// launchFragment splits a launch URL into the address the app was given and
// the launch parameters Telegram appended to its fragment.
func launchFragment(t *testing.T, launch string) (string, url.Values) {
	t.Helper()
	base, frag, ok := strings.Cut(launch, "#")
	if !ok {
		t.Fatalf("launch URL %q has no fragment", launch)
	}
	if i := strings.Index(frag, "?"); i >= 0 {
		base += "#" + frag[:i]
		frag = frag[i+1:]
	}
	params, err := url.ParseQuery(frag)
	if err != nil {
		t.Fatalf("launch fragment %q does not parse: %v", frag, err)
	}
	return base, params
}

func TestWebAppButton_PrivateChatOnly(t *testing.T) {
	s := newStand(t, Options{})
	markup := `{"inline_keyboard":[[{"text":"Open in Coddy","web_app":{"url":"https://coddy.example.com/?session=sess_1"}}]]}`
	status, body := s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"open"}, "reply_markup": {markup}})
	if status != http.StatusOK {
		t.Fatalf("a web_app button in a private chat: %d %v", status, body)
	}
	msgs := s.fake.Chat(4242).Messages
	if len(msgs) != 1 || len(msgs[0].Keyboard) != 1 || msgs[0].Keyboard[0][0].WebApp == nil ||
		msgs[0].Keyboard[0][0].WebApp.URL != "https://coddy.example.com/?session=sess_1" {
		t.Fatalf("the chat should keep the web_app button: %+v", msgs)
	}
	if got := s.fake.Chat(4242).Text(); !strings.Contains(got, "[Open in Coddy]") {
		t.Fatalf("the text transcript should show the button by its label:\n%s", got)
	}

	// Telegram allows web_app buttons only in a private chat with the bot.
	status, body = s.call("sendMessage", url.Values{"chat_id": {"-100500"}, "text": {"open"}, "reply_markup": {markup}})
	if status != http.StatusBadRequest || !strings.Contains(body["description"].(string), "BUTTON_TYPE_INVALID") {
		t.Fatalf("a web_app button in a group: %d %v", status, body)
	}
	if n := len(s.fake.Chat(-100500).Messages); n != 0 {
		t.Fatalf("a refused group message reached the chat: %d", n)
	}
}

func TestWebAppButton_URLRules(t *testing.T) {
	s := newStand(t, Options{})
	send := func(webURL string) (int, map[string]any) {
		return s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"open"},
			"reply_markup": {`{"inline_keyboard":[[{"text":"App","web_app":{"url":"` + webURL + `"}}]]}`}})
	}
	for _, ok := range []string{"https://coddy.example.com/", "http://127.0.0.1:18792/", "http://localhost:5173/", "http://[::1]:8080/"} {
		if status, body := send(ok); status != http.StatusOK {
			t.Fatalf("web_app URL %s: %d %v", ok, status, body)
		}
	}
	for _, bad := range []string{"http://coddy.example.com/", "ftp://coddy.example.com/", "coddy.example.com", ""} {
		status, body := send(bad)
		if status != http.StatusBadRequest || !strings.Contains(strings.ToLower(body["description"].(string)), "url") {
			t.Fatalf("web_app URL %q: %d %v, want 400 naming the URL", bad, status, body)
		}
	}
	// One action per button. Telegram takes the first of several in its own
	// order; the stand refuses the button, so an ambiguous one never passes.
	status, body := s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"open"},
		"reply_markup": {`{"inline_keyboard":[[{"text":"App","callback_data":"x","web_app":{"url":"https://coddy.example.com/"}}]]}`}})
	if status != http.StatusBadRequest || !strings.Contains(body["description"].(string), "BUTTON_TYPE_INVALID") {
		t.Fatalf("a button with callback_data and web_app: %d %v", status, body)
	}
	// A button with no action at all is refused the way Telegram does.
	status, body = s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"open"},
		"reply_markup": {`{"inline_keyboard":[[{"text":"App"}]]}`}})
	if status != http.StatusBadRequest || body["description"] != "Bad Request: Text buttons are not allowed in the inline keyboard" {
		t.Fatalf("a button with no action: %d %v", status, body)
	}
}

func TestWebAppButton_SameKeyboardIsNotModified(t *testing.T) {
	s := newStand(t, Options{})
	markup := `{"inline_keyboard":[[{"text":"App","web_app":{"url":"https://coddy.example.com/"}}]]}`
	s.call("sendMessage", url.Values{"chat_id": {"4242"}, "text": {"open"}, "reply_markup": {markup}})
	status, body := s.call("editMessageReplyMarkup", url.Values{"chat_id": {"4242"}, "message_id": {"1"}, "reply_markup": {markup}})
	if status != http.StatusBadRequest || !strings.Contains(body["description"].(string), "not modified") {
		t.Fatalf("the same web_app keyboard again: %d %v", status, body)
	}
	other := `{"inline_keyboard":[[{"text":"App","web_app":{"url":"https://coddy.example.com/?session=sess_2"}}]]}`
	if status, body := s.call("editMessageReplyMarkup", url.Values{"chat_id": {"4242"}, "message_id": {"1"}, "reply_markup": {other}}); status != http.StatusOK {
		t.Fatalf("a web_app button to another address: %d %v", status, body)
	}
}

func TestMenuButton_DefaultAndPerChat(t *testing.T) {
	s := newStand(t, Options{})
	get := func(chat string) map[string]any {
		t.Helper()
		form := url.Values{}
		if chat != "" {
			form.Set("chat_id", chat)
		}
		status, body := s.call("getChatMenuButton", form)
		if status != http.StatusOK {
			t.Fatalf("getChatMenuButton(%q): %d %v", chat, status, body)
		}
		return result(t, body)
	}
	if got := get(""); got["type"] != "commands" {
		t.Fatalf("a bot that set nothing has the commands menu: %v", got)
	}
	status, body := s.call("setChatMenuButton", url.Values{
		"menu_button": {`{"type":"web_app","text":"Coddy","web_app":{"url":"https://coddy.example.com/"}}`}})
	if status != http.StatusOK || body["result"] != true {
		t.Fatalf("set the default menu button: %d %v", status, body)
	}
	for _, chat := range []string{"", "4242"} {
		got := get(chat)
		app, _ := got["web_app"].(map[string]any)
		if got["type"] != "web_app" || got["text"] != "Coddy" || app["url"] != "https://coddy.example.com/" {
			t.Fatalf("menu button for chat %q: %v", chat, got)
		}
	}
	// A chat's own button wins over the default; "default" takes it away again.
	s.call("setChatMenuButton", url.Values{"chat_id": {"4242"}, "menu_button": {`{"type":"commands"}`}})
	if got := get("4242"); got["type"] != "commands" {
		t.Fatalf("the chat's own button: %v", got)
	}
	if got := get("777"); got["type"] != "web_app" {
		t.Fatalf("another chat still has the default: %v", got)
	}
	s.call("setChatMenuButton", url.Values{"chat_id": {"4242"}, "menu_button": {`{"type":"default"}`}})
	if got := get("4242"); got["type"] != "web_app" {
		t.Fatalf("after default the chat follows the bot's button again: %v", got)
	}
	// No menu_button at all, or type default, puts the bot's button back.
	s.call("setChatMenuButton", url.Values{"menu_button": {`{"type":"default"}`}})
	if got := get(""); got["type"] != "commands" {
		t.Fatalf("the default menu button after a reset: %v", got)
	}
	if view := s.fake.Chat(4242); view.MenuButton == nil || view.MenuButton.Type != "commands" {
		t.Fatalf("the chat view should name the menu button the chat shows: %+v", view.MenuButton)
	}
}

func TestMenuButton_Refusals(t *testing.T) {
	s := newStand(t, Options{})
	for name, raw := range map[string]string{
		"unknown type":   `{"type":"weird"}`,
		"web_app no url": `{"type":"web_app","text":"Coddy","web_app":{"url":""}}`,
		"web_app http":   `{"type":"web_app","text":"Coddy","web_app":{"url":"http://coddy.example.com/"}}`,
		"web_app text":   `{"type":"web_app","text":"","web_app":{"url":"https://coddy.example.com/"}}`,
		"not json":       `{`,
	} {
		if status, body := s.call("setChatMenuButton", url.Values{"menu_button": {raw}}); status != http.StatusBadRequest {
			t.Fatalf("%s: %d %v", name, status, body)
		}
	}
	// A menu button belongs to a private chat or to the bot.
	status, body := s.call("setChatMenuButton", url.Values{"chat_id": {"-100500"}, "menu_button": {`{"type":"commands"}`}})
	if status != http.StatusBadRequest {
		t.Fatalf("a group's menu button: %d %v", status, body)
	}
	if got := s.fake.MenuButton(0); got.Type != "commands" {
		t.Fatalf("refused calls must leave the menu button alone: %+v", got)
	}
}

// Telegram shows a bot's menu button in private chats only, and the Bot API
// reads the chat_id of both menu button methods as a user: a group's id is
// refused, with the menu button checked first on a set.
func TestMenuButton_PrivateChatsOnly(t *testing.T) {
	s := newStand(t, Options{})
	app := `{"type":"web_app","text":"Coddy","web_app":{"url":"https://coddy.example.com/"}}`
	if status, body := s.call("setChatMenuButton", url.Values{"menu_button": {app}}); status != http.StatusOK {
		t.Fatalf("the bot's menu button: %d %v", status, body)
	}
	if m := s.fake.Chat(4242).MenuButton; m == nil || m.Type != "web_app" {
		t.Fatalf("a private chat's menu button: %+v", m)
	}
	if _, err := s.fake.LaunchWebApp(WebAppLaunch{ChatID: 4242}); err != nil {
		t.Fatalf("the menu button of a private chat does not open: %v", err)
	}
	if m := s.fake.Chat(-100500); m.Type != "group" || m.MenuButton == nil || m.MenuButton.Type != "commands" {
		t.Fatalf("a group shows %s with the menu button %+v", m.Type, m.MenuButton)
	}
	if _, err := s.fake.LaunchWebApp(WebAppLaunch{ChatID: -100500}); err == nil {
		t.Fatal("a group opened a Mini App from a menu button it does not have")
	}
	for _, chatID := range []string{"-100500", "0", "abc"} {
		for _, method := range []string{"getChatMenuButton", "setChatMenuButton"} {
			status, body := s.call(method, url.Values{"chat_id": {chatID}, "menu_button": {`{"type":"commands"}`}})
			if status != http.StatusBadRequest || body["description"] != "Bad Request: Invalid chat_id specified" {
				t.Errorf("%s for chat_id %s: %d %v", method, chatID, status, body)
			}
		}
	}
	status, body := s.call("setChatMenuButton", url.Values{"chat_id": {"-100500"}, "menu_button": {`{"type":"weird"}`}})
	if status != http.StatusBadRequest || body["description"] == "Bad Request: Invalid chat_id specified" {
		t.Errorf("the menu button is checked before the chat: %d %v", status, body)
	}
}

func TestToken_KeptFromThePath(t *testing.T) {
	s := newStand(t, Options{})
	if got := s.fake.Token(); got != "" {
		t.Fatalf("no call yet, token = %q", got)
	}
	s.callToken("123456:FIRST", "getMe", nil)
	s.callToken("123456:SECOND", "getMe", nil)
	if got := s.fake.Token(); got != "123456:SECOND" {
		t.Fatalf("token = %q, want the one of the latest call", got)
	}
	fixed := newStand(t, Options{Token: "123456:FIXED"})
	fixed.callToken("123456:OTHER", "getMe", nil) // refused with 401
	if got := fixed.fake.Token(); got != "123456:FIXED" {
		t.Fatalf("--token wins, token = %q", got)
	}
}

func TestLaunchWebApp_SignedLikeTelegram(t *testing.T) {
	s := newStand(t, Options{})
	s.callToken("123456:SIGN", "getMe", nil)
	launch, err := s.fake.LaunchWebApp(WebAppLaunch{
		ChatID: 4242, UserID: 4242, Username: "alice", URL: "https://coddy.example.com/?session=sess_1",
	})
	if err != nil {
		t.Fatal(err)
	}
	base, params := launchFragment(t, launch.URL)
	if base != "https://coddy.example.com/?session=sess_1" {
		t.Fatalf("the app was given %q", base)
	}
	if params.Get("tgWebAppVersion") == "" || params.Get("tgWebAppPlatform") == "" || params.Get("tgWebAppThemeParams") == "" {
		t.Fatalf("launch parameters missing: %v", params)
	}
	if params.Get("tgWebAppData") != launch.InitData {
		t.Fatalf("tgWebAppData %q is not the init data %q", params.Get("tgWebAppData"), launch.InitData)
	}
	vals := checkInitData(t, "123456:SIGN", launch.InitData)
	var user map[string]any
	if err := json.Unmarshal([]byte(vals.Get("user")), &user); err != nil || user["id"] != float64(4242) || user["username"] != "alice" {
		t.Fatalf("init data user: %q (%v)", vals.Get("user"), err)
	}
	if vals.Get("auth_date") == "" || vals.Get("query_id") == "" {
		t.Fatalf("init data lacks auth_date or query_id: %v", vals)
	}
	var theme map[string]string
	if err := json.Unmarshal([]byte(params.Get("tgWebAppThemeParams")), &theme); err != nil || theme["bg_color"] == "" {
		t.Fatalf("theme params: %q (%v)", params.Get("tgWebAppThemeParams"), err)
	}
}

func TestLaunchWebApp_Options(t *testing.T) {
	s := newStand(t, Options{Token: "123456:OPT"})
	if _, err := s.fake.LaunchWebApp(WebAppLaunch{ChatID: 4242}); err == nil {
		t.Fatal("no URL and no web_app menu button: want an error")
	}
	s.callToken("123456:OPT", "setChatMenuButton", url.Values{"menu_button": {`{"type":"web_app","text":"Coddy","web_app":{"url":"https://coddy.example.com/"}}`}})
	launch, err := s.fake.LaunchWebApp(WebAppLaunch{ChatID: 4242, StartParam: "sess_9", ColorScheme: "light"})
	if err != nil {
		t.Fatal(err)
	}
	base, params := launchFragment(t, launch.URL)
	// The start parameter travels in the query, where Telegram puts it, and
	// in the signed data.
	if base != "https://coddy.example.com/?tgWebAppStartParam=sess_9" {
		t.Fatalf("the menu button's app with a start parameter was given %q", base)
	}
	if vals := checkInitData(t, "123456:OPT", launch.InitData); vals.Get("start_param") != "sess_9" {
		t.Fatalf("start_param in init data: %v", vals)
	}
	var theme map[string]string
	_ = json.Unmarshal([]byte(params.Get("tgWebAppThemeParams")), &theme)
	if theme["bg_color"] != "#ffffff" {
		t.Fatalf("light theme bg_color = %q", theme["bg_color"])
	}
	// A fragment the address already has keeps its place; the launch
	// parameters follow it after a question mark, the form the SDK parses.
	launch, err = s.fake.LaunchWebApp(WebAppLaunch{ChatID: 4242, URL: "https://coddy.example.com/#/s/sess_1"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(launch.URL, "https://coddy.example.com/#/s/sess_1?tgWebAppData=") {
		t.Fatalf("launch URL with a fragment: %q", launch.URL)
	}
}

// A fixed vector, computed outside this package (Python's hmac over the
// documented data-check-string), so the fake's signing and the checker above
// cannot agree on the same mistake.
func TestSignInitData_KnownVector(t *testing.T) {
	data := url.Values{}
	data.Set("auth_date", "1700000000")
	data.Set("query_id", "AAHdF6IQAAAAAN0XohDhrOrc")
	data.Set("user", `{"id":279058397,"first_name":"Vladislav","username":"vdkfrost","language_code":"ru"}`)
	const want = "d16b987afd609aa3c33232cac13427d98b80535d6e4b3e4025a02a336fd343ef"
	if got := signInitData("123456:ABC-DEF1234ghIkl", data); got != want {
		t.Fatalf("signInitData = %s, want %s", got, want)
	}
	data.Set("hash", want)
	checkInitData(t, "123456:ABC-DEF1234ghIkl", data.Encode())
}
