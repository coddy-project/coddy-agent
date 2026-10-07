package pachcafake

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testToken = "pachca-test-token-0123456789"

type stand struct {
	t    *testing.T
	fake *Server
	srv  *httptest.Server
}

func newStand(t *testing.T, opts Options) *stand {
	t.Helper()
	if opts.Token == "" {
		opts.Token = testToken
	}
	fake := New(opts)
	srv := httptest.NewServer(fake.Handler())
	t.Cleanup(srv.Close)
	return &stand{t: t, fake: fake, srv: srv}
}

type response struct {
	status int
	header http.Header
	raw    []byte
	body   map[string]any
}

// do sends a request with the stand's token; body is marshalled unless it is
// already a string.
func (s *stand) do(method, path string, body any) response {
	s.t.Helper()
	return s.doToken(testToken, method, path, body)
}

func (s *stand) doToken(token, method, path string, body any) response {
	s.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			s.t.Fatal(err)
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, s.srv.URL+path, rd)
	if err != nil {
		s.t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	out := response{status: resp.StatusCode, header: resp.Header, raw: raw}
	if len(raw) > 0 && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		if err := json.Unmarshal(raw, &out.body); err != nil {
			s.t.Fatalf("decode %s: %v", raw, err)
		}
	}
	return out
}

func (s *stand) post(content string, extra map[string]any) response {
	s.t.Helper()
	msg := map[string]any{"content": content}
	for k, v := range extra {
		msg[k] = v
	}
	return s.do(http.MethodPost, "/messages", map[string]any{"message": msg})
}

func expectStatus(t *testing.T, r response, want int) {
	t.Helper()
	if r.status != want {
		t.Fatalf("status %d, want %d: %s", r.status, want, r.raw)
	}
}

func errorCode(t *testing.T, r response) string {
	t.Helper()
	errs, _ := r.body["errors"].([]any)
	if len(errs) == 0 {
		t.Fatalf("no errors in %s", r.raw)
	}
	item := errs[0].(map[string]any)
	for _, k := range []string{"key", "value", "message", "code", "payload"} {
		if _, ok := item[k]; !ok {
			t.Fatalf("error item misses %q: %s", k, r.raw)
		}
	}
	return item["code"].(string)
}

func data(t *testing.T, r response) map[string]any {
	t.Helper()
	d, ok := r.body["data"].(map[string]any)
	if !ok {
		t.Fatalf("no data object in %s", r.raw)
	}
	return d
}

func num(v any) int64 {
	f, _ := v.(float64)
	return int64(f)
}

func TestAuthRefusesMissingAndWrongToken(t *testing.T) {
	s := newStand(t, Options{})
	for _, token := range []string{"", "wrong"} {
		r := s.doToken(token, http.MethodGet, "/profile", nil)
		expectStatus(t, r, http.StatusUnauthorized)
		if r.body["error"] != "invalid_token" || r.body["error_description"] == "" {
			t.Fatalf("token %q: body %s", token, r.raw)
		}
	}
	if got := len(s.fake.Calls("GET /profile")); got != 2 {
		t.Fatalf("calls recorded %d, want 2", got)
	}

	open := New(Options{})
	srv := httptest.NewServer(open.Handler())
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/profile", nil)
	req.Header.Set("Authorization", "Bearer anything")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("empty Options.Token refused a token: %d", resp.StatusCode)
	}
}

func TestTokenInfoAndProfile(t *testing.T) {
	s := newStand(t, Options{})
	r := s.do(http.MethodGet, "/oauth/token/info", nil)
	expectStatus(t, r, http.StatusOK)
	d := data(t, r)
	if d["token"] != "pachca-t...6789" {
		t.Fatalf("token mask %v", d["token"])
	}
	if num(d["user_id"]) != 7000001 || d["name"] != nil || d["revoked_at"] != nil || d["expires_in"] != nil {
		t.Fatalf("token info %s", r.raw)
	}
	if scopes, _ := d["scopes"].([]any); len(scopes) != len(DefaultScopes()) {
		t.Fatalf("scopes %v", d["scopes"])
	}
	if _, err := time.Parse(timestampLayout, d["created_at"].(string)); err != nil {
		t.Fatalf("created_at %v: %v", d["created_at"], err)
	}

	r = s.do(http.MethodGet, "/profile", nil)
	expectStatus(t, r, http.StatusOK)
	p := data(t, r)
	if num(p["id"]) != 7000001 || p["nickname"] != "coddy_bot" || p["first_name"] != "Coddy" || p["bot"] != true {
		t.Fatalf("profile %s", r.raw)
	}
	for _, k := range []string{"last_name", "email", "role", "invite_status", "list_tags", "custom_properties", "user_status", "sso", "created_at", "time_zone", "image_url"} {
		if _, ok := p[k]; !ok {
			t.Fatalf("profile misses %q: %s", k, r.raw)
		}
	}
}

func TestMissingScopeIs403(t *testing.T) {
	s := newStand(t, Options{Scopes: []string{"messages:read"}})
	r := s.do(http.MethodGet, "/profile", nil)
	expectStatus(t, r, http.StatusForbidden)
	if r.body["error"] != "insufficient_scope" {
		t.Fatalf("body %s", r.raw)
	}
	expectStatus(t, s.do(http.MethodGet, "/oauth/token/info", nil), http.StatusOK)
}

func TestGetChat(t *testing.T) {
	s := newStand(t, Options{})
	s.fake.AddGroupChat(42, "Team", 501, 502)
	r := s.do(http.MethodGet, "/chats/42", nil)
	expectStatus(t, r, http.StatusOK)
	c := data(t, r)
	members, _ := c["member_ids"].([]any)
	if c["name"] != "Team" || c["personal"] != false || len(members) != 3 {
		t.Fatalf("chat %s", r.raw)
	}
	r = s.do(http.MethodGet, "/chats/43", nil)
	expectStatus(t, r, http.StatusNotFound)
	if errorCode(t, r) != "not_found" {
		t.Fatalf("body %s", r.raw)
	}
}

func TestPostToDiscussionThreadAndUser(t *testing.T) {
	s := newStand(t, Options{})
	s.fake.AddUser(501, "alice", "Alice")
	s.fake.AddGroupChat(42, "Team", 501)

	r := s.post("hello group", map[string]any{"entity_id": 42, "skip_invite_mentions": true})
	expectStatus(t, r, http.StatusCreated)
	m := data(t, r)
	if m["entity_type"] != "discussion" || num(m["entity_id"]) != 42 || num(m["chat_id"]) != 42 || num(m["user_id"]) != 7000001 {
		t.Fatalf("discussion message %s", r.raw)
	}
	for _, k := range []string{"files", "voice_content", "buttons", "thread", "forwarding", "parent_message_id", "changed_at", "deleted_at", "root_chat_id", "url"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("message misses %q: %s", k, r.raw)
		}
	}
	if m["buttons"] != nil || m["parent_message_id"] != nil {
		t.Fatalf("message %s", r.raw)
	}
	wantURL := "https://app.pachca.com/chats/42?message=" + strconv.FormatInt(num(m["id"]), 10)
	if m["url"] != wantURL {
		t.Fatalf("url %v, want %v", m["url"], wantURL)
	}
	if calls := s.fake.Calls("POST /messages"); len(calls) != 1 || !calls[0].SkipInviteMentions || calls[0].LinkPreview {
		t.Fatalf("calls %+v", calls)
	}

	// A thread under a person's message: posted by thread id, or into the
	// thread's chat as a discussion.
	root := s.fake.UserPosts(Post{UserID: 501, ChatID: 42, Content: "question"})
	threadID, threadChat := s.fake.StartThread(root.ID)
	if again, _ := s.fake.StartThread(root.ID); again != threadID {
		t.Fatalf("second StartThread opened a new thread")
	}
	r = s.post("in thread", map[string]any{"entity_type": "thread", "entity_id": threadID})
	expectStatus(t, r, http.StatusCreated)
	m = data(t, r)
	if m["entity_type"] != "thread" || num(m["entity_id"]) != threadID || num(m["chat_id"]) != threadChat || num(m["root_chat_id"]) != 42 {
		t.Fatalf("thread message %s", r.raw)
	}
	r = s.post("thread chat", map[string]any{"entity_id": threadChat})
	expectStatus(t, r, http.StatusCreated)
	if num(data(t, r)["chat_id"]) != threadChat {
		t.Fatalf("discussion into thread chat %s", r.raw)
	}
	parent, _ := s.fake.Message(root.ID)
	if parent.Thread == nil || parent.Thread.ID != threadID || parent.Thread.ChatID != threadChat {
		t.Fatalf("parent thread %+v", parent.Thread)
	}

	// A user target creates the personal chat once.
	r = s.post("hi alice", map[string]any{"entity_type": "user", "entity_id": 501})
	expectStatus(t, r, http.StatusCreated)
	m = data(t, r)
	personal := s.fake.PersonalChat(501)
	if m["entity_type"] != "user" || num(m["entity_id"]) != 501 || num(m["chat_id"]) != personal {
		t.Fatalf("personal message %s (chat %d)", r.raw, personal)
	}
	if got := len(s.fake.Messages(personal)); got != 1 {
		t.Fatalf("personal history %d", got)
	}

	for _, target := range []map[string]any{
		{"entity_id": 99},
		{"entity_type": "thread", "entity_id": 99},
		{"entity_type": "user", "entity_id": 99},
	} {
		expectStatus(t, s.post("x", target), http.StatusNotFound)
	}
	r = s.post("   ", map[string]any{"entity_id": 42})
	expectStatus(t, r, http.StatusUnprocessableEntity)
	if errorCode(t, r) != "blank" {
		t.Fatalf("blank %s", r.raw)
	}
	expectStatus(t, s.do(http.MethodPost, "/messages", "{not json"), http.StatusBadRequest)
}

func TestPostParentValidation(t *testing.T) {
	s := newStand(t, Options{})
	s.fake.AddGroupChat(42, "A", 501)
	s.fake.AddGroupChat(43, "B", 501)
	inA := s.fake.UserPosts(Post{UserID: 501, ChatID: 42, Content: "a"})

	r := s.post("reply", map[string]any{"entity_id": 42, "parent_message_id": inA.ID})
	expectStatus(t, r, http.StatusCreated)
	if num(data(t, r)["parent_message_id"]) != inA.ID {
		t.Fatalf("reply %s", r.raw)
	}
	r = s.post("reply", map[string]any{"entity_id": 43, "parent_message_id": inA.ID})
	expectStatus(t, r, http.StatusUnprocessableEntity)
	if errorCode(t, r) != "invalid" {
		t.Fatalf("other chat %s", r.raw)
	}
	r = s.post("reply", map[string]any{"entity_id": 42, "parent_message_id": 1})
	expectStatus(t, r, http.StatusNotFound)
}

func TestPostButtonValidation(t *testing.T) {
	s := newStand(t, Options{})
	s.fake.AddGroupChat(42, "A")
	row := func(n int) []map[string]any {
		out := make([]map[string]any, n)
		for i := range out {
			out[i] = map[string]any{"text": "b" + strconv.Itoa(i), "data": "d" + strconv.Itoa(i)}
		}
		return out
	}
	cases := map[string]any{
		"data over 255 bytes": [][]map[string]any{{{"text": "x", "data": strings.Repeat("я", 128)}}},
		"url and data":        [][]map[string]any{{{"text": "x", "data": "d", "url": "https://e.com"}}},
		"neither":             [][]map[string]any{{{"text": "x"}}},
		"empty text":          [][]map[string]any{{{"text": "", "data": "d"}}},
		"nine in a row":       [][]map[string]any{row(9)},
		"over 100 total":      [][]map[string]any{row(8), row(8), row(8), row(8), row(8), row(8), row(8), row(8), row(8), row(8), row(8), row(8), row(8)},
	}
	for name, buttons := range cases {
		r := s.post("x", map[string]any{"entity_id": 42, "buttons": buttons})
		if r.status != http.StatusUnprocessableEntity || errorCode(t, r) != "invalid" {
			t.Errorf("%s: %d %s", name, r.status, r.raw)
		}
	}
	if got := len(s.fake.Messages(42)); got != 0 {
		t.Fatalf("refused posts stored %d messages", got)
	}
	r := s.post("ok", map[string]any{"entity_id": 42, "buttons": [][]map[string]any{row(8), {{"text": "site", "url": "https://e.com"}}}})
	expectStatus(t, r, http.StatusCreated)
	if rows, _ := data(t, r)["buttons"].([]any); len(rows) != 2 {
		t.Fatalf("buttons %s", r.raw)
	}
}

func TestContentTooLong(t *testing.T) {
	s := newStand(t, Options{MaxContentRunes: 5})
	s.fake.AddGroupChat(42, "A")
	expectStatus(t, s.post("пятьб", map[string]any{"entity_id": 42}), http.StatusCreated)
	r := s.post("шестьб", map[string]any{"entity_id": 42})
	expectStatus(t, r, http.StatusUnprocessableEntity)
	if errorCode(t, r) != "too_long" {
		t.Fatalf("body %s", r.raw)
	}
	id := s.fake.Messages(42)[0].ID
	r = s.do(http.MethodPut, "/messages/"+strconv.FormatInt(id, 10), map[string]any{"message": map[string]any{"content": "toolong"}})
	expectStatus(t, r, http.StatusUnprocessableEntity)
	if errorCode(t, r) != "too_long" {
		t.Fatalf("put body %s", r.raw)
	}
}

func TestEditByBotOnlyAndButtons(t *testing.T) {
	s := newStand(t, Options{})
	s.fake.AddGroupChat(42, "A", 501)
	buttons := [][]map[string]any{{{"text": "Yes", "data": "yes"}}}
	r := s.post("draft", map[string]any{"entity_id": 42, "buttons": buttons})
	expectStatus(t, r, http.StatusCreated)
	path := "/messages/" + strconv.FormatInt(num(data(t, r)["id"]), 10)

	r = s.do(http.MethodPut, path, map[string]any{"message": map[string]any{"content": "edited"}})
	expectStatus(t, r, http.StatusOK)
	m := data(t, r)
	if m["content"] != "edited" || m["changed_at"] == nil {
		t.Fatalf("edit %s", r.raw)
	}
	if rows, _ := m["buttons"].([]any); len(rows) != 1 {
		t.Fatalf("absent buttons did not keep the keyboard: %s", r.raw)
	}
	r = s.do(http.MethodPut, path, map[string]any{"message": map[string]any{"buttons": []any{}}})
	expectStatus(t, r, http.StatusOK)
	if data(t, r)["buttons"] != nil || data(t, r)["content"] != "edited" {
		t.Fatalf("[] did not remove the keyboard: %s", r.raw)
	}
	r = s.do(http.MethodGet, path, nil)
	expectStatus(t, r, http.StatusOK)
	if data(t, r)["buttons"] != nil {
		t.Fatalf("GET after removal %s", r.raw)
	}

	theirs := s.fake.UserPosts(Post{UserID: 501, ChatID: 42, Content: "mine"})
	r = s.do(http.MethodPut, "/messages/"+strconv.FormatInt(theirs.ID, 10), map[string]any{"message": map[string]any{"content": "hijack"}})
	expectStatus(t, r, http.StatusForbidden)
	if errorCode(t, r) != "not_authorized" {
		t.Fatalf("body %s", r.raw)
	}
	if got, _ := s.fake.Message(theirs.ID); got.Content != "mine" {
		t.Fatalf("refused edit changed the message")
	}
	expectStatus(t, s.do(http.MethodPut, "/messages/1", map[string]any{"message": map[string]any{"content": "x"}}), http.StatusNotFound)
	expectStatus(t, s.do(http.MethodGet, "/messages/1", nil), http.StatusNotFound)
}

// listEvents reads one page of the event log.
func (s *stand) listEvents(query string) (ids []string, next string, hasNext bool, r response) {
	s.t.Helper()
	r = s.do(http.MethodGet, "/webhooks/events"+query, nil)
	if r.status != http.StatusOK {
		return nil, "", false, r
	}
	for _, e := range r.body["data"].([]any) {
		ids = append(ids, e.(map[string]any)["id"].(string))
	}
	pg := r.body["meta"].(map[string]any)["paginate"].(map[string]any)
	next, _ = pg["next_page"].(string)
	hasNext, _ = pg["has_next"].(bool)
	if next == "" {
		s.t.Fatalf("next_page empty: %s", r.raw)
	}
	return ids, next, hasNext, r
}

func TestEventsNewestFirstWithCursorPages(t *testing.T) {
	s := newStand(t, Options{})
	s.fake.AddGroupChat(42, "A", 501)
	var logged []string
	for i := range 6 {
		s.fake.UserPosts(Post{UserID: 501, ChatID: 42, Content: "m" + strconv.Itoa(i)})
	}
	for _, e := range s.fake.Events() {
		logged = append(logged, e.ID)
	}
	if len(logged) != 6 || !sort.SliceIsSorted(logged, func(i, j int) bool { return logged[i] > logged[j] }) {
		t.Fatalf("Events() not newest first: %v", logged)
	}

	page1, next, hasNext, r := s.listEvents("?limit=2")
	if !hasNext || len(page1) != 2 || page1[0] != logged[0] || page1[1] != logged[1] {
		t.Fatalf("page1 %v has_next %v", page1, hasNext)
	}
	first := r.body["data"].([]any)[0].(map[string]any)
	payload := first["payload"].(map[string]any)
	if first["event_type"] != "message_new" || payload["type"] != "message" || payload["event"] != "new" || payload["content"] != "m5" || payload["thread"] != nil || payload["parent_message_id"] != nil {
		t.Fatalf("event %s", r.raw)
	}

	// New events arriving between pages do not shift the walk back.
	s.fake.UserPosts(Post{UserID: 501, ChatID: 42, Content: "late"})
	page2, next, hasNext, _ := s.listEvents("?limit=2&cursor=" + next)
	if !hasNext || len(page2) != 2 || page2[0] != logged[2] || page2[1] != logged[3] {
		t.Fatalf("page2 %v", page2)
	}
	page3, next, hasNext, _ := s.listEvents("?limit=2&cursor=" + next)
	if hasNext || len(page3) != 2 || page3[0] != logged[4] || page3[1] != logged[5] {
		t.Fatalf("page3 %v has_next %v", page3, hasNext)
	}
	page4, _, hasNext, _ := s.listEvents("?limit=2&cursor=" + next)
	if hasNext || len(page4) != 0 {
		t.Fatalf("page4 %v", page4)
	}
	all, _, _, _ := s.listEvents("")
	if len(all) != 7 {
		t.Fatalf("default limit returned %d", len(all))
	}

	for _, q := range []string{"?limit=0", "?limit=51", "?limit=x", "?cursor=garbage!", "?cursor=eyJpZCI6MTAsImRpciI6ImFzYyJ9"} {
		if _, _, _, r := s.listEvents(q); r.status != http.StatusBadRequest {
			t.Errorf("%s: %d %s", q, r.status, r.raw)
		}
	}
}

func TestEventsEmptyLogStillCarriesNextPage(t *testing.T) {
	s := newStand(t, Options{})
	ids, next, hasNext, r := s.listEvents("")
	if len(ids) != 0 || hasNext || !strings.HasPrefix(string(r.raw), `{"data":[]`) {
		t.Fatalf("empty log %s", r.raw)
	}
	if ids, _, _, _ := s.listEvents("?cursor=" + next); len(ids) != 0 {
		t.Fatalf("floor cursor returned %v", ids)
	}
}

func TestDeleteEvent(t *testing.T) {
	s := newStand(t, Options{})
	s.fake.AddGroupChat(42, "A", 501)
	s.fake.UserPosts(Post{UserID: 501, ChatID: 42, Content: "x"})
	id := s.fake.Events()[0].ID
	r := s.do(http.MethodDelete, "/webhooks/events/"+id, nil)
	expectStatus(t, r, http.StatusNoContent)
	if len(r.raw) != 0 || len(s.fake.Events()) != 0 {
		t.Fatalf("delete left %v, body %q", s.fake.Events(), r.raw)
	}
	expectStatus(t, s.do(http.MethodDelete, "/webhooks/events/"+id, nil), http.StatusNotFound)
}

func TestHistoryDisabledLogsNothing(t *testing.T) {
	s := newStand(t, Options{HistoryDisabled: true})
	s.fake.AddGroupChat(42, "A", 501)
	m := s.fake.UserPosts(Post{UserID: 501, ChatID: 42, Content: "x"})
	expectStatus(t, s.post("bot", map[string]any{"entity_id": 42}), http.StatusCreated)
	if s.fake.LogRaw("custom", map[string]any{}) != "" {
		t.Fatalf("LogRaw logged with the history off")
	}
	_ = s.fake.UserClicks(501, m.ID, "nothing")
	ids, _, _, _ := s.listEvents("")
	if len(ids) != 0 || len(s.fake.Events()) != 0 {
		t.Fatalf("events %v", ids)
	}
}

func TestSelfMessagesAreLoggedUnlessIgnored(t *testing.T) {
	for _, ignore := range []bool{false, true} {
		s := newStand(t, Options{IgnoreSelfMessages: ignore})
		s.fake.AddGroupChat(42, "A", 501)
		r := s.post("bot says", map[string]any{"entity_id": 42, "buttons": [][]map[string]any{{{"text": "Go", "data": "go"}}}})
		expectStatus(t, r, http.StatusCreated)
		id := num(data(t, r)["id"])
		expectStatus(t, s.do(http.MethodPut, "/messages/"+strconv.FormatInt(id, 10), map[string]any{"message": map[string]any{"content": "bot edits"}}), http.StatusOK)
		if err := s.fake.UserClicks(501, id, "go"); err != nil {
			t.Fatal(err)
		}
		if err := s.fake.UserClicks(501, id, "stop"); err == nil {
			t.Fatalf("click on a missing button succeeded")
		}
		var types []string
		for _, e := range s.fake.Events() {
			types = append(types, e.EventType)
		}
		want := "button_click,message_update,message_new"
		if ignore {
			want = "button_click"
		}
		if strings.Join(types, ",") != want {
			t.Fatalf("ignore=%v: events %v, want %s", ignore, types, want)
		}
		var click map[string]any
		_ = json.Unmarshal(s.fake.Events()[0].Payload, &click)
		if click["type"] != "button" || click["event"] != "click" || click["data"] != "go" || num(click["message_id"]) != id || num(click["user_id"]) != 501 || num(click["chat_id"]) != 42 || len(click["trigger_id"].(string)) != 36 {
			t.Fatalf("click payload %v", click)
		}
	}
}

func TestMessagePayloadsForPersonalAndThread(t *testing.T) {
	s := newStand(t, Options{})
	s.fake.AddGroupChat(42, "A", 501)
	personal := s.fake.PersonalChat(501)
	s.fake.UserPosts(Post{UserID: 501, ChatID: personal, Content: "dm"})
	root := s.fake.UserPosts(Post{UserID: 501, ChatID: 42, Content: "root"})
	threadID, threadChat := s.fake.StartThread(root.ID)
	s.fake.UserPosts(Post{UserID: 501, ChatID: threadChat, Content: "in thread"})
	s.fake.UserEdits(root.ID, "root edited")

	events := s.fake.Events()
	payload := func(i int) map[string]any {
		var p map[string]any
		_ = json.Unmarshal(events[i].Payload, &p)
		return p
	}
	edit := payload(0)
	if events[0].EventType != "message_update" || edit["event"] != "update" || edit["content"] != "root edited" || edit["changed_at"] == nil {
		t.Fatalf("edit %v", edit)
	}
	inThread := payload(1)
	th, _ := inThread["thread"].(map[string]any)
	if inThread["entity_type"] != "thread" || num(inThread["entity_id"]) != threadID || num(inThread["chat_id"]) != threadChat ||
		th == nil || num(th["message_id"]) != root.ID || num(th["message_chat_id"]) != 42 {
		t.Fatalf("thread payload %v", inThread)
	}
	dm := payload(3)
	if dm["entity_type"] != "user" || num(dm["entity_id"]) != 501 || num(dm["chat_id"]) != personal || dm["thread"] != nil {
		t.Fatalf("personal payload %v", dm)
	}
	if num(dm["webhook_timestamp"]) == 0 {
		t.Fatalf("webhook_timestamp missing: %v", dm)
	}
}

func TestFaults(t *testing.T) {
	s := newStand(t, Options{})
	s.fake.AddGroupChat(42, "A")

	s.fake.AddFault(Fault{Route: "POST /messages", Status: http.StatusTooManyRequests, RetryAfter: 3, Times: 2})
	for range 2 {
		r := s.post("x", map[string]any{"entity_id": 42})
		expectStatus(t, r, http.StatusTooManyRequests)
		if r.header.Get("Retry-After") != "3" || string(r.raw) != "Too Many Requests" || !strings.HasPrefix(r.header.Get("Content-Type"), "text/plain") {
			t.Fatalf("429 %v %q", r.header, r.raw)
		}
	}
	if got := len(s.fake.Messages(42)); got != 0 {
		t.Fatalf("faulted requests stored %d messages", got)
	}
	expectStatus(t, s.post("x", map[string]any{"entity_id": 42}), http.StatusCreated)

	s.fake.AddFault(Fault{Route: "POST /messages", Status: http.StatusUnprocessableEntity, Contains: "poison"})
	expectStatus(t, s.post("clean", map[string]any{"entity_id": 42}), http.StatusCreated)
	r := s.post("poison pill", map[string]any{"entity_id": 42})
	expectStatus(t, r, http.StatusUnprocessableEntity)
	if errorCode(t, r) == "" {
		t.Fatalf("default fault body %s", r.raw)
	}
	expectStatus(t, s.post("poison again", map[string]any{"entity_id": 42}), http.StatusCreated)

	s.fake.AddFault(Fault{Route: "GET /webhooks/events", Status: http.StatusBadGateway, Body: "<html>bad gateway</html>", ContentType: "text/html"})
	r = s.do(http.MethodGet, "/webhooks/events", nil)
	if r.status != http.StatusBadGateway || r.header.Get("Content-Type") != "text/html" || string(r.raw) != "<html>bad gateway</html>" {
		t.Fatalf("502 %d %v %q", r.status, r.header, r.raw)
	}

	calls := s.fake.Calls("POST /messages")
	if len(calls) != 6 || calls[0].Status != http.StatusTooManyRequests || calls[2].Status != http.StatusCreated {
		t.Fatalf("calls %+v", calls)
	}
	s.fake.AddFault(Fault{Route: "PUT /messages", Status: 500})
	s.fake.Reset()
	if len(s.fake.Calls("")) != 0 {
		t.Fatalf("Reset kept the outbox")
	}
	if got := len(s.fake.Messages(42)); got != 3 {
		t.Fatalf("Reset dropped messages: %d", got)
	}
}

func TestSameMillisecondEventsKeepIDOrder(t *testing.T) {
	s := newStand(t, Options{})
	fixed := time.Date(2026, 10, 4, 12, 0, 0, 123_456_789, time.UTC)
	s.fake.SetNow(func() time.Time { return fixed })
	var ids []string
	for i := range 12 {
		ids = append(ids, s.fake.LogRaw("custom", map[string]any{"n": i}))
	}
	for i := 1; i < len(ids); i++ {
		if len(ids[i]) != 26 || ids[i] <= ids[i-1] {
			t.Fatalf("ids not increasing: %q then %q", ids[i-1], ids[i])
		}
	}
	events := s.fake.Events()
	for i, e := range events {
		if e.ID != ids[len(ids)-1-i] {
			t.Fatalf("event %d is %s, want %s", i, e.ID, ids[len(ids)-1-i])
		}
		if e.CreatedAt != "2026-10-04T12:00:00.123Z" {
			t.Fatalf("created_at %s", e.CreatedAt)
		}
	}
	page, next, _, _ := s.listEvents("?limit=5")
	rest, _, _, _ := s.listEvents("?limit=50&cursor=" + next)
	if len(page) != 5 || len(rest) != 7 || page[4] <= rest[0] {
		t.Fatalf("pages %v / %v", page, rest)
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	s := newStand(t, Options{})
	r := s.do(http.MethodGet, "/api/shared/v1/messages/1", nil)
	expectStatus(t, r, http.StatusNotFound)
	if calls := s.fake.Calls("GET /api/shared/v1/messages/1"); len(calls) != 1 {
		t.Fatalf("calls %+v", s.fake.Calls(""))
	}
}

func TestGetUser(t *testing.T) {
	s := New(Options{Token: "tok"})
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	s.AddUser(42, "anna", "Anna")
	get := func(path string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		req.Header.Set("Authorization", "Bearer tok")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, body := get("/users/42"); code != 200 || !strings.Contains(body, `"first_name":"Anna"`) || !strings.Contains(body, `"nickname":"anna"`) {
		t.Fatalf("GET /users/42: %d %s", code, body)
	}
	if code, _ := get("/users/999"); code != 404 {
		t.Fatalf("unknown user: %d", code)
	}
}
